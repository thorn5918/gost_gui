package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCheckOneNoStuckChecking 回归测试：CheckOne 结束后必须清除"检测中"标记，
// 且失败的结果要落库（修复前：CheckAll 标记后 CheckOne 二次 SetChecking
// 失败直接返回，导致全部卡在"检测中"）。
func TestCheckOneNoStuckChecking(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "proxies.txt")
	// 127.0.0.1:1 几乎必然连接失败，用于走失败路径
	if err := os.WriteFile(list, []byte("socks5://127.0.0.1:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pool := NewPool()
	if _, _, err := pool.LoadFile(list); err != nil {
		t.Fatal(err)
	}
	items := pool.All()
	if len(items) != 1 {
		t.Fatalf("加载条数不对: %d", len(items))
	}
	p := items[0]

	c := NewChecker(pool)
	c.Timeout = 2 * time.Second

	// 模拟 CheckAll 的行为：先标记，再 CheckOne
	if !pool.SetChecking(p.Raw) {
		t.Fatal("SetChecking 应成功")
	}
	c.CheckOne(p)

	// 1) 状态必须落为 Fail，而不是停留在 Checking
	if p.Status != StatusFail {
		t.Fatalf("状态未落库，仍为 %s", p.Status)
	}
	// 2) 标记必须已解除：再次 SetChecking 应成功
	if !pool.SetChecking(p.Raw) {
		t.Fatal("检测中标记未解除，会卡死 UI 显示")
	}
	pool.UnsetChecking(p.Raw)
}

// TestCheckAllCompletes CheckAll 后所有行都应离开"检测中"状态。
func TestCheckAllCompletes(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "proxies.txt")
	content := "socks5://127.0.0.1:1\nsocks5://127.0.0.1:2\nsocks5://127.0.0.1:3\n"
	if err := os.WriteFile(list, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pool := NewPool()
	if _, _, err := pool.LoadFile(list); err != nil {
		t.Fatal(err)
	}
	c := NewChecker(pool)
	c.Timeout = 2 * time.Second
	c.CheckAll()

	deadline := time.Now().Add(20 * time.Second)
	for {
		items := pool.All()
		stuck := 0
		for _, it := range items {
			if it.Status == StatusChecking {
				stuck++
			}
		}
		if stuck == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d 条卡在检测中（回归 bug 复现）", stuck)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
