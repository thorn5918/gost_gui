package core

import (
	"errors"
	"fmt"
	"sync"

	"s5picker/util"
)

// Switcher 一键切换：选最快的可用代理，用前复检，然后重启 gost。
// 内部互斥锁防止 UI 与 HTTP API 同时触发切换。
type Switcher struct {
	Pool    *Pool
	Checker *Checker
	Gost    *GostProc

	busy sync.Mutex
}

// SwitchBest 执行一键切换。province 非空时只在指定省份里挑最快。
// 成功返回使用的代理；失败返回错误。
func (s *Switcher) SwitchBest(province string) (*Proxy, error) {
	if !s.busy.TryLock() {
		util.Log.Switchf("正在切换中，忽略重复请求")
		return nil, errors.New("正在切换中，请稍候")
	}
	defer s.busy.Unlock()

	cands := s.Pool.CandidatesIn(province)
	if len(cands) == 0 {
		if province != "" {
			util.Log.Switchf("省份「%s」没有可用代理，请先执行检测", province)
			return nil, fmt.Errorf("省份「%s」没有可用代理，请先执行检测", province)
		}
		util.Log.Switchf("没有可用代理可切换，请先执行检测")
		return nil, errors.New("没有可用代理可切换，请先执行检测")
	}
	scope := ""
	if province != "" {
		scope = "[" + province + "] "
	}

	// 只试前 10 个最快候选，避免切换过程过长
	if len(cands) > 10 {
		cands = cands[:10]
	}
	for _, p := range cands {
		util.Log.Switchf("%s候选 %s(%vms)，复检中…", scope, p.Label(), p.Latency.Milliseconds())
		if !s.Checker.QuickCheck(p) {
			continue
		}
		if err := s.Gost.Start(p.UpstreamURL()); err != nil {
			util.Log.Errorf("切换失败(%s): %v", p.Label(), err)
			continue
		}
		s.Pool.SetActive(p)
		util.Log.Switchf("已切换 → %s 出口:%s %s%s %vms",
			p.Label(), p.ExitIP, p.Province, p.City, p.Latency.Milliseconds())
		return p, nil
	}
	util.Log.Switchf("全部候选复检失败，保持原状")
	return nil, errors.New("全部候选复检失败，保持原状")
}

// UseProxy 手动使用指定代理（复检通过后启用）。
func (s *Switcher) UseProxy(p *Proxy) error {
	if p == nil {
		return errors.New("代理为空")
	}
	if !s.busy.TryLock() {
		util.Log.Switchf("正在切换中，忽略重复请求")
		return errors.New("正在切换中，请稍候")
	}
	defer s.busy.Unlock()

	util.Log.Switchf("手动选择 %s，复检中…", p.Label())
	if !s.Checker.QuickCheck(p) {
		util.Log.Switchf("%s 复检失败，放弃使用", p.Label())
		return fmt.Errorf("%s 复检失败，放弃使用", p.Label())
	}
	if err := s.Gost.Start(p.UpstreamURL()); err != nil {
		util.Log.Errorf("启用 %s 失败: %v", p.Label(), err)
		return err
	}
	s.Pool.SetActive(p)
	util.Log.Switchf("已启用 → %s 出口:%s %s%s", p.Label(), p.ExitIP, p.Province, p.City)
	return nil
}

// StopProxy 停止转发（gost 停止）。
func (s *Switcher) StopProxy() {
	s.Gost.Stop()
	s.Pool.SetActive(nil)
	util.Log.Switchf("已停止代理转发")
}
