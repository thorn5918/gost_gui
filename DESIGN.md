# S5Picker 详细设计文档

一个 Windows 桌面小工具：SOCKS5 代理池的批量检测、地区筛选、手动/一键切换，内置本地转发端口。
参考形态：JackProxy（主卡片 + 列表 + 日志）。硬性要求：体积尽量小、不用 web 界面。

---

## 1. 技术选型

| 项 | 选择 | 理由 |
|---|---|---|
| 语言 | Go 1.22+ | 生态成熟、并发模型适合代理转发 |
| GUI | **lxn/walk**（Win32 原生控件） | 仅 Windows 场景下体积最小、原生观感、自带虚拟化 TableView（万行列表无压力） |
| 网络（检测） | golang.org/x/net/proxy | socks5 拨号（含 user:pass 鉴权），标准库 http 完成其余 |
| 转发引擎 | **gost.exe 子进程托管（内嵌释放）** | 协议质量 100% 由 gost 保证；go:embed 内嵌 upx 压缩后的 gost.exe，首启释放到 %LOCALAPPDATA% |
| 构建优化 | `-ldflags "-s -w -H windowsgui"` + upx | 去符号、去控制台窗口、压缩 |

备选：Gio（若日后要跨平台）。Fyne 因 CGO + 体积大，不选。
自研协议代码为 0：转发全部交给 gost，检测只是普通 HTTP 请求。

体积预算：主程序（含内嵌 gost.exe）单文件 ~15–18MB。
注意：upx + 内嵌 exe 有杀软误报可能；退路是"主程序 + 外置 gost.exe"双文件形态（构建开关切换）。

---

## 2. 总体架构

单二进制，UI 与引擎解耦，二者只通过内存对象 + 事件通道交互。

```
┌──────────────────────────────────────────────┐
│  ui/            walk 界面（主线程）           │
│   ├ mainwindow.go   主卡片 + 表格 + 日志面板   │
│   └ model.go        TableView 数据适配        │
│          ▲ 事件通道 chan Event  │ 命令调用     │
│  core/           后台 goroutine               │
│   ├ pool.go        代理条目与状态              │
│   ├ checker.go     并发检测（含地区查询）       │
│   ├ gostproc.go    gost.exe 子进程托管（内嵌释放/重启）│
│   └ switcher.go    一键切换策略                 │
│  util/                                                      │
│   ├ cz88.go        IP 地区接口适配             │
│   └ log.go         日志（环形缓冲 + 文件）      │
└──────────────────────────────────────────────┘
```

---

## 3. 数据模型

```go
type ProxyStatus int
const (StatusUnknown, StatusChecking, StatusOK, StatusFail)

type Proxy struct {
    Raw       string      // socks5://1.2.3.4:5678（支持 user:pass@）
    Host      string
    Port      int
    Status    ProxyStatus
    Latency   time.Duration // 0 表示未测/失败
    ExitIP    string
    Province  string        // 省
    City      string        // 市
    LastCheck time.Time
    failCount int           // 连续失败次数，用于降权
}

type ProxyPool struct {
    mu     sync.RWMutex
    items  []*Proxy
    index  map[string]*Proxy // Raw 去重
    active *Proxy            // 当前转发使用的代理
}
```

---

## 4. 核心流程

### 4.1 加载（core/pool.go）
- 启动时（及手动刷新）读取 exe 同目录 `proxies.txt`
- 每行一条，`strings.TrimSpace`，跳过空行/`#` 注释，`url.Parse` 校验 scheme
- 与已有池合并去重：新出现的入池，消失的移除；已测过的状态保留

### 4.2 批量检测（core/checker.go）
一次请求同时得到四项结果：连通性、延迟、出口 IP、省市。

- 并发 worker 池，默认 32（可在设置改），避免过大触发风控
- 单个检测步骤：
  1. 通过代理拨号到 `cz88.net:443`（x/net/proxy 的 socks5 dialer，支持 URL 中带 user:pass）
  2. `GET https://cz88.net/api/cz88/ip/base?ip=`（ip 留空 = 查出口自身）
  3. 延迟 = 从开始到收到完整响应的总耗时；HTTP 状态非 200 或超时（默认 5s）判失败
  4. 成功则解析 JSON → ExitIP / Province / City
- cz88 响应做适配器封装（util/cz88.go），字段映射集中一处；若接口结构变化只改一个文件
- 结果写入 Proxy 后发事件给 UI
- cz88 若限流：worker 内失败重试 1 次；仍失败则退化为「TCP 连通 + 延迟」，省市标记"未知"

### 4.3 本地转发（core/gostproc.go）
- 首次启动：释放内嵌 gost.exe 到 `%LOCALAPPDATA%\S5Picker\bin\gost.exe`（按版本哈希校验，缺失/不符则重写）
- 启动子进程：
  `gost.exe -L "socks5://:55558" -L "http://:55559" -F "socks5://上游IP:端口"`
- 双协议双端口入站：SOCKS5 `127.0.0.1:55558`、HTTP `127.0.0.1:55559`（端口可配置，占用时自动 +1）
- 未激活上游时 `-F` 省略 = gost 直连出口（受 direct_when_inactive 开关控制）
- 切换代理 = 换 `-F` 参数重启子进程（毫秒级，旧连接断开）；主程序退出时连带杀掉子进程
- gost.exe 崩溃/退出时自动拉起，并写日志

### 4.4 一键切换（core/switcher.go）
- 从「状态 OK 且已填省市」的条目中选延迟最低者
- 选择前对目标再做一次快速复检（超时 3s），失败则顺延次优
- 切换后：更新主卡片，日志记录新旧出口 IP

### 4.5 日志（util/log.go）
- 环形缓冲 1000 条 + 追加写 exe 同目录 `s5picker.log`
- 事件通道推给 UI；格式：`[15:04:05] 文本`
- 分类前缀：`[检测] [切换] [转发] [错误]`

---

## 5. 界面设计（walk）

主窗口 ~860×560，水平 Splitter：

**左栏**
1. 主卡片（仿 JackProxy）：
   - 代理IP（出口）、位置（省 市）、本地代理地址、状态徽标（活跃/未启用）
   - 「一键切换」红色主按钮 + 「检测全部」次按钮
2. 筛选行：省份下拉框（全部 + 已发现省份列表）、显示失败开关
3. TableView 列：状态 ● / 延迟 ms / 出口 IP / 省 / 市 / 最近检测 / 操作【使用】
   - 虚拟模式，行数大也不卡；【使用】点击后该行置为 active 并复检

**右栏**：日志 TextBox，只读，自动滚动到底部，右键可复制

菜单：文件(重新加载 txt / 导出可用列表) · 设置(并发数/超时/端口/直连开关) · 帮助

UI 线程模型：后台 goroutine 全部通过 `chan Event` 上报，walk 主循环每 100ms 取事件刷新（或 win.PostMessage），绝不跨线程直接改控件。

---

## 6. 配置

`config.json`（与 exe 同目录，不存在则用默认值）：
```json
{
  "socks5_port": 55558,
  "http_port": 55559,
  "checker_concurrency": 32,
  "check_timeout_sec": 5,
  "direct_when_inactive": false
}
```

---

## 7. 体积与打包

1. `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui"`
2. gost.exe 先 upx --best --lzma 压缩，再经 go:embed 内嵌进主程序
3. 主程序本体再 upx（可选，注意杀软误报时的退路：跳过 upx 或改双文件发布）
4. 预期最终单文件 ~15–18MB

---

## 8. 里程碑

- M1 core 引擎（pool/checker/gostproc + cz88 适配）+ CLI 自测命令
- M2 walk UI 主卡片 + 表格 + 日志，打通事件通道
- M3 一键切换、省份筛选、设置与配置持久化
- M4 打磨：图标/manifest/DPI、upx 产物、边界情况（端口占用、txt 畸形行）

## 9. 实现结果（M1-M4 完成记录）

- 转发引擎按最终定案落地：官方 gost v3.3.3 二进制 gzip 内嵌（17MB），首启解压释放到
  `%LOCALAPPDATA%\S5Picker\bin\gost.exe`，子进程托管，崩溃自动拉起，切换上游即时重启
- cz88 实测响应为 `{"code":200,"data":{"ip":...,"province":"浙江","city":"杭州",...}}`，
  适配器按宽松递归匹配实现，字段变化无需改架构
- e2e 已验证（本机 linux，走内嵌 gost）：直连链路 126ms、两跳链式 272ms、
  HTTP 入站口均正常拿到出口 IP 与省市
- Windows 交叉编译通过（walk 无 CGO），单文件 exe 约 28MB（内含 17MB gz 内核）
- GUI：walk declarative 实现，主卡片 + 省份筛选 + TableView（状态列着色）+ 右侧日志
- 构建产物（含 gost 二进制）不入库；CI（GitHub Actions）构建时下载 gost 后压缩内嵌，
  push main 出 artifact，推 `v*` 标签自动发布 Release

## 10. 风险与对策

| 风险 | 对策 |
|---|---|
| cz88 接口变更/限流 | 适配器集中封装 + 失败退化（只测连通）；预留备选 IP 接口插槽 |
| walk 库维护不活跃 | 功能面稳定，锁 Go 1.22 编译；有问题可平移 Gio |
| 代理含认证 | 解析 URL user:pass 传给 socks5 dialer |
| 大列表检测拖慢 UI | 全部在后台 worker，UI 只收事件 |
