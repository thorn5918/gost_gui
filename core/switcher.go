package core

import (
	"s5picker/util"
)
// Switcher 一键切换：选最快的可用代理，用前复检，然后重启 gost。
type Switcher struct {
	Pool    *Pool
	Checker *Checker
	Gost    *GostProc
}

// SwitchBest 执行切换。返回最终使用的代理；失败返回 nil。
// upstream 为空字符串时表示取消代理（直连，受配置开关控制）。
func (s *Switcher) SwitchBest() *Proxy {
	cands := s.Pool.Candidates()
	if len(cands) == 0 {
		util.Log.Switchf("没有可用代理可切换，请先执行检测")
		return nil
	}

	// 只试前 10 个最快候选，避免切换过程过长
	if len(cands) > 10 {
		cands = cands[:10]
	}
	for _, p := range cands {
		util.Log.Switchf("候选 %s(%vms)，复检中…", p.Label(), p.Latency.Milliseconds())
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
		return p
	}
	util.Log.Switchf("全部候选复检失败，保持原状")
	return nil
}

// StopProxy 停止转发（gost 停止）。
func (s *Switcher) StopProxy() {
	s.Gost.Stop()
	s.Pool.SetActive(nil)
	util.Log.Switchf("已停止代理转发")
}
