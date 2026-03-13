package app

import (
	"strings"
	"sync"
)

type endpointLatencyBatch struct {
	server    *Server
	testCount int

	mu       sync.Mutex
	cache    map[string]endpointTestInfo
	inflight map[string]*endpointLatencyCall
}

type endpointLatencyCall struct {
	done chan struct{}
	info endpointTestInfo
	err  error
}

func newEndpointLatencyBatch(server *Server, testCount int) *endpointLatencyBatch {
	if testCount < 1 {
		testCount = 1
	}
	return &endpointLatencyBatch{
		server:    server,
		testCount: testCount,
		cache:     make(map[string]endpointTestInfo),
		inflight:  make(map[string]*endpointLatencyCall),
	}
}

func (b *endpointLatencyBatch) test(url string) (endpointTestInfo, error) {
	key := strings.TrimSpace(url)
	if key == "" {
		return endpointTestInfo{LatencyMs: -1, StatusCode: 0, TestCount: 0}, nil
	}

	b.mu.Lock()
	if info, ok := b.cache[key]; ok {
		b.mu.Unlock()
		return info, nil
	}
	if call, ok := b.inflight[key]; ok {
		b.mu.Unlock()
		<-call.done
		return call.info, call.err
	}

	call := &endpointLatencyCall{done: make(chan struct{})}
	b.inflight[key] = call
	b.mu.Unlock()

	call.info, call.err = b.server.testEndpointLatencyMulti(key, b.testCount)

	b.mu.Lock()
	delete(b.inflight, key)
	b.cache[key] = call.info
	close(call.done)
	b.mu.Unlock()

	return call.info, call.err
}
