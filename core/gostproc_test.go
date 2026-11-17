package core

import (
	"testing"
	"time"

	"s5picker/util"
)

// TestGostProcE2E 端到端验证：释放内嵌 gost -> 启动 -> 通过本地端口访问外网。
func TestGostProcE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过")
	}
	g := NewGostProc()
	g.Socks5Port = 55568
	g.HTTPPort = 55569
	defer g.Shutdown()

	if err := g.Start(""); err != nil {
		t.Fatalf("启动 gost 失败: %v", err)
	}
	if !g.Running() {
		t.Fatal("gost 未标记运行")
	}
	t.Logf("本地监听: %s / %s", g.SocksAddr(), g.HTTPAddr())

	// 通过我们托管的本地 socks5 端口访问 cz88（即完整转发链路）
	res := queryVia(t, "socks5://"+g.SocksAddr())
	if !res.OK {
		t.Fatalf("经本地代理访问外网失败: %v", res.Err)
	}
	t.Logf("出口IP=%s 省=%s 市=%s 延迟=%v", res.ExitIP, res.Province, res.City, res.Latency)
	if res.ExitIP == "" || res.Province == "" {
		t.Fatalf("cz88 解析不完整")
	}

	// 切换上游为空 -> 再 Start 一次，验证重启逻辑
	if err := g.Start(""); err != nil {
		t.Fatalf("重启 gost 失败: %v", err)
	}
	// 停止后 Running 应为 false
	g.Stop()
	if g.Running() {
		t.Fatal("Stop 后仍标记运行")
	}
}

// queryVia 复用 cz88 检测链路。
func queryVia(t *testing.T, proxyURL string) util.GeoResult {
	t.Helper()
	return util.QueryViaProxy(proxyURL, 8*time.Second)
}
