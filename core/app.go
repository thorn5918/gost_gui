package core

import (
	"fmt"
	"sync"
	"time"

	"s5picker/util"
)

// App 聚合全部引擎组件，供 UI 与 CLI 共用。
type App struct {
	Pool    *Pool
	Checker *Checker
	Gost    *GostProc
	Sw      *Switcher
	Cfg     Config
	CfgPath string
}

// NewApp 组装引擎。exeDir 为程序所在目录。
func NewApp(exeDir string) (*App, error) {
	cfg, cfgPath := LoadConfig(exeDir)

	util.Log.SetFileDir(exeDir)

	pool := NewPool()
	checker := NewChecker(pool)
	checker.Concurrency = cfg.CheckerConcurrency
	checker.Timeout = time.Duration(cfg.CheckTimeoutSec) * time.Second

	gost := NewGostProc()
	gost.Socks5Port = cfg.Socks5Port
	gost.HTTPPort = cfg.HTTPPort
	gost.DirectWhenInactive = cfg.DirectWhenInactive

	sw := &Switcher{Pool: pool, Checker: checker, Gost: gost}

	app := &App{
		Pool:    pool,
		Checker: checker,
		Gost:    gost,
		Sw:      sw,
		Cfg:     cfg,
		CfgPath: cfgPath,
	}

	// gost 崩溃自动重启
	go gost.MonitorLoop()

	// 加载代理列表
	proxiesPath := exeDir + "/proxies.txt"
	if _, _, err := pool.LoadFile(proxiesPath); err != nil {
		util.Log.Errorf("读取代理列表失败: %v（请确保 proxies.txt 与程序同目录）", err)
	} else {
		total, _ := pool.Count()
		util.Log.Infof("已加载代理 %d 条（%s）", total, proxiesPath)
	}
	return app, nil
}

// ReloadProxies 重新加载列表。
func (a *App) ReloadProxies() {
	added, bad, err := a.Pool.LoadFile(exeDir() + "/proxies.txt")
	if err != nil {
		util.Log.Errorf("读取代理列表失败: %v", err)
		return
	}
	util.Log.Infof("代理列表已刷新：新增 %d 条", added)
	for _, b := range bad {
		util.Log.Errorf("无法解析的行: %s", b)
	}
}

// 惰性缓存 exe 目录（ReloadProxies 多次调用避免重复取）。
var (
	exeDirOnce sync.Once
	exeDirPath string
)

func exeDir() string {
	exeDirOnce.Do(func() { exeDirPath = util.ExeDir() })
	return exeDirPath
}

func (a *App) Shutdown() {
	a.Gost.Shutdown()
	util.Log.Close()
}

// StartLocalProxy 用当前 active 或直连方式启动本地转发。
// active 为空且 !DirectWhenInactive 时仅启动监听（无上游）。
func (a *App) StartLocalProxy() error {
	if p := a.Pool.Active(); p != nil {
		return a.Gost.Start(p.UpstreamURL())
	}
	return a.Gost.Start("")
}

func (a *App) StatusText() string {
	total, okCnt := a.Pool.Count()
	if p := a.Pool.Active(); p != nil {
		return fmt.Sprintf("可用 %d/%d · 当前 %s", okCnt, total, p.Label())
	}
	return fmt.Sprintf("可用 %d/%d · 未启用转发", okCnt, total)
}
