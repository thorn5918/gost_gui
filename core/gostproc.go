package core

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"s5picker/internal/gostbin"
	"s5picker/util"
)

// gostInstance 一次启动的 gost 子进程实例。
// 每个实例持有独立的 done 通道：Stop 先关闭自己的 done 再 Kill，
// 这样该实例的 Wait goroutine 能可靠区分"人为停止"与"异常退出"，
// 不会误触发自动重启（修复共享 manualStop 的竞态）。
type gostInstance struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// GostProc gost.exe 子进程托管。
// 职责：释放内嵌 gost -> 启动双协议监听 -> 切换上游时重启 -> 崩溃自动拉起。
type GostProc struct {
	Socks5Port int
	HTTPPort   int
	// DirectWhenInactive 未激活上游时是否直连。
	DirectWhenInactive bool

	mu         sync.Mutex // 串行化 Start/Stop，防止用户点击与自动重启并发
	inst       *gostInstance
	exePath    string
	upstream   atomic.Value // string
	running    atomic.Bool
	restartCh  chan struct{}
	stopCh     chan struct{}
	socksAddr  atomic.Value // string
	httpAddr   atomic.Value // string
	backoff    time.Duration
	consecFail int
}

func NewGostProc() *GostProc {
	return &GostProc{restartCh: make(chan struct{}, 1), stopCh: make(chan struct{}), backoff: 2 * time.Second}
}

// EnsureExtracted 把内嵌 gost（gzip 压缩）解压释放到用户数据目录。
func (g *GostProc) EnsureExtracted() (string, error) {
	if g.exePath != "" {
		return g.exePath, nil
	}
	if len(gostbin.Bytes) == 0 {
		return "", fmt.Errorf("本平台未内嵌 gost")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	dir := filepath.Join(base, "S5Picker", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, gostbin.ExeName)

	// 用 .size 文件记录内嵌数据长度，与磁盘上的 gost 比对决定是否重新释放
	sizeFile := path + ".size"
	var gzLen int
	if st, err := os.Stat(sizeFile); err == nil && st.Size() > 0 {
		fmt.Sscanf(readFileString(sizeFile), "%d", &gzLen)
	}
	if gzLen != len(gostbin.Bytes) || !fileExists(path) {
		if err := gunzipTo(gostbin.Bytes, path); err != nil {
			return "", err
		}
		_ = os.WriteFile(sizeFile, []byte(fmt.Sprintf("%d", len(gostbin.Bytes))), 0o644)
		if runtime.GOOS != "windows" {
			_ = os.Chmod(path, 0o755)
		}
		util.Log.Infof("已释放 gost 内核: %s", path)
	}
	g.exePath = path
	return path, nil
}

func readFileString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func gunzipTo(data []byte, dest string) error {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer zr.Close()
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, zr)
	return err
}

// Start 以指定上游启动（upstream 为空则直连）。
func (g *GostProc) Start(upstream string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.startLocked(upstream)
}

func (g *GostProc) startLocked(upstream string) error {
	exe, err := g.EnsureExtracted()
	if err != nil {
		return err
	}
	g.stopLocked()

	g.upstream.Store(upstream)

	socksPort, err := g.freePort(g.Socks5Port)
	if err != nil {
		return err
	}
	httpPort, err := g.freePort(g.HTTPPort)
	if err != nil {
		return err
	}

	args := []string{
		"-L", fmt.Sprintf("socks5://:%d", socksPort),
		"-L", fmt.Sprintf("http://:%d", httpPort),
	}
	if upstream != "" {
		args = append(args, "-F", upstream)
	}

	inst := &gostInstance{done: make(chan struct{})}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = &logWriter{}
	cmd.Stderr = &logWriter{}
	cmd.SysProcAttr = sysProcAttr() // Windows: 隐藏控制台窗口
	if err := cmd.Start(); err != nil {
		return err
	}
	inst.cmd = cmd
	g.inst = inst
	g.socksAddr.Store(fmt.Sprintf("127.0.0.1:%d", socksPort))
	g.httpAddr.Store(fmt.Sprintf("127.0.0.1:%d", httpPort))
	g.running.Store(true)

	// 监控 goroutine：只对"自己这个实例"负责。
	go func(inst *gostInstance) {
		err := cmd.Wait()
		g.running.Store(false)

		// 人为停止（Stop/Shutdown/被下一次 Start 替换）→ 静默返回
		select {
		case <-inst.done:
			return
		default:
		}

		// 确认当前实例仍是自己（用户没有另外手动启动过）
		g.mu.Lock()
		stillCurrent := g.inst == inst
		if stillCurrent {
			g.inst = nil
		}
		g.mu.Unlock()
		if !stillCurrent {
			return
		}

		util.Log.Errorf("gost 内核异常退出(%v)，稍后自动重启", err)
		select {
		case g.restartCh <- struct{}{}:
		default:
		}
	}(inst)

	// 就绪检测：等本地端口可连
	if err := g.waitReady(socksPort, 3*time.Second); err != nil {
		return err
	}
	g.consecFail = 0
	g.backoff = 2 * time.Second
	if upstream != "" {
		util.Log.FwdLogf("本地代理已启动 %s(socks5) / %s(http)，上游 %s",
			g.SocksAddr(), g.HTTPAddr(), upstream)
	} else {
		util.Log.FwdLogf("本地代理已启动 %s(socks5) / %s(http)，直连模式",
			g.SocksAddr(), g.HTTPAddr())
	}
	return nil
}

// stopLocked 停止当前实例（须持有 g.mu）。
func (g *GostProc) stopLocked() {
	if inst := g.inst; inst != nil {
		// 先标记人为停止，再杀进程，顺序不可颠倒
		close(inst.done)
		if inst.cmd.Process != nil {
			_ = inst.cmd.Process.Kill()
		}
		_ = inst.cmd.Wait()
	}
	g.inst = nil
	g.running.Store(false)
}

// waitReady 轮询等待端口可连。
func (g *GostProc) waitReady(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("等待本地端口 %s 就绪超时", addr)
}

func (g *GostProc) freePort(prefer int) (int, error) {
	for p := prefer; p < prefer+20; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			l.Close()
			return p, nil
		}
	}
	// 全被占用则随机
	p := 20000 + rand.Intn(20000)
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		return 0, err
	}
	l.Close()
	return p, nil
}

// Stop 停止子进程。
func (g *GostProc) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopLocked()
}

// MonitorLoop 自动重启循环：带退避（2s 起，×2，上限 30s），
// 连续失败 5 次放弃自动重启并提示用户。
func (g *GostProc) MonitorLoop() {
	for {
		select {
		case <-g.stopCh:
			return
		case <-g.restartCh:
			g.mu.Lock()
			// 用户已经手动启动了新实例（比如又点了一次切换）→ 不用自动重启
			if g.inst != nil {
				g.mu.Unlock()
				continue
			}
			up, _ := g.upstream.Load().(string)
			g.consecFail++
			if g.consecFail > 5 {
				g.mu.Unlock()
				util.Log.Errorf("gost 连续 %d 次异常退出，已停止自动重启。请检查上游代理地址或重启程序", g.consecFail-1)
				continue
			}
			delay := g.backoff
			g.backoff *= 2
			if g.backoff > 30*time.Second {
				g.backoff = 30 * time.Second
			}
			util.Log.Infof("%v 后自动重启 gost（第 %d 次尝试）", delay, g.consecFail)
			g.mu.Unlock()

			time.Sleep(delay)
			select {
			case <-g.stopCh:
				return
			default:
			}

			g.mu.Lock()
			if g.inst != nil { // 等待期间用户手动启动了
				g.mu.Unlock()
				continue
			}
			err := g.startLocked(up)
			g.mu.Unlock()
			if err != nil {
				util.Log.Errorf("gost 自动重启失败: %v", err)
				select {
				case g.restartCh <- struct{}{}:
				default:
				}
			}
		}
	}
}

// Shutdown 停止一切。
func (g *GostProc) Shutdown() {
	close(g.stopCh)
	g.Stop()
}

func (g *GostProc) SocksAddr() string {
	s, _ := g.socksAddr.Load().(string)
	if s == "" {
		return fmt.Sprintf("127.0.0.1:%d", g.Socks5Port)
	}
	return s
}

func (g *GostProc) HTTPAddr() string {
	s, _ := g.httpAddr.Load().(string)
	if s == "" {
		return fmt.Sprintf("127.0.0.1:%d", g.HTTPPort)
	}
	return s
}

func (g *GostProc) Running() bool { return g.running.Load() }

// logWriter 把 gost 的 stdout/stderr 导入日志。
type logWriter struct{}

func (logWriter) Write(b []byte) (int, error) {
	n := len(b)
	util.Log.FwdLogf("%s", trimLine(b))
	return n, nil
}

func trimLine(b []byte) string {
	s := string(b)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
