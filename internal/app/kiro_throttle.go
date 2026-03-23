package app

import (
	"crypto/rand"
	"math/big"
	"strconv"
	"sync"
	"time"
)

// kiroKeyThrottle Kiro 预设专用的 per-key 请求节流器
// 确保同一 Key 的请求之间有随机 1-3 秒间隔，模拟真实 IDE 单用户行为
type kiroKeyThrottle struct {
	mu       sync.Mutex
	lastTime map[string]time.Time // channelID:keyIndex -> 上次请求时间
}

var (
	globalKiroThrottle *kiroKeyThrottle
	kiroThrottleOnce   sync.Once
)

// getKiroThrottle 获取全局 Kiro 节流器
func getKiroThrottle() *kiroKeyThrottle {
	kiroThrottleOnce.Do(func() {
		globalKiroThrottle = &kiroKeyThrottle{
			lastTime: make(map[string]time.Time),
		}
	})
	return globalKiroThrottle
}

// Wait 等待直到可以发送请求，返回实际等待的时间
// 同一 Key 的请求之间强制随机 1-3 秒间隔
func (t *kiroKeyThrottle) Wait(channelID int64, keyIndex int) time.Duration {
	key := throttleKey(channelID, keyIndex)

	t.mu.Lock()
	last, exists := t.lastTime[key]
	// 先生成本次需要的最小间隔（1-3 秒随机）
	minInterval := randomInterval()
	now := time.Now()

	if exists {
		elapsed := now.Sub(last)
		if elapsed < minInterval {
			waitDur := minInterval - elapsed
			// 更新为预计完成时间，防止后续请求也挤进来
			t.lastTime[key] = now.Add(waitDur)
			t.mu.Unlock()
			time.Sleep(waitDur)
			return waitDur
		}
	}

	// 无需等待，记录当前时间
	t.lastTime[key] = now
	t.mu.Unlock()
	return 0
}

// Remove 移除指定渠道的所有节流记录（渠道删除时调用）
func (t *kiroKeyThrottle) Remove(channelID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// 遍历清除该渠道下所有 key
	prefix := strconv.FormatInt(channelID, 10) + ":"
	for k := range t.lastTime {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(t.lastTime, k)
		}
	}
}

// throttleKey 构建节流键
func throttleKey(channelID int64, keyIndex int) string {
	return strconv.FormatInt(channelID, 10) + ":" + strconv.Itoa(keyIndex)
}

// randomInterval 生成 1-3 秒的随机间隔
func randomInterval() time.Duration {
	// 1000-3000 毫秒随机
	n, err := rand.Int(rand.Reader, big.NewInt(2001)) // [0, 2000]
	if err != nil {
		return 2 * time.Second // 降级使用固定 2 秒
	}
	return time.Duration(1000+n.Int64()) * time.Millisecond
}
