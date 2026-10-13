package data

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

var (
	sharedTransport     *http.Transport
	sharedHTTPClient    *http.Client
	SharedHTTPClient    *resty.Client
	httpConfigMutex     sync.RWMutex
	currentProxyEnabled bool
	currentProxyURL     string
)

func init() {
	sharedTransport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   4,
		MaxConnsPerHost:       10,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		Proxy:                 nil,
	}

	sharedHTTPClient = &http.Client{
		Transport: sharedTransport,
		Timeout:   300 * time.Second,
	}

	SharedHTTPClient = resty.NewWithClient(sharedHTTPClient).
		SetRetryCount(0).
		SetTimeout(300 * time.Second)
}

func UpdateHTTPClientProxy(proxyURL string) {
	httpConfigMutex.Lock()
	defer httpConfigMutex.Unlock()

	if proxyURL == "" || proxyURL == currentProxyURL {
		return
	}

	sharedTransport.Proxy = http.ProxyURL(parseProxyURL(proxyURL))
	currentProxyURL = proxyURL
	currentProxyEnabled = true
}

func DisableHTTPClientProxy() {
	httpConfigMutex.Lock()
	defer httpConfigMutex.Unlock()

	sharedTransport.Proxy = nil
	currentProxyEnabled = false
	currentProxyURL = ""
}

func UpdateHTTPClientTimeout(timeout time.Duration) {
	sharedHTTPClient.Timeout = timeout
	SharedHTTPClient.SetTimeout(timeout)
}

func parseProxyURL(proxyURL string) *url.URL {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil
	}
	return u
}

func ConfigureFromSettings(config *SettingConfig) {
	if config == nil {
		return
	}

	if config.HttpProxyEnabled && config.HttpProxy != "" {
		UpdateHTTPClientProxy(config.HttpProxy)
	} else {
		DisableHTTPClientProxy()
	}

	if config.CrawlTimeOut > 0 {
		UpdateHTTPClientTimeout(time.Duration(config.CrawlTimeOut) * time.Second)
	} else {
		UpdateHTTPClientTimeout(300 * time.Second)
	}
}

func CreateHTTPClientWithTimeout(timeout time.Duration) *resty.Client {
	httpConfigMutex.RLock()
	transport := sharedTransport
	httpConfigMutex.RUnlock()

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return resty.NewWithClient(httpClient).
		SetTimeout(timeout).
		SetRetryCount(0)
}

func CreateDownloadClient() *resty.Client {
	httpConfigMutex.RLock()
	transport := sharedTransport
	httpConfigMutex.RUnlock()

	downloadTransport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          5,
		MaxIdleConnsPerHost:   2,
		MaxConnsPerHost:       2,
		IdleConnTimeout:       120 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		Proxy:                 transport.Proxy,
	}

	downloadHTTPClient := &http.Client{
		Transport: downloadTransport,
		Timeout:   0,
	}

	return resty.NewWithClient(downloadHTTPClient).
		SetTimeout(0).
		SetRetryCount(2).
		SetRetryWaitTime(5 * time.Second).
		SetRetryMaxWaitTime(30 * time.Second)
}

// GetSharedTransport 返回共享 HTTP Transport（包含用户代理配置），
// 供流式下载等场景复用代理设置。
func GetSharedTransport() *http.Transport {
	httpConfigMutex.RLock()
	defer httpConfigMutex.RUnlock()
	return sharedTransport
}

// CreateHTTPClientInsecureTLS 克隆共享 Transport，并跳过 TLS 证书校验。
// 用于企业代理 / 飞连等 HTTPS 中间人场景：系统/浏览器已信任企业根证书，
// 但 Go 默认证书池未包含时会出现 x509: certificate signed by unknown authority。
// 仅应在明确需要（如广场 API 证书校验失败后的回退）时使用。
func CreateHTTPClientInsecureTLS(timeout time.Duration) *resty.Client {
	httpConfigMutex.RLock()
	base := sharedTransport
	httpConfigMutex.RUnlock()

	tr := &http.Transport{
		DialContext:           base.DialContext,
		MaxIdleConns:          base.MaxIdleConns,
		MaxIdleConnsPerHost:   base.MaxIdleConnsPerHost,
		MaxConnsPerHost:       base.MaxConnsPerHost,
		IdleConnTimeout:       base.IdleConnTimeout,
		TLSHandshakeTimeout:   base.TLSHandshakeTimeout,
		ResponseHeaderTimeout: base.ResponseHeaderTimeout,
		ExpectContinueTimeout: base.ExpectContinueTimeout,
		ForceAttemptHTTP2:     base.ForceAttemptHTTP2,
		Proxy:                 base.Proxy,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // intentional fallback for MITM proxy CA
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return resty.NewWithClient(&http.Client{Transport: tr, Timeout: timeout}).
		SetTimeout(timeout).
		SetRetryCount(0)
}

// IsTLSUnknownAuthority 判断错误是否为证书不被信任（常见于企业 HTTPS 解密代理）。
func IsTLSUnknownAuthority(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "certificate signed by unknown authority") ||
		strings.Contains(msg, "tls: failed to verify certificate") ||
		strings.Contains(msg, "x509: certificate")
}
