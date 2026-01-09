package app

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
	"sync"
)

// ResponseCapture 是一个 ResponseWriter 包装器，用于捕获响应数据
// 类似抓包工具的原理：在最外层拦截所有写入的数据
type ResponseCapture struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
	mu         sync.Mutex
	maxSize    int  // 最大捕获大小（字节）
	truncated  bool // 是否已截断
}

// NewResponseCapture 创建响应捕获器
// maxSize: 最大捕获大小，超过后停止捕获（避免内存问题）
func NewResponseCapture(w http.ResponseWriter, maxSize int) *ResponseCapture {
	return &ResponseCapture{
		ResponseWriter: w,
		statusCode:     http.StatusOK, // 默认状态码
		body:           &bytes.Buffer{},
		maxSize:        maxSize,
	}
}

// WriteHeader 捕获状态码
func (rc *ResponseCapture) WriteHeader(code int) {
	rc.statusCode = code
	rc.ResponseWriter.WriteHeader(code)
}

// Write 捕获响应体数据
func (rc *ResponseCapture) Write(b []byte) (int, error) {
	// 写入原始 ResponseWriter
	n, err := rc.ResponseWriter.Write(b)

	// 捕获数据（线程安全）
	rc.mu.Lock()
	if !rc.truncated && rc.body.Len() < rc.maxSize {
		remaining := rc.maxSize - rc.body.Len()
		if len(b) <= remaining {
			rc.body.Write(b)
		} else {
			rc.body.Write(b[:remaining])
			rc.truncated = true
		}
	}
	rc.mu.Unlock()

	return n, err
}

// Flush 支持流式响应的 Flusher 接口
func (rc *ResponseCapture) Flush() {
	if flusher, ok := rc.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// StatusCode 获取捕获的状态码
func (rc *ResponseCapture) StatusCode() int {
	return rc.statusCode
}

// Body 获取捕获的响应体
func (rc *ResponseCapture) Body() []byte {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.body.Bytes()
}

// IsTruncated 返回响应是否因超过大小限制而被截断
func (rc *ResponseCapture) IsTruncated() bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.truncated
}

// Hijack 支持 WebSocket 等需要劫持连接的场景
func (rc *ResponseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rc.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Push 支持 HTTP/2 Server Push
func (rc *ResponseCapture) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := rc.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}
