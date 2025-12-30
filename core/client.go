package core

import (
	"net"
	"net/http"
	"time"
)

// GlobalHTTPClient 全局单例 HTTP Client
var GlobalHTTPClient *http.Client

// InitHTTPClient 初始化高性能 HTTP Client
func InitHTTPClient() {
	GlobalHTTPClient = &http.Client{
		Timeout: 0, // 禁用全局超时，由 Request Context 控制
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 60 * time.Second, // 保持 TCP 连接活跃
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1000,             // 最大空闲连接数
			MaxIdleConnsPerHost:   100,              // 每个 Host 的最大空闲连接数
			IdleConnTimeout:       90 * time.Second, // 空闲连接超时
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second, // 等待首字节超时
		},
	}
}
