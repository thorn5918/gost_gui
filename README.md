# S5Picker

SOCKS5 代理池的批量检测、地区筛选与一键切换工具（Windows 原生界面）。

![界面说明](https://img.shields.io/badge/platform-Windows-blue)

## 功能

- **批量检测**：从程序同目录 `proxies.txt`（每行一条 `socks5://ip:port`）读取代理列表，
  并发检测连通性与延迟，同时通过代理请求 [cz88](https://cz88.net) IP 接口，
  一次拿到 **出口 IP / 省 / 市**
- **地区筛选**：按省份筛选列表，勾选"显示失败"查看全部
- **手动使用**：双击列表行（或右键菜单）使用该代理，使用前自动复检
- **一键切换**：取延迟最快的可用代理，复检通过后立即切换
- **本地转发**：由内嵌的官方 [gost](https://github.com/go-gost/gost) 内核提供
  - `socks5://127.0.0.1:55558`
  - `http://127.0.0.1:55559`
- **日志**：右侧实时日志 + 同目录 `s5picker.log`

## 使用

1. 下载 `s5picker.exe`，和 `proxies.txt` 放同一目录
2. 启动 → 点"批量检测"
3. 按省份筛选 → 双击某行使用；或点"一键切换（最快）"
4. 浏览器/程序把代理指到 `127.0.0.1:55558`（socks5）或 `:55559`（http）

命令行自测（无界面）：`s5picker.exe -test`、`s5picker.exe -proxy socks5://1.2.3.4:1080`

## 配置（config.json，可改）

```json
{
  "socks5_port": 55558,
  "http_port": 55559,
  "checker_concurrency": 32,
  "check_timeout_sec": 5,
  "direct_when_inactive": false
}
```

## 从源码构建

```bash
./build.sh          # Windows exe -> build/s5picker.exe
./build.sh linux    # Linux 测试版
```

首次构建自动下载 gost v3.3.3 内核（github，可用 gh-proxy 加速）gzip 内嵌。
推送 `v*` 标签会触发 GitHub Actions 自动构建并发布 exe。

## 技术

- Go + [lxn/walk](https://github.com/lxn/walk) 原生 Win32 界面
- 转发内核为 gost 官方二进制（子进程托管，崩溃自动拉起，切换上游即时重启）
- 协议代码 0 自研，转发质量由 gost 保证
