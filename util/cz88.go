package util

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// GeoResult 单次代理检测结果。
type GeoResult struct {
	OK       bool          // 连通性
	Latency  time.Duration // 从发起请求到收到响应的总耗时
	ExitIP   string
	Province string
	City     string
	Err      error
}

// QueryViaProxy 通过指定 socks5 代理查询出口信息。
// 首选 cz88（同时得到出口 IP / 省 / 市）；cz88 失败时退化为
// 访问 baidu 测连通性与延迟（地区标为未知）。
func QueryViaProxy(proxyURL string, timeout time.Duration) GeoResult {
	res := GeoResult{Err: errors.New("未检测")}

	dialer, err := socksDialer(proxyURL)
	if err != nil {
		res.Err = fmt.Errorf("代理地址解析失败: %w", err)
		return res
	}

	// 1) 首选 cz88：一次请求拿到连通性 + 延迟 + 出口IP + 省市
	start := time.Now()
	body, err := httpGetViaDialer(context.Background(), dialer, "https://cz88.net/api/cz88/ip/base?ip=", timeout)
	res.Latency = time.Since(start)
	if err == nil {
		ip, prov, city := parseCZ88(body)
		res.OK = true
		res.Err = nil
		res.ExitIP = ip
		res.Province = prov
		res.City = city
		return res
	}

	// 2) 退化：访问 baidu 仅验证连通性 + 延迟
	start = time.Now()
	_, err2 := httpGetViaDialer(context.Background(), dialer, "https://www.baidu.com/", timeout)
	if err2 == nil {
		res.OK = true
		res.Err = fmt.Errorf("地区查询失败(%v)", err)
	} else {
		res.Err = fmt.Errorf("无法通过代理访问外网: %v", err2)
	}
	return res
}

// socksDialer 根据 socks5 URL 构造拨号器。
func socksDialer(proxyURL string) (proxy.Dialer, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks":
	default:
		return nil, fmt.Errorf("不支持协议: %s", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("缺少主机地址")
	}
	return proxy.FromURL(u, proxy.Direct)
}

func httpGetViaDialer(ctx context.Context, dialer proxy.Dialer, rawURL string, timeout time.Duration) ([]byte, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if cd, ok := dialer.(proxy.ContextDialer); ok {
				return cd.DialContext(ctx, network, addr)
			}
			return dialer.Dial(network, addr)
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var c net.Conn
			var err error
			if cd, ok := dialer.(proxy.ContextDialer); ok {
				c, err = cd.DialContext(ctx, "tcp", addr)
			} else {
				c, err = dialer.Dial("tcp", addr)
			}
			if err != nil {
				return nil, err
			}
			h, _, _ := net.SplitHostPort(addr)
			tc := tls.Client(c, &tls.Config{ServerName: h})
			if err := tc.HandshakeContext(ctx); err != nil {
				c.Close()
				return nil, err
			}
			return tc, nil
		},
		DisableKeepAlives: true,
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) s5picker/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 1024)
	for {
		n, e := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if e != nil {
			break
		}
		if len(buf) > 64*1024 {
			break
		}
	}
	return buf, nil
}

// parseCZ88 解析 cz88 响应。响应结构未知细节，采用宽松的
// 递归字段匹配：在任意层级寻找 province/prov、city、ip 字段。
func parseCZ88(body []byte) (ip, province, city string) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return
	}
	findGeo(v, &ip, &province, &city)
	return
}

func findGeo(v any, ip, province, city *string) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, val := range m {
		s, isStr := val.(string)
		if !isStr || s == "" {
			continue
		}
		kl := strings.ToLower(k)
		switch {
		case *province == "" && (kl == "province" || kl == "prov"):
			*province = s
		case *city == "" && kl == "city":
			*city = s
		case *ip == "" && kl == "ip":
			*ip = s
		}
	}
	// 未找齐则继续下钻
	if *province == "" || *city == "" || *ip == "" {
		for _, val := range m {
			if sub, ok := val.(map[string]any); ok {
				findGeo(sub, ip, province, city)
			}
		}
	}
}
