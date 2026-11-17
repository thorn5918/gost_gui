package core

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type ProxyStatus int

const (
	StatusUnknown ProxyStatus = iota
	StatusChecking
	StatusOK
	StatusFail
)

func (s ProxyStatus) String() string {
	switch s {
	case StatusChecking:
		return "检测中"
	case StatusOK:
		return "可用"
	case StatusFail:
		return "失败"
	default:
		return "未检测"
	}
}

// Proxy 一条 socks5 代理的全部状态。
type Proxy struct {
	Raw       string
	Host      string
	Port      string
	Status    ProxyStatus
	Latency   time.Duration
	ExitIP    string
	Province  string
	City      string
	LastCheck time.Time
	FailCount int
	checking  bool
}

// Label 形如 1.2.3.4:5678
func (p *Proxy) Label() string { return p.Host + ":" + p.Port }

// UpstreamURL 返回 gost -F 需要的上游地址。
func (p *Proxy) UpstreamURL() string { return p.Raw }

// Pool 代理池。
type Pool struct {
	mu     sync.RWMutex
	items  []*Proxy
	index  map[string]*Proxy
	active *Proxy

	// Notify 池发生变化时的回调（UI 刷新），可随时被替换。
	Notify func()
}

func NewPool() *Pool {
	return &Pool{index: map[string]*Proxy{}}
}

func (p *Pool) changed() {
	if p.Notify != nil {
		p.Notify()
	}
}

// LoadFile 从 txt 加载代理（每行一条），与现有池合并：
// 新出现的追加，消失的移除，已有状态保留。返回新增数量。
func (p *Pool) LoadFile(path string) (int, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()

	var (
		raws  []string
		badLn []string
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Host == "" ||
			(!strings.EqualFold(u.Scheme, "socks5") && !strings.EqualFold(u.Scheme, "socks")) {
			badLn = append(badLn, line)
			continue
		}
		host, port, _ := strings.Cut(u.Host, ":")
		if host == "" || port == "" {
			badLn = append(badLn, line)
			continue
		}
		raws = append(raws, line)
	}
	if err := sc.Err(); err != nil {
		return 0, badLn, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	want := make(map[string]struct{}, len(raws))
	added := 0
	var keep []*Proxy
	for _, raw := range raws {
		want[raw] = struct{}{}
		if _, ok := p.index[raw]; ok {
			continue
		}
		host, port, _ := strings.Cut(mustHost(raw), ":")
		np := &Proxy{Raw: raw, Host: host, Port: port}
		p.index[raw] = np
		keep = append(keep, np)
		added++
	}
	// 保留原有顺序 + 追加新增
	for _, old := range p.items {
		if _, ok := want[old.Raw]; ok {
			keep = append(keep, old)
		} else if p.active == old {
			p.active = nil
		}
	}
	p.items = keep
	p.changed()
	return added, badLn, nil
}

func mustHost(raw string) string {
	u, _ := url.Parse(raw)
	if u != nil {
		return u.Host
	}
	return raw
}

// All 返回快照。
func (p *Pool) All() []*Proxy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Proxy, len(p.items))
	copy(out, p.items)
	return out
}

// Count 按状态统计。
func (p *Pool) Count() (total, okCnt int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	total = len(p.items)
	for _, it := range p.items {
		if it.Status == StatusOK {
			okCnt++
		}
	}
	return
}

// Provinces 返回已发现省份（排序去重）。
func (p *Pool) Provinces() []string {
	p.mu.RLock()
	set := map[string]struct{}{}
	for _, it := range p.items {
		if it.Province != "" {
			set[it.Province] = struct{}{}
		}
	}
	p.mu.RUnlock()
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Filter 按省份和是否包含失败行筛选快照。
func (p *Pool) Filter(province string, includeFail bool) []*Proxy {
	all := p.All()
	out := all[:0:0]
	for _, it := range all {
		if province != "" && it.Province != province {
			continue
		}
		if !includeFail && it.Status == StatusFail {
			continue
		}
		out = append(out, it)
	}
	return out
}

func (p *Pool) Get(raw string) *Proxy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.index[raw]
}

// Active 当前转发所用代理。
func (p *Pool) Active() *Proxy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.active
}

// SetActive 设置当前代理并通知。
func (p *Pool) SetActive(np *Proxy) {
	p.mu.Lock()
	p.active = np
	p.mu.Unlock()
	p.changed()
}

// Update 写入单条检测结果。
func (p *Pool) Update(raw string, st ProxyStatus, latency time.Duration, ip, prov, city string) {
	p.mu.Lock()
	it, ok := p.index[raw]
	if ok {
		it.Status = st
		it.Latency = latency
		it.ExitIP = ip
		it.Province = prov
		it.City = city
		it.LastCheck = time.Now()
		if st == StatusOK {
			it.FailCount = 0
		} else if st == StatusFail {
			it.FailCount++
		}
	}
	p.mu.Unlock()
	if ok {
		p.changed()
	}
}

// SetChecking 标记检测中，避免重复下发。
func (p *Pool) SetChecking(raw string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	it, ok := p.index[raw]
	if !ok || it.checking {
		return false
	}
	it.checking = true
	it.Status = StatusChecking
	return true
}

// UnsetChecking 检测结束清除标记。
func (p *Pool) UnsetChecking(raw string) {
	p.mu.Lock()
	if it, ok := p.index[raw]; ok {
		it.checking = false
	}
	p.mu.Unlock()
}

// Candidates 返回可用（OK）代理，按延迟升序。
func (p *Pool) Candidates() []*Proxy {
	all := p.All()
	out := make([]*Proxy, 0, len(all))
	for _, it := range all {
		if it.Status == StatusOK {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Latency < out[j].Latency })
	return out
}

func (p *Proxy) String() string { return fmt.Sprintf("%s(%s)", p.Label(), p.Status) }
