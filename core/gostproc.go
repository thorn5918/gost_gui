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
	"sync/atomic"
	"time"

	"s5picker/internal/gostbin"
	"s5picker/util"
)

// GostProc gost.exe 子进程托管。
// 职责：释放内嵌 gost -> 启动双协议监听 -> 切换上游时重启 -> 崩溃自动拉起。
type GostProc struct {
	Socks5Port int
	HTTPPort   int
	// DirectWhenInactive 未激活上游时是否直连。
	DirectWhenInactive bool

	cmd        *exec.Cmd
	exePath    string
	upstream   atomic.Value // string
	running    atomic.Bool
	manualStop atomic.Bool
	restartCh  chan struct{}
	stopCh     chan struct{}
	socksAddr  atomic.Value // string
	httpAddr   atomic.Value // string
}

func NewGostProc() *GostProc {
	return &GostProc{restartCh: make(chan struct{}, 1), stopCh: make(chan struct{})}
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

	// 已存在且为合法 gzip 数据解出来的大小一致则复用：
	// 以 gzip 内容大小校验（解压后的字节数写入 .size 文件）
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

// Start 以指定上游启动（upstream 为空则直连，受 DirectWhenInactive 控制）。
func (g *GostProc) Start(upstream string) error {
	exe, err := g.EnsureExtracted()
	if err != nil {
		return err
	}
	g.Stop()

	g.manualStop.Store(false)
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

	cmd := exec.Command(exe, args...)
	cmd.Stdout = &logWriter{}
	cmd.Stderr = &logWriter{}
	if err := cmd.Start(); err != nil {
		return err
	}
	g.cmd = cmd
	g.socksAddr.Store(fmt.Sprintf("127.0.0.1:%d", socksPort))
	g.httpAddr.Store(fmt.Sprintf("127.0.0.1:%d", httpPort))
	g.running.Store(true)

	go func() {
		err := cmd.Wait()
		g.running.Store(false)
		if g.manualStop.Load() {
			return
		}
		if err != nil {
			util.Log.Errorf("gost 内核异常退出(%v)，2 秒后自动重启", err)
		} else {
			util.Log.Switchf("gost 内核已退出，2 秒后自动重启")
		}
		select {
		case g.restartCh <- struct{}{}:
		default:
		}
	}()

	// 就绪检测：等本地端口可连
	if err := g.waitReady(socksPort, 3*time.Second); err != nil {
		return err
	}
	if upstream != "" {
		util.Log.FwdLogf("本地代理已启动 %s(socks5) / %s(http)，上游 %s",
			g.SocksAddr(), g.HTTPAddr(), upstream)
	} else {
		util.Log.FwdLogf("本地代理已启动 %s(socks5) / %s(http)，直连模式",
			g.SocksAddr(), g.HTTPAddr())
	}
	return nil
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
	g.manualStop.Store(true)
	if g.cmd != nil && g.cmd.Process != nil {
		_ = g.cmd.Process.Kill()
		_ = g.cmd.Wait()
	}
	g.cmd = nil
	g.running.Store(false)
}

// RestartAsync 请求自动重启（由 monitor 通道触发）。
func (g *GostProc) MonitorLoop() {
	for {
		select {
		case <-g.stopCh:
			return
		case <-g.restartCh:
			up, _ := g.upstream.Load().(string)
			if err := g.Start(up); err != nil {
				util.Log.Errorf("gost 重启失败: %v", err)
				time.AfterFunc(2*time.Second, func() {
					select {
					case g.restartCh <- struct{}{}:
					default:
					}
				})
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
