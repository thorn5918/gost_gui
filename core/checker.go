package core

import (
	"fmt"
	"sync"
	"time"

	"s5picker/util"
)
// Checker 批量检测器。
type Checker struct {
	Pool        *Pool
	Concurrency int
	Timeout     time.Duration
}

func NewChecker(pool *Pool) *Checker {
	return &Checker{Pool: pool, Concurrency: 32, Timeout: 5 * time.Second}
}

// CheckAll 对池内所有未在检测中的代理并发检测。
// 立即返回，结果通过 Pool.Notify 回调。
func (c *Checker) CheckAll() {
	items := c.Pool.All()
	var targets []*Proxy
	for _, it := range items {
		if c.Pool.SetChecking(it.Raw) {
			targets = append(targets, it)
		}
	}
	if len(targets) == 0 {
		return
	}
	conc := c.Concurrency
	if conc <= 0 {
		conc = 32
	}
	done := make(chan struct{}, conc)
	go func() {
		sem := make(chan struct{}, conc)
		for _, t := range targets {
			sem <- struct{}{}
			go func(p *Proxy) {
				defer func() { <-sem; done <- struct{}{} }()
				c.CheckOne(p)
			}(t)
		}
	}()
}

// CheckAllSync 同步版本：阻塞直到全部检测完成（CLI 自测用）。
func (c *Checker) CheckAllSync() {
	items := c.Pool.All()
	conc := c.Concurrency
	if conc <= 0 {
		conc = 32
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for _, t := range items {
		if !c.Pool.SetChecking(t.Raw) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(p *Proxy) {
			defer wg.Done()
			defer func() { <-sem; c.Pool.UnsetChecking(p.Raw) }()
			r := util.QueryViaProxy(p.Raw, c.timeout())
			if r.OK {
				util.Log.Checkf("%s ✓ %vms 出口:%s %s%s", p.Label(),
					r.Latency.Milliseconds(), r.ExitIP, r.Province, r.City)
				c.Pool.Update(p.Raw, StatusOK, r.Latency, r.ExitIP, r.Province, r.City)
			} else {
				util.Log.Checkf("%s ✗ %v", p.Label(), r.Err)
				c.Pool.Update(p.Raw, StatusFail, r.Latency, "", "", "")
			}
		}(t)
	}
	wg.Wait()
}

// CheckOne 检测单个代理并更新池。同步阻塞。
func (c *Checker) CheckOne(p *Proxy) util.GeoResult {
	if !c.Pool.SetChecking(p.Raw) {
		return util.GeoResult{Err: fmt.Errorf("正在检测中")}
	}
	defer c.Pool.UnsetChecking(p.Raw)

	r := util.QueryViaProxy(p.Raw, c.timeout())
	if r.OK {
		util.Log.Checkf("%s ✓ %vms 出口:%s %s%s", p.Label(),
			r.Latency.Milliseconds(), r.ExitIP, r.Province, r.City)
		c.Pool.Update(p.Raw, StatusOK, r.Latency, r.ExitIP, r.Province, r.City)
	} else {
		util.Log.Checkf("%s ✗ %v", p.Label(), r.Err)
		c.Pool.Update(p.Raw, StatusFail, r.Latency, "", "", "")
	}
	return r
}

// QuickCheck 快速复检（用于一键切换前的确认）。
func (c *Checker) QuickCheck(p *Proxy) bool {
	r := util.QueryViaProxy(p.Raw, 3*time.Second)
	if r.OK {
		c.Pool.Update(p.Raw, StatusOK, r.Latency, r.ExitIP, r.Province, r.City)
	} else {
		c.Pool.Update(p.Raw, StatusFail, 0, "", "", "")
	}
	return r.OK
}

func (c *Checker) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 5 * time.Second
	}
	return c.Timeout
}
