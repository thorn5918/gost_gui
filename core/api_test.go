package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	list := filepath.Join(dir, "proxies.txt")
	content := "socks5://127.0.0.1:1\nsocks5://127.0.0.1:2\n"
	if err := os.WriteFile(list, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pool := NewPool()
	if _, _, err := pool.LoadFile(list); err != nil {
		t.Fatal(err)
	}
	checker := NewChecker(pool)
	g := NewGostProc() // 不真正启动
	sw := &Switcher{Pool: pool, Checker: checker, Gost: g}
	return &App{Pool: pool, Checker: checker, Gost: g, Sw: sw}
}

func apiReq(t *testing.T, h http.Handler, method, target string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

// TestAPIRoutes 覆盖 /status /proxies /switch /use /checkall /stop。
func TestAPIRoutes(t *testing.T) {
	app := newTestApp(t)
	api := NewAPIServer(app)

	// status
	code, m := apiReq(t, api.srv.Handler, "GET", "/status")
	if code != 200 || m["ok"] != true {
		t.Fatalf("status: code=%d m=%v", code, m)
	}
	if m["total"].(float64) != 2 {
		t.Fatalf("status total = %v", m["total"])
	}

	// proxies
	code, m = apiReq(t, api.srv.Handler, "GET", "/proxies")
	if code != 200 {
		t.Fatal("proxies code != 200")
	}
	items := m["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("proxies items = %d", len(items))
	}

	// switch：无可用代理 → ok=false 且不 panic
	code, m = apiReq(t, api.srv.Handler, "POST", "/switch")
	if code != 200 || m["ok"] != false {
		t.Fatalf("switch: code=%d m=%v", code, m)
	}
	if msg, _ := m["error"].(string); !strings.Contains(msg, "没有可用代理") {
		t.Fatalf("switch error = %v", m["error"])
	}

	// switch 并发第二次应返回 busy 错误而不是卡死
	app.Sw.busy.Lock()
	code, m = apiReq(t, api.srv.Handler, "POST", "/switch")
	app.Sw.busy.Unlock()
	if code != 200 || m["ok"] != false || !strings.Contains(m["error"].(string), "正在切换") {
		t.Fatalf("busy switch: code=%d m=%v", code, m)
	}

	// use：不存在的代理 → 404
	code, m = apiReq(t, api.srv.Handler, "POST", "/use?raw=socks5://9.9.9.9:9")
	if code != 404 {
		t.Fatalf("use unknown: code=%d", code)
	}

	// checkall
	code, m = apiReq(t, api.srv.Handler, "POST", "/checkall")
	if code != 200 || m["ok"] != true {
		t.Fatalf("checkall: code=%d m=%v", code, m)
	}
	// 等检测（127.0.0.1:1/:2 必失败）完成后不卡"检测中"
	deadline := time.Now().Add(10 * time.Second)
	for {
		items := app.Pool.All()
		stuck := 0
		for _, it := range items {
			if it.Status == StatusChecking {
				stuck++
			}
		}
		if stuck == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("checkall 后仍卡在检测中")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// stop
	code, m = apiReq(t, api.srv.Handler, "POST", "/stop")
	if code != 200 || m["ok"] != true {
		t.Fatalf("stop: code=%d m=%v", code, m)
	}
}

// TestAPIProvinceSwitch 验证带省份参数的切换在无该省代理时快速失败。
func TestAPIProvinceSwitch(t *testing.T) {
	app := newTestApp(t)
	api := NewAPIServer(app)
	code, m := apiReq(t, api.srv.Handler, "POST", "/switch?province=%E5%B9%BF%E4%B8%9C")
	if code != 200 || m["ok"] != false {
		t.Fatalf("province switch: code=%d m=%v", code, m)
	}
	if !strings.Contains(m["error"].(string), "广东") {
		t.Fatalf("error = %v", m["error"])
	}
}
