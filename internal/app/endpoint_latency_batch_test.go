package app

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEndpointLatencyBatch_ReusesSameURL(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	srv := &Server{
		client: upstream.Client(),
	}
	batch := newEndpointLatencyBatch(srv, 1)

	const callers = 6
	results := make([]endpointTestInfo, callers)

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			info, err := batch.test(upstream.URL)
			if err != nil {
				t.Errorf("batch.test returned error: %v", err)
				return
			}
			results[idx] = info
		}(i)
	}
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Fatalf("expected exactly 1 upstream request, got %d", got)
	}

	for i, info := range results {
		if info.StatusCode != http.StatusNoContent {
			t.Fatalf("result[%d] status code = %d, want %d", i, info.StatusCode, http.StatusNoContent)
		}
		if info.TestCount != 1 {
			t.Fatalf("result[%d] test count = %d, want 1", i, info.TestCount)
		}
		if info.LatencyMs < 0 {
			t.Fatalf("result[%d] latency = %d, want non-negative", i, info.LatencyMs)
		}
	}
}
