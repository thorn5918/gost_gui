package core

import (
	"strings"
	"testing"
	"time"

	"s5picker/util"
)

// TestRapidSwitchNoRestartLoop 回归测试：连续快速切换上游，
// 旧实例的 Wait goroutine 不得误报"异常退出"触发自动重启风暴。
// （修复前：Stop 置 manualStop=true 后 Start 立即置回 false，
//   旧实例 goroutine 晚调度读到 false → 误判 → 无限杀/启循环）
func TestRapidSwitchNoRestartLoop(t *testing.T) {
	g := NewGostProc()
	g.Socks5Port = 55588
	g.HTTPPort = 55589
	defer g.Shutdown()

	logCh := util.Log.Subscribe()
	defer util.Log.Unsubscribe(logCh)

	// 模拟用户连点三次"一键切换"
	if err := g.Start(""); err != nil {
		t.Fatalf("第1次启动失败: %v", err)
	}
	if err := g.Start(""); err != nil {
		t.Fatalf("第2次启动失败: %v", err)
	}
	if err := g.Start(""); err != nil {
		t.Fatalf("第3次启动失败: %v", err)
	}

	// 观察窗口：若存在重启风暴，期间必然出现"异常退出/自动重启"日志
	time.Sleep(4 * time.Second)
	drain := func() []string {
		var found []string
		for {
			select {
			case e := <-logCh:
				if strings.Contains(e.Text, "异常退出") || strings.Contains(e.Text, "自动重启") {
					found = append(found, e.Text)
				}
			default:
				return found
			}
		}
	}
	if bad := drain(); len(bad) > 0 {
		t.Fatalf("检测到重启风暴日志: %v", bad)
	}

	// 实例应当存活且可用
	if !g.Running() {
		t.Fatal("切换后 gost 未在运行")
	}
	res := queryVia(t, "socks5://"+g.SocksAddr())
	if !res.OK {
		t.Fatalf("最终实例不可用: %v", res.Err)
	}
	// 再兜底确认观察窗口内没有风暴日志
	if bad := drain(); len(bad) > 0 {
		t.Fatalf("后续出现重启风暴日志: %v", bad)
	}
}
