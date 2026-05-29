package app

import (
	"context"
	"io"
	"net/http"
	"time"
)

// SSEKeepaliveInterval SSE 心跳间隔（CF 免费计划 proxy read timeout ~100s，留余量）
const SSEKeepaliveInterval = 60 * time.Second

// sseKeepalivePayload SSE 心跳数据（注释行，客户端忽略）
var sseKeepalivePayload = []byte(": keepalive\n\n")

// ============================================================================
// 流式传输数据结构
// ============================================================================

// streamReadStats 流式传输统计信息
type streamReadStats struct {
	readCount  int
	totalBytes int64
}

// firstByteDetector 检测首字节读取时间和传输统计的Reader包装器
type firstByteDetector struct {
	io.ReadCloser
	stats       *streamReadStats
	onFirstRead func()
	onBytesRead func(int64) // 可选：每次读取后的回调（nil 时不触发）
}

// Read 实现io.Reader接口，记录读取统计
func (r *firstByteDetector) Read(p []byte) (n int, err error) {
	n, err = r.ReadCloser.Read(p)
	if n > 0 {
		// 记录统计信息
		if r.stats != nil {
			r.stats.readCount++
			r.stats.totalBytes += int64(n)
		}
		// 触发首次读取回调
		if r.onFirstRead != nil {
			r.onFirstRead()
			r.onFirstRead = nil // 只触发一次
		}
		// 触发字节读取回调（可选）
		if r.onBytesRead != nil {
			r.onBytesRead(int64(n))
		}
	}
	return
}

// ============================================================================
// 流式传输核心函数
// ============================================================================

// streamCopy 流式复制（支持flusher与ctx取消）
// 从proxy.go提取，遵循SRP原则
// 简化实现：直接循环读取与写入，避免为每次读取创建goroutine导致泄漏
// 首字节超时依赖于上游握手/响应头阶段的超时控制（Transport 配置），此处不再重复实现
func streamCopy(ctx context.Context, src io.Reader, dst http.ResponseWriter, onData func([]byte) error) error {
	buf := make([]byte, StreamBufferSize)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			if flusher, ok := dst.(http.Flusher); ok {
				flusher.Flush()
			}
			if onData != nil {
				if hookErr := onData(buf[:n]); hookErr != nil {
					// 钩子错误不中断流传输（容错设计）
					// 错误日志由钩子内部自行处理
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			// [FIX] 检查 context 是否在 Read 期间被取消
			// 场景：客户端取消请求 → HTTP/2 流关闭 → Read 返回 "http2: response body closed"
			// 此时应返回 context.Canceled，让上层正确识别为客户端断开（499）而非上游错误（502）
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}

// streamCopySSE SSE专用流式复制（使用小缓冲区优化延迟 + 心跳保活）
// [INFO] SSE优化（2025-10-17）：4KB缓冲区降低首Token延迟60~80%
// [INFO] 支持数据钩子（2025-11）：允许SSE usage解析器增量处理数据流
// [INFO] 心跳保活（2026-05）：每60秒发送SSE注释行，防止CDN空闲断开
// 设计原则：SSE事件通常200B-2KB，小缓冲区避免事件积压
func streamCopySSE(ctx context.Context, src io.Reader, dst http.ResponseWriter, onData func([]byte) error) error {
	type readResult struct {
		n   int
		err error
	}

	buf := make([]byte, SSEBufferSize)
	readCh := make(chan readResult, 1)
	keepalive := time.NewTimer(SSEKeepaliveInterval)
	defer keepalive.Stop()

	flusher, _ := dst.(http.Flusher)

	// 启动首次读取
	go func() {
		n, err := src.Read(buf)
		readCh <- readResult{n, err}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-keepalive.C:
			if _, writeErr := dst.Write(sseKeepalivePayload); writeErr != nil {
				return writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
			keepalive.Reset(SSEKeepaliveInterval)

		case res := <-readCh:
			if res.n > 0 {
				if _, writeErr := dst.Write(buf[:res.n]); writeErr != nil {
					return writeErr
				}
				if flusher != nil {
					flusher.Flush()
				}
				if onData != nil {
					if hookErr := onData(buf[:res.n]); hookErr != nil {
						// 钩子错误不中断流传输（容错设计）
					}
				}
				keepalive.Reset(SSEKeepaliveInterval)
			}
			if res.err != nil {
				if res.err == io.EOF {
					return nil
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return res.err
			}
			// 启动下一次读取
			go func() {
				n, err := src.Read(buf)
				readCh <- readResult{n, err}
			}()
		}
	}
}

// readWithKeepalive 带心跳的完整读取（用于 Kiro 渠道）
// 在读取上游数据期间，每 SSEKeepaliveInterval 向客户端发送心跳注释
// 防止 CDN 因长时间无数据而断开客户端连接
func readWithKeepalive(ctx context.Context, src io.Reader, dst http.ResponseWriter) ([]byte, error) {
	type readResult struct {
		data []byte
		err  error
	}

	readCh := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(src)
		readCh <- readResult{data, err}
	}()

	keepalive := time.NewTimer(SSEKeepaliveInterval)
	defer keepalive.Stop()

	flusher, _ := dst.(http.Flusher)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-keepalive.C:
			if _, writeErr := dst.Write(sseKeepalivePayload); writeErr != nil {
				return nil, writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
			keepalive.Reset(SSEKeepaliveInterval)

		case res := <-readCh:
			return res.data, res.err
		}
	}
}
