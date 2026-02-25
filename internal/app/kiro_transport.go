package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"ccLoad/internal/config"

	utls "github.com/refraction-networking/utls"
)

// ============================================================================
// Kiro 专用 HTTP Transport（utls TLS 指纹伪装）
// 仅用于 Kiro 预设渠道，其他渠道继续使用标准 Transport
// 参考: https://codeberg.org/HenryXiaoYang/kirocli2api
// ============================================================================

// buildKiroHTTPTransport 构建 Kiro 专用 Transport（utls 指纹伪装）
// 使用 utls.HelloChrome_Auto 预设模拟 Chrome TLS 指纹，强制 HTTP/1.1
// utls 不完整支持 HTTP/2，通过移除 ALPN 中的 h2 实现降级
func buildKiroHTTPTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   config.HTTPDialTimeout,
		KeepAlive: config.HTTPKeepAliveInterval,
	}

	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        config.HTTPMaxIdleConns,
		MaxIdleConnsPerHost: config.HTTPMaxIdleConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		MaxConnsPerHost:     config.HTTPMaxConnsPerHost,
		DisableCompression:  false,
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   false, // 禁用 HTTP/2，utls 不完整支持
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		// 使用自定义 DialTLS 替代标准 TLS 握手
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialTLSWithUTLS(ctx, dialer, network, addr)
		},
	}

	return transport
}

// dialTLSWithUTLS 使用 utls 进行 TLS 握手，模拟 Chrome 浏览器指纹
func dialTLSWithUTLS(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	// 检查是否配置了代理
	proxyURL := getProxyURL(addr)
	var rawConn net.Conn
	var err error

	if proxyURL != nil {
		// 通过代理建立连接
		rawConn, err = dialThroughProxy(ctx, dialer, proxyURL, addr)
	} else {
		// 直接连接
		rawConn, err = dialer.DialContext(ctx, network, addr)
	}
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	// 提取 host（不含端口）
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	// 使用 utls 进行 TLS 握手（Chrome 指纹）
	tlsConn := utls.UClient(rawConn, &utls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}, utls.HelloChrome_Auto)

	// 移除 ALPN 中的 h2，强制 HTTP/1.1
	if err := removeH2FromALPN(tlsConn); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("remove h2 from ALPN: %w", err)
	}

	// 执行 TLS 握手
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("utls handshake: %w", err)
	}

	return tlsConn, nil
}

// removeH2FromALPN 从 utls ClientHello 中移除 h2 协议，强制 HTTP/1.1
func removeH2FromALPN(conn *utls.UConn) error {
	// 先构建 ClientHello（触发扩展初始化）
	if err := conn.BuildHandshakeState(); err != nil {
		return err
	}

	// 遍历 ClientHello 的扩展，找到 ALPN 扩展并移除 h2
	for _, ext := range conn.Extensions {
		if alpnExt, ok := ext.(*utls.ALPNExtension); ok {
			filtered := make([]string, 0, len(alpnExt.AlpnProtocols))
			for _, proto := range alpnExt.AlpnProtocols {
				if proto != "h2" {
					filtered = append(filtered, proto)
				}
			}
			// 确保至少保留 http/1.1
			if len(filtered) == 0 {
				filtered = []string{"http/1.1"}
			}
			alpnExt.AlpnProtocols = filtered
			break
		}
	}
	return nil
}

// getProxyURL 检查是否配置了 HTTP/HTTPS 代理
func getProxyURL(addr string) *url.URL {
	// 构造临时 HTTPS URL 用于 ProxyFromEnvironment 查询
	host, _, _ := net.SplitHostPort(addr)
	if host == "" {
		host = addr
	}
	tempReq := &http.Request{
		URL: &url.URL{
			Scheme: "https",
			Host:   host,
		},
	}
	proxyURL, err := http.ProxyFromEnvironment(tempReq)
	if err != nil || proxyURL == nil {
		return nil
	}
	return proxyURL
}

// dialThroughProxy 通过 HTTP 代理建立 CONNECT 隧道
func dialThroughProxy(ctx context.Context, dialer *net.Dialer, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	// 连接到代理服务器
	proxyAddr := proxyURL.Host
	if !hasPort(proxyAddr) {
		if proxyURL.Scheme == "https" {
			proxyAddr += ":443"
		} else {
			proxyAddr += ":80"
		}
	}

	proxyConn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy: %w", err)
	}

	// 发送 CONNECT 请求
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetAddr, targetAddr)
	// 添加代理认证（如果有）
	if proxyURL.User != nil {
		// 暂不实现代理认证（YAGNI），大多数场景不需要
		// 如需支持可在此处添加 Proxy-Authorization 头
	}
	connectReq += "\r\n"

	if _, err := proxyConn.Write([]byte(connectReq)); err != nil {
		proxyConn.Close()
		return nil, fmt.Errorf("send CONNECT: %w", err)
	}

	// 读取代理响应（简化处理，只检查状态行）
	buf := make([]byte, 1024)
	n, err := proxyConn.Read(buf)
	if err != nil {
		proxyConn.Close()
		return nil, fmt.Errorf("read proxy response: %w", err)
	}

	resp := string(buf[:n])
	// 检查状态码（HTTP/1.1 200 Connection established 或类似）
	if len(resp) < 12 || resp[9] != '2' {
		proxyConn.Close()
		return nil, fmt.Errorf("proxy CONNECT failed: %s", resp)
	}

	return proxyConn, nil
}

// hasPort 检查地址是否包含端口
func hasPort(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}
