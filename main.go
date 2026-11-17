package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"

	"s5picker/core"
	"s5picker/ui"
	"s5picker/util"
)

var version = "v1.0.0"

func main() {
	var (
		test  = flag.Bool("test", false, "命令行自测：检测全部代理并打印结果后退出")
		list  = flag.String("list", "", "自测使用的代理列表文件（默认为程序目录 proxies.txt）")
		proxy = flag.String("proxy", "", "自测单个代理，如 socks5://1.2.3.4:1080")
	)
	flag.Parse()

	if *test || *proxy != "" {
		runSelfTest(*list, *proxy)
		return
	}

	if runtime.GOOS != "windows" {
		fmt.Println("图形界面仅支持 Windows；Linux/macOS 可使用 -test 自测模式。")
		os.Exit(0)
	}
	ui.Run(version)
}

// runSelfTest CLI 自测（M1 验证入口）。
func runSelfTest(listFile, single string) {
	exeDir := util.ExeDir()
	app, err := core.NewApp(exeDir)
	if err != nil {
		fmt.Println("初始化失败:", err)
		os.Exit(1)
	}
	if single != "" {
		p := &core.Proxy{Raw: single}
		if host, port, ok := splitHostPort(single); ok {
			p.Host, p.Port = host, port
		}
		r := util.QueryViaProxy(single, app.Checker.Timeout)
		fmt.Printf("代理 %s\n  连通性: %v\n  延迟:   %v\n  出口IP: %s\n  地区:   %s %s\n",
			single, r.OK, r.Latency, r.ExitIP, r.Province, r.City)
		if !r.OK {
			os.Exit(2)
		}
		return
	}

	if listFile != "" {
		if _, _, err := app.Pool.LoadFile(listFile); err != nil {
			fmt.Println("读取列表失败:", err)
			os.Exit(1)
		}
	}
	items := app.Pool.All()
	if len(items) == 0 {
		fmt.Println("代理列表为空")
		os.Exit(1)
	}
	fmt.Printf("开始检测 %d 条代理（并发 %d，超时 %ds）…\n\n",
		len(items), app.Checker.Concurrency, app.Checker.Timeout)
	app.Checker.CheckAllSync()

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "状态\t延迟\t出口IP\t省\t市\t代理")
	for _, p := range items {
		lat := "-"
		if p.Latency > 0 {
			lat = fmt.Sprintf("%vms", p.Latency.Milliseconds())
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			p.Status.String(), lat, p.ExitIP, p.Province, p.City, p.Label())
	}
	w.Flush()
}

func splitHostPort(raw string) (string, string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	host, port, _ := strings.Cut(u.Host, ":")
	return host, port, port != ""
}
