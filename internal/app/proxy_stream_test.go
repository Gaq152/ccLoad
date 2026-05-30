package app

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStartKeepalive_WritesHeartbeats 验证心跳按间隔写入注释行
func TestStartKeepalive_WritesHeartbeats(t *testing.T) {
	rec := httptest.NewRecorder()
	stop := startKeepalive(rec, 20*time.Millisecond, nil)
	time.Sleep(70 * time.Millisecond) // 应触发约 3 次心跳
	stop()

	body := rec.Body.String()
	count := strings.Count(body, ": keepalive\n\n")
	if count < 2 {
		t.Fatalf("expected at least 2 keepalive heartbeats, got %d (body=%q)", count, body)
	}
}

// TestStartKeepalive_StopHaltsWrites 验证 stop() 后不再写入（无并发写竞争）
func TestStartKeepalive_StopHaltsWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	stop := startKeepalive(rec, 10*time.Millisecond, nil)
	time.Sleep(35 * time.Millisecond)
	stop()
	lenAfterStop := rec.Body.Len()

	// stop 之后再等，长度不应增加
	time.Sleep(40 * time.Millisecond)
	if rec.Body.Len() != lenAfterStop {
		t.Fatalf("keepalive kept writing after stop: before=%d after=%d", lenAfterStop, rec.Body.Len())
	}
}

// TestStartKeepalive_StopIdempotent 验证 stop() 可安全多次调用
func TestStartKeepalive_StopIdempotent(t *testing.T) {
	rec := httptest.NewRecorder()
	stop := startKeepalive(rec, 50*time.Millisecond, nil)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); stop() }()
	}
	wg.Wait() // 不应 panic 或死锁
}

// TestWriteStreamErrorAndFinish 验证已发头后写 SSE error 事件的格式
func TestWriteStreamErrorAndFinish(t *testing.T) {
	rec := httptest.NewRecorder()
	s := &Server{}
	s.writeStreamErrorAndFinish(rec, errTestUpstream)

	body := rec.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Errorf("missing 'event: error' line: %q", body)
	}
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf("missing error type in payload: %q", body)
	}
	if !strings.Contains(body, "upstream boom") {
		t.Errorf("missing cause message: %q", body)
	}
}

var errTestUpstream = &testErr{"upstream boom"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
