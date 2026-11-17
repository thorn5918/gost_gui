package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"s5picker/util"
)

// APIServer 本地 HTTP 控制 API（仅监听 127.0.0.1，用于脚本/自动化触发）。
//
// 路由：
//
//	GET  /status              当前代理状态
//	GET  /proxies             代理列表及检测结果
//	POST /switch[?province=x] 一键切换最快代理（可限定省份）
//	POST /use?raw=socks5://.. 使用指定代理（复检后启用）
//	POST /checkall            触发批量检测
//	POST /stop                停止转发
type APIServer struct {
	app *App
	srv *http.Server
}

// NewAPIServer 构建 API 服务（未启动）。
func NewAPIServer(app *App) *APIServer {
	s := &APIServer{app: app}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/proxies", s.handleProxies)
	mux.HandleFunc("/switch", s.handleSwitch)
	mux.HandleFunc("/use", s.handleUse)
	mux.HandleFunc("/checkall", s.handleCheckAll)
	mux.HandleFunc("/stop", s.handleStop)
	s.srv = &http.Server{
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Start 监听 127.0.0.1:port。阻塞，请在 goroutine 中调用。
func (s *APIServer) Start(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	s.srv.Addr = addr
	util.Log.Infof("控制 API 已启动: http://%s/status", addr)
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		util.Log.Errorf("控制 API 启动失败(%s): %v（端口被占用？请修改 config.json 的 api_port）", addr, err)
		return err
	}
	return nil
}

// Close 停止 API 服务。
func (s *APIServer) Close() {
	_ = s.srv.Close()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type apiProxy struct {
	Raw       string `json:"raw"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	ExitIP    string `json:"exit_ip"`
	Province  string `json:"province"`
	City      string `json:"city"`
	Active    bool   `json:"active"`
}

func toAPIProxy(p *Proxy, active *Proxy) apiProxy {
	lat := int64(-1)
	if p.Latency > 0 {
		lat = p.Latency.Milliseconds()
	}
	return apiProxy{
		Raw: p.Raw, Status: p.Status.String(), LatencyMs: lat,
		ExitIP: p.ExitIP, Province: p.Province, City: p.City,
		Active: active != nil && active.Raw == p.Raw,
	}
}

func (s *APIServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	active := s.app.Pool.Active()
	total, okCnt := s.app.Pool.Count()
	var ap *apiProxy
	if active != nil {
		x := toAPIProxy(active, active)
		ap = &x
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"active":     ap,
		"running":    s.app.Gost.Running(),
		"socks5":     s.app.Gost.SocksAddr(),
		"http":       s.app.Gost.HTTPAddr(),
		"total":      total,
		"available":  okCnt,
		"switching_supported": true,
	})
}

func (s *APIServer) handleProxies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	active := s.app.Pool.Active()
	items := make([]apiProxy, 0)
	for _, p := range s.app.Pool.All() {
		items = append(items, toAPIProxy(p, active))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items})
}

func (s *APIServer) handleSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "use POST"})
		return
	}
	province := strings.TrimSpace(r.URL.Query().Get("province"))
	// 同步执行：切换含复检可能耗时数秒，完成后返回最终结果；
	// 并发重复请求会被 Switcher 内部 TryLock 拒绝并返回 busy 错误。
	p, err := s.app.Sw.SwitchBest(province)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "proxy": toAPIProxy(p, p)})
}

func (s *APIServer) handleUse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "use POST"})
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("raw"))
	if raw == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing ?raw="})
		return
	}
	p := s.app.Pool.Get(raw)
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "代理不在列表中: " + raw})
		return
	}
	if err := s.app.Sw.UseProxy(p); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "proxy": toAPIProxy(p, p)})
}

func (s *APIServer) handleCheckAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "use POST"})
		return
	}
	total, _ := s.app.Pool.Count()
	if total == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "代理列表为空"})
		return
	}
	s.app.Checker.CheckAll() // 异步，结果轮询 /proxies
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": fmt.Sprintf("已开始检测 %d 条", total)})
}

func (s *APIServer) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "use POST"})
		return
	}
	s.app.Sw.StopProxy()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已停止代理转发"})
}
