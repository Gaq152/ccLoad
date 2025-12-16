package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// 外部健康监控数据结构
type ExternalHealthRecord struct {
	Provider      string               `json:"provider"`
	Service       string               `json:"service"` // "cc" or "cx"
	Channel       string               `json:"channel"`
	ProbeURL      string               `json:"probe_url"`
	CurrentStatus ExternalCurrentStatus `json:"current_status"`
	Timeline      []ExternalTimeline   `json:"timeline"`
}

type ExternalCurrentStatus struct {
	Status  int `json:"status"`  // 0=不可用, 1=可用
	Latency int `json:"latency"` // 毫秒
}

type ExternalTimeline struct {
	Time         string                `json:"time"`
	Timestamp    int64                 `json:"timestamp"`
	Status       int                   `json:"status"`
	Latency      int                   `json:"latency"`
	Availability float64               `json:"availability"`
	StatusCounts ExternalStatusCounts  `json:"status_counts"`
}

type ExternalStatusCounts struct {
	Available      int                    `json:"available"`
	Degraded       int                    `json:"degraded"`
	Unavailable    int                    `json:"unavailable"`
	Missing        int                    `json:"missing"`
	SlowLatency    int                    `json:"slow_latency"`
	RateLimit      int                    `json:"rate_limit"`
	ServerError    int                    `json:"server_error"`
	ClientError    int                    `json:"client_error"`
	AuthError      int                    `json:"auth_error"`
	InvalidRequest int                    `json:"invalid_request"`
	NetworkError   int                    `json:"network_error"`
	ContentMismatch int                   `json:"content_mismatch"`
	HTTPCodeBreakdown map[string]map[string]int `json:"http_code_breakdown"`
}

// ExternalHealthResponse 匹配外部API的响应封装格式
type ExternalHealthResponse struct {
	Data []ExternalHealthRecord `json:"data"`
}

// 缓存结构
type healthCacheEntry struct {
	data      []ExternalHealthRecord
	fetchedAt time.Time
}

var (
	healthCache      = make(map[string]*healthCacheEntry)
	healthCacheMutex sync.RWMutex
	healthCacheTTL   = 60 * time.Second // 60秒缓存
)

// handleChannelHealthProxy 代理第三方健康监控API
func (s *Server) handleChannelHealthProxy(c *gin.Context) {
	period := c.DefaultQuery("period", "24h")

	// 验证period参数
	if period != "24h" && period != "7d" && period != "30d" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid period, must be 24h, 7d, or 30d"})
		return
	}

	// 检查缓存
	healthCacheMutex.RLock()
	cached, exists := healthCache[period]
	healthCacheMutex.RUnlock()

	if exists && time.Since(cached.fetchedAt) < healthCacheTTL {
		c.JSON(http.StatusOK, cached.data)
		return
	}

	// 获取新数据
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://relaypulse.top/api/status?period=%s", period)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		// 如果有旧缓存，返回旧数据
		if exists {
			c.Header("X-Cache-Stale", "true")
			c.JSON(http.StatusOK, cached.data)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create request"})
		return
	}

	// 设置浏览器请求头（模拟真实浏览器访问）
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://relaypulse.top/")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// 返回旧缓存或错误
		if exists {
			c.Header("X-Cache-Stale", "true")
			c.JSON(http.StatusOK, cached.data)
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{
			"error":       "upstream fetch failed",
			"retry_after": 30,
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 返回旧缓存或错误
		if exists {
			c.Header("X-Cache-Stale", "true")
			c.JSON(http.StatusOK, cached.data)
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{
			"error":       fmt.Sprintf("upstream returned %d", resp.StatusCode),
			"retry_after": 30,
		})
		return
	}

	var upstream ExternalHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&upstream); err != nil {
		// 返回旧缓存或错误
		if exists {
			c.Header("X-Cache-Stale", "true")
			c.JSON(http.StatusOK, cached.data)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode response"})
		return
	}

	// 验证必要字段并过滤无效记录
	validData := make([]ExternalHealthRecord, 0, len(upstream.Data))
	for _, record := range upstream.Data {
		if record.Provider == "" || record.Service == "" ||
		   record.Channel == "" || record.ProbeURL == "" {
			continue // 跳过缺少必要字段的记录
		}
		validData = append(validData, record)
	}

	// 更新缓存
	healthCacheMutex.Lock()
	healthCache[period] = &healthCacheEntry{
		data:      validData,
		fetchedAt: time.Now(),
	}
	healthCacheMutex.Unlock()

	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, validData)
}
