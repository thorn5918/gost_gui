//go:build windows

package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"s5picker/core"
	"s5picker/util"
)

// proxyModel TableView 数据模型（支持表头点击排序）。
type proxyModel struct {
	walk.TableModelBase
	rows      []*core.Proxy
	sortCol   int
	sortOrder walk.SortOrder
	sortPub   walk.EventPublisher
}

func newProxyModel() *proxyModel {
	return &proxyModel{sortCol: -1}
}

func (m *proxyModel) RowCount() int { return len(m.rows) }

func (m *proxyModel) Value(row, col int) any {
	if row < 0 || row >= len(m.rows) {
		return ""
	}
	p := m.rows[row]
	switch col {
	case 0:
		switch p.Status {
		case core.StatusOK:
			return "● 可用"
		case core.StatusFail:
			return "✗ 失败"
		case core.StatusChecking:
			return "◐ 检测中"
		default:
			return "○ 未检测"
		}
	case 1:
		if p.Latency > 0 {
			return p.Latency.Milliseconds()
		}
		return int64(-1)
	case 2:
		if p.ExitIP != "" {
			return p.ExitIP
		}
		return "-"
	case 3:
		return p.Province
	case 4:
		return p.City
	case 5:
		if p.LastCheck.IsZero() {
			return "-"
		}
		return p.LastCheck.Format("15:04:05")
	case 6:
		return p.Raw
	}
	return ""
}

// StyleCell 状态列着色。
func (m *proxyModel) StyleCell(style *walk.CellStyle) {
	if style.Col() != 0 || style.Row() >= len(m.rows) {
		return
	}
	switch m.rows[style.Row()].Status {
	case core.StatusOK:
		style.TextColor = walk.RGB(0, 150, 0)
	case core.StatusFail:
		style.TextColor = walk.RGB(200, 40, 40)
	case core.StatusChecking:
		style.TextColor = walk.RGB(220, 140, 0)
	}
}

// ---------- 排序（点击表头触发） ----------

// ColumnSortable 所有列均可排序。
func (m *proxyModel) ColumnSortable(col int) bool { return col >= 0 && col <= 6 }

// Sort 按列排序并发布变更。
func (m *proxyModel) Sort(col int, order walk.SortOrder) error {
	if col < 0 {
		col = -1
	}
	m.sortCol, m.sortOrder = col, order
	m.applySort()
	m.PublishRowsReset()
	m.sortPub.Publish()
	return nil
}

// SortChanged 排序变化事件。
func (m *proxyModel) SortChanged() *walk.Event { return m.sortPub.Event() }

// SortedColumn 当前排序列。
func (m *proxyModel) SortedColumn() int { return m.sortCol }

// SortOrder 当前排序方向。
func (m *proxyModel) SortOrder() walk.SortOrder { return m.sortOrder }

// applySort 对当前 rows 重新应用排序（刷新数据后保持用户排序）。
func (m *proxyModel) applySort() {
	if m.sortCol < 0 {
		return
	}
	asc := m.sortOrder == walk.SortAscending
	less := m.less(m.sortCol)
	rows := m.rows
	sort.SliceStable(rows, func(i, j int) bool {
		d := less(rows[i], rows[j])
		if asc {
			return d < 0
		}
		// 降序时空值排最后
		if isZero(m.sortCol, rows[i]) {
			return false
		}
		if isZero(m.sortCol, rows[j]) {
			return true
		}
		return d > 0
	})
}

// less 返回该列的"小于"比较函数（升序语义，空值视为最大）。
func (m *proxyModel) less(col int) func(a, b *core.Proxy) float64 {
	switch col {
	case 0: // 状态：可用 < 检测中 < 未检测 < 失败
		rank := func(s core.ProxyStatus) float64 {
			switch s {
			case core.StatusOK:
				return 0
			case core.StatusChecking:
				return 1
			case core.StatusUnknown:
				return 2
			default:
				return 3
			}
		}
		return func(a, b *core.Proxy) float64 { return rank(a.Status) - rank(b.Status) }
	case 1: // 延迟：未测/失败视为无穷大
		return func(a, b *core.Proxy) float64 { return latVal(a) - latVal(b) }
	case 5: // 最近检测
		return func(a, b *core.Proxy) float64 {
			if a.LastCheck.IsZero() && b.LastCheck.IsZero() {
				return 0
			}
			if a.LastCheck.IsZero() {
				return 1
			}
			if b.LastCheck.IsZero() {
				return -1
			}
			return float64(a.LastCheck.Unix() - b.LastCheck.Unix())
		}
	default: // 2 出口IP / 3 省 / 4 市 / 6 上游地址
		get := func(p *core.Proxy) string {
			switch col {
			case 2:
				return p.ExitIP
			case 3:
				return p.Province
			case 4:
				return p.City
			default:
				return p.Raw
			}
		}
		return func(a, b *core.Proxy) float64 {
			sa, sb := get(a), get(b)
			if sa == "" && sb == "" {
				return 0
			}
			if sa == "" {
				return 1
			}
			if sb == "" {
				return -1
			}
			if sa < sb {
				return -1
			}
			if sa > sb {
				return 1
			}
			return 0
		}
	}
}

func latVal(p *core.Proxy) float64 {
	if p.Latency <= 0 {
		return 1e12
	}
	return float64(p.Latency)
}

// isZero 判断该列是否为"空值"（降序时排最后）。
func isZero(col int, p *core.Proxy) bool {
	switch col {
	case 0:
		return false
	case 1:
		return p.Latency <= 0
	case 2:
		return p.ExitIP == ""
	case 3:
		return p.Province == ""
	case 4:
		return p.City == ""
	case 5:
		return p.LastCheck.IsZero()
	default:
		return p.Raw == ""
	}
}

// win 封装窗口与控件引用。
type win struct {
	app   *core.App
	mw    *walk.MainWindow
	model *proxyModel

	lblIP    *walk.Label
	lblLoc   *walk.Label
	lblLocal *walk.Label
	lblStat  *walk.Label
	combo    *walk.ComboBox
	chkFail  *walk.CheckBox
	tv       *walk.TableView
	logBox   *walk.TextEdit

	provFilter  string
	includeFail bool
	provsKey    string
}

// Run 启动 GUI 主循环（阻塞）。
func Run(version string) {
	exeDir := util.ExeDir()
	app, err := core.NewApp(exeDir)
	if err != nil {
		walk.MsgBox(nil, "S5Picker", "初始化失败: "+err.Error(), walk.MsgBoxIconError)
		return
	}

	w := &win{app: app}
	w.model = newProxyModel()

	err = MainWindow{
		AssignTo: &w.mw,
		Title:    "S5Picker - SOCKS5 代理检测与切换 " + version,
		MinSize:  Size{Width: 900, Height: 540},
		Size:     Size{Width: 980, Height: 620},
		Layout: HBox{MarginsZero: true},
		Children: []Widget{HSplitter{Children: []Widget{
			Composite{
				Layout: VBox{Margins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 8}, Spacing: 8},
				Children: []Widget{
					GroupBox{
						Title:  "代理信息",
						Layout: Grid{Columns: 2, Spacing: 8},
						Children: []Widget{
							Label{Text: "代理IP:", TextColor: walk.RGB(120, 120, 120)},
							Label{AssignTo: &w.lblIP, Text: "未启用", Font: Font{PointSize: 10, Bold: true}},
							Label{Text: "位置:", TextColor: walk.RGB(120, 120, 120)},
							Label{AssignTo: &w.lblLoc, Text: "-"},
							Label{Text: "本地代理:", TextColor: walk.RGB(120, 120, 120)},
							Label{AssignTo: &w.lblLocal, Text: "未启动"},
							Label{Text: "统计:", TextColor: walk.RGB(120, 120, 120)},
							Label{AssignTo: &w.lblStat, Text: "-"},
						},
					},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 8},
						Children: []Widget{
							PushButton{Text: "⚡ 一键切换（最快）", MinSize: Size{Width: 150, Height: 34}, OnClicked: w.onSwitch},
							PushButton{Text: "批量检测", MinSize: Size{Width: 100, Height: 34}, OnClicked: w.onCheckAll},
							PushButton{Text: "停止代理", MinSize: Size{Width: 100, Height: 34}, OnClicked: w.onStop},
							HSpacer{},
						},
					},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							Label{Text: "省份筛选:"},
							ComboBox{AssignTo: &w.combo, Model: []string{"全部省份"}, Value: "全部省份", MinSize: Size{Width: 140, Height: 0}, OnCurrentIndexChanged: w.onProvChanged},
							CheckBox{AssignTo: &w.chkFail, Text: "显示失败", OnClicked: w.onFailToggled},
							HSpacer{},
						},
					},
					TableView{
						AssignTo: &w.tv,
						Model:    w.model,
						Columns: []TableViewColumn{
							{Title: "状态", Width: 80},
							{Title: "延迟ms", Width: 70},
							{Title: "出口IP", Width: 130},
							{Title: "省", Width: 90},
							{Title: "市", Width: 110},
							{Title: "最近检测", Width: 90},
							{Title: "上游地址", Width: 190},
						},
						OnItemActivated: w.onUseSelected,
						ContextMenuItems: []MenuItem{
							Action{Text: "使用该代理", OnTriggered: w.onUseSelected},
							Action{Text: "复制代理地址", OnTriggered: w.onCopyAddr},
							Action{Text: "复制出口IP", OnTriggered: w.onCopyIP},
						},
					},
				},
			},
					GroupBox{
						Title:  "日志",
						Layout: VBox{MarginsZero: false},
						Children: []Widget{
							TextEdit{AssignTo: &w.logBox, ReadOnly: true, VScroll: true},
						},
					},
				}}},
		MenuItems: []MenuItem{
			Menu{
				Text: "文件(&F)",
				Items: []MenuItem{
					Action{Text: "重新加载列表", OnTriggered: w.onReload},
					Action{Text: "导出可用列表", OnTriggered: w.onExport},
					Separator{},
					Action{Text: "退出", OnTriggered: func() { w.mw.Close() }},
				},
			},
			Menu{
				Text: "帮助(&H)",
				Items: []MenuItem{
					Action{Text: "关于", OnTriggered: func() {
						walk.MsgBox(w.mw, "关于",
							fmt.Sprintf("S5Picker %s\n\nSOCKS5 代理池检测 / 地区筛选 / 一键切换\n"+
								"本地转发内核: gost\n\n使用方法:\n"+
								"1. 将 proxies.txt 放到程序同目录（每行一条 socks5://ip:port）\n"+
								"2. 批量检测后，双击列表行使用该代理\n"+
								"3. 浏览器/程序挂本地代理即可", version), walk.MsgBoxIconInformation)
					}},
				},
			},
		},
	}.Create()

	if err != nil {
		walk.MsgBox(nil, "S5Picker", "创建窗口失败: "+err.Error(), walk.MsgBoxIconError)
		return
	}

	// 池变化 → UI 刷新
	app.Pool.Notify = func() { w.mw.Synchronize(w.refresh) }

	// 关闭时记录日志
	w.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		util.Log.Infof("程序退出")
	})

	// 日志实时输出
	w.startLogPump()

	// 初始刷新
	w.mw.Synchronize(w.refresh)

	// gost 崩溃自动重启由 core 内部 goroutine 负责

	w.mw.Run()
	app.Shutdown()
}

func (w *win) startLogPump() {
	ch := util.Log.Subscribe()
	go func() {
		for e := range ch {
			e := e
			if w.mw == nil {
				continue
			}
			w.mw.Synchronize(func() {
				if w.logBox == nil {
					return
				}
				w.logBox.AppendText(e.Time.Format("[15:04:05] ") + e.Text + "\r\n")
				// 防止日志无限增长
				if w.logBox.TextLength() > 1<<20 {
					full := w.logBox.Text()
					lines := strings.Split(full, "\r\n")
					if len(lines) > 2000 {
						w.logBox.SetText(strings.Join(lines[len(lines)-2000:], "\r\n"))
					}
				}
			})
		}
	}()
}

// refresh 全量刷新界面（必须在 UI 线程调用）。
func (w *win) refresh() {
	// 省份下拉
	provs := w.app.Pool.Provinces()
	key := strings.Join(provs, "|")
	if key != w.provsKey {
		w.provsKey = key
		items := append([]string{"全部省份"}, provs...)
		if w.combo != nil {
			w.combo.SetModel(items)
		}
	}

	// 表格：筛选 + 排序（首次出结果时默认按延迟升序，最快在前）
	w.model.rows = w.app.Pool.Filter(w.provFilter, w.includeFail)
	if w.model.sortCol < 0 {
		for _, p := range w.model.rows {
			if p.Latency > 0 {
				w.model.sortCol, w.model.sortOrder = 1, walk.SortAscending
				break
			}
		}
	}
	w.model.applySort()
	w.model.PublishRowsReset()

	// 主卡片
	active := w.app.Pool.Active()
	if active != nil {
		if active.ExitIP != "" {
			w.lblIP.SetText(active.ExitIP)
		} else {
			w.lblIP.SetText(active.Label())
		}
		loc := active.Province + active.City
		if strings.TrimSpace(loc) == "" {
			loc = "未知"
		}
		w.lblLoc.SetText(loc)
	} else {
		w.lblIP.SetText("未启用")
		w.lblLoc.SetText("-")
	}
	if w.app.Gost.Running() {
		w.lblLocal.SetText(w.app.Gost.SocksAddr() + "(socks5) / " + w.app.Gost.HTTPAddr() + "(http)")
	} else {
		w.lblLocal.SetText("未启动")
	}
	total, okCnt := w.app.Pool.Count()
	if active != nil {
		w.lblStat.SetText(fmt.Sprintf("可用 %d/%d · 当前 %s", okCnt, total, active.Label()))
	} else {
		w.lblStat.SetText(fmt.Sprintf("可用 %d/%d · 未启用转发", okCnt, total))
	}
}

// ---------- 按钮事件 ----------

func (w *win) onSwitch() {
	go w.app.Sw.SwitchBest("")
}

func (w *win) onCheckAll() {
	total, _ := w.app.Pool.Count()
	if total == 0 {
		util.Log.Errorf("代理列表为空，请检查 proxies.txt 后在菜单-文件-重新加载列表")
		return
	}
	util.Log.Checkf("开始批量检测 %d 条…", total)
	go w.app.Checker.CheckAll()
}

func (w *win) onStop() {
	go w.app.Sw.StopProxy()
}

func (w *win) onProvChanged() {
	if w.combo == nil {
		return
	}
	t := w.combo.Text()
	if t == "全部省份" || t == "" {
		w.provFilter = ""
	} else {
		w.provFilter = t
	}
	w.refresh()
}

func (w *win) onFailToggled() {
	w.includeFail = w.chkFail.Checked()
	w.refresh()
}

func (w *win) onUseSelected() {
	idx := w.tv.CurrentIndex()
	if idx < 0 || idx >= len(w.model.rows) {
		return
	}
	p := w.model.rows[idx]
	go w.app.Sw.UseProxy(p)
}

func (w *win) onCopyAddr() {
	if p := w.selected(); p != nil {
		walk.Clipboard().SetText(p.Raw)
	}
}

func (w *win) onCopyIP() {
	if p := w.selected(); p != nil && p.ExitIP != "" {
		walk.Clipboard().SetText(p.ExitIP)
	}
}

func (w *win) selected() *core.Proxy {
	idx := w.tv.CurrentIndex()
	if idx < 0 || idx >= len(w.model.rows) {
		return nil
	}
	return w.model.rows[idx]
}

func (w *win) onReload() {
	go w.app.ReloadProxies()
}

func (w *win) onExport() {
	var lines []string
	for _, p := range w.app.Pool.All() {
		if p.Status == core.StatusOK {
			lines = append(lines, p.Raw)
		}
	}
	if len(lines) == 0 {
		util.Log.Errorf("没有可用的代理可导出")
		return
	}
	path := filepath.Join(util.ExeDir(), "proxies_ok.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		util.Log.Errorf("导出失败: %v", err)
		return
	}
	util.Log.Infof("已导出 %d 条可用代理到 %s", len(lines), path)
	walk.MsgBox(w.mw, "导出完成",
		fmt.Sprintf("已导出 %d 条可用代理到:\n%s", len(lines), path), walk.MsgBoxIconInformation)
}
