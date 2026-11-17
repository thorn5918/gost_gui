package core

import (
	"os/exec"
	"testing"
	"time"

	"s5picker/util"
)

// TestGostProcUpstream 验证 -F 上游链路转发。
func TestGostProcUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过")
	}
	// 用同一个内嵌 gost 起一个"上游" socks5（直连出口）
	gp := NewGostProc()
	upExe, err := gp.EnsureExtracted()
	if err != nil {
		t.Fatalf("释放 gost 失败: %v", err)
	}

	upCmd := exec.Command(upExe, "-L", "socks5://127.0.0.1:15600")
	if err := upCmd.Start(); err != nil {
		t.Fatalf("启动上游 gost 失败: %v", err)
	}
	defer upCmd.Process.Kill()
	time.Sleep(500 * time.Millisecond)

	g := NewGostProc()
	g.Socks5Port = 55578
	g.HTTPPort = 55579
	defer g.Shutdown()

	if err := g.Start("socks5://127.0.0.1:15600"); err != nil {
		t.Fatalf("启动本地 gost 失败: %v", err)
	}
	t.Logf("本地 %s -> 上游 127.0.0.1:15600", g.SocksAddr())

	res := util.QueryViaProxy("socks5://"+g.SocksAddr(), 8*time.Second)
	if !res.OK {
		t.Fatalf("链式转发失败: %v", res.Err)
	}
	if res.ExitIP == "" {
		t.Fatal("未取到出口IP")
	}
	t.Logf("链式出口IP=%s 省=%s 市=%s 延迟=%v", res.ExitIP, res.Province, res.City, res.Latency)

	// HTTP 入站口也应可用
	out, err := exec.Command("curl", "-s", "--max-time", "8",
		"-x", "http://"+g.HTTPAddr(),
		"https://cz88.net/api/cz88/ip/base?ip=").CombinedOutput()
	if err != nil {
		t.Fatalf("HTTP 入站失败: %v %s", err, out)
	}
	if len(out) < 10 {
		t.Fatal("HTTP 入站响应异常")
	}
	t.Logf("HTTP 入站响应片段: %.60s...", string(out))
	
}
