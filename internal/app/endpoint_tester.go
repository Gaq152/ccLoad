package app

import (
	"context"
	"log"
	"sync"
	"time"
)

// EndpointTester 后台端点测速服务
// 定期测速所有开启自动选择的渠道端点，并自动切换到最快端点
type EndpointTester struct {
	server   *Server
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewEndpointTester 创建端点测速服务
func NewEndpointTester(server *Server, intervalSeconds int) *EndpointTester {
	if intervalSeconds <= 0 {
		return nil // 禁用
	}
	return &EndpointTester{
		server:   server,
		interval: time.Duration(intervalSeconds) * time.Second,
		stopCh:   make(chan struct{}),
	}
}

// Start 启动后台测速
func (t *EndpointTester) Start() {
	if t == nil {
		return
	}
	t.wg.Add(1)
	go t.loop()
	log.Printf("[INFO] 后台端点测速已启动（间隔: %v）", t.interval)
}

// Stop 停止后台测速
func (t *EndpointTester) Stop() {
	if t == nil {
		return
	}
	close(t.stopCh)
	t.wg.Wait()
	log.Print("[INFO] 后台端点测速已停止")
}

// loop 测速循环
func (t *EndpointTester) loop() {
	defer t.wg.Done()

	// 启动后等待一个周期再开始测速（避免启动时立即测速）
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.testAllEndpoints()
		}
	}
}

// testAllEndpoints 测速所有开启自动选择的渠道端点
func (t *EndpointTester) testAllEndpoints() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 获取所有开启自动选择的渠道
	channels, err := t.server.store.GetChannelsWithAutoSelect(ctx)
	if err != nil {
		log.Printf("[WARN] 后台测速: 获取渠道列表失败: %v", err)
		return
	}

	if len(channels) == 0 {
		return
	}

	// 从配置读取测试次数
	testCount := t.server.configService.GetIntMin("endpoint_test_count", 3, 1)
	if testCount > 10 {
		testCount = 10
	}

	// 并发测速所有渠道（限制并发数避免资源耗尽）
	sem := make(chan struct{}, 5) // 最多同时测5个渠道
	var wg sync.WaitGroup

	for _, ch := range channels {
		wg.Add(1)
		go func(channelID int64) {
			defer wg.Done()

			sem <- struct{}{}        // 获取信号量
			defer func() { <-sem }() // 释放信号量

			t.testChannelEndpoints(ctx, channelID, testCount)
		}(ch.ID)
	}

	wg.Wait()
}

// testChannelEndpoints 测速单个渠道的所有端点
func (t *EndpointTester) testChannelEndpoints(ctx context.Context, channelID int64, testCount int) {
	endpoints, err := t.server.store.ListEndpoints(ctx, channelID)
	if err != nil || len(endpoints) == 0 {
		return
	}

	// 并发测速
	latencyResults := make(map[int64]int)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, ep := range endpoints {
		wg.Add(1)
		go func(endpointID int64, url string) {
			defer wg.Done()

			info, _ := t.server.testEndpointLatencyMulti(url, testCount)
			if info.LatencyMs >= 0 {
				mu.Lock()
				latencyResults[endpointID] = info.LatencyMs
				mu.Unlock()
			}
		}(ep.ID, ep.URL)
	}

	wg.Wait()

	// 保存测速结果
	if len(latencyResults) > 0 {
		_ = t.server.store.UpdateEndpointsLatency(ctx, latencyResults)
		// 选择最快的端点
		_ = t.server.store.SelectFastestEndpoint(ctx, channelID)
	}
}
