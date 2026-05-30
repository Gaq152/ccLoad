package app

import (
	"bytes"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	cases := map[string]logLevel{
		"debug":   levelDebug,
		"DEBUG":   levelDebug,
		" info ":  levelInfo,
		"warn":    levelWarn,
		"warning": levelWarn,
		"error":   levelError,
		"":        levelInfo, // 默认
		"garbage": levelInfo, // 无法识别回退 info
	}
	for in, want := range cases {
		if got := parseLogLevel(in); got != want {
			t.Errorf("parseLogLevel(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestLineLevel(t *testing.T) {
	cases := map[string]logLevel{
		"2026/05/30 19:00:00 [DEBUG] foo": levelDebug,
		"2026/05/30 19:00:00 [INFO] foo":  levelInfo,
		"2026/05/30 19:00:00 [WARN] foo":  levelWarn,
		"2026/05/30 19:00:00 [ERROR] foo": levelError,
		"2026/05/30 19:00:00 [FATAL] foo": levelError,
		"[GIN] 200 GET /foo":              levelNone, // 无级别前缀放行
		"listening on :8080":              levelNone, // 裸日志放行
	}
	for line, want := range cases {
		if got := lineLevel([]byte(line)); got != want {
			t.Errorf("lineLevel(%q) = %d, want %d", line, got, want)
		}
	}
}

func TestLevelFilterWriter(t *testing.T) {
	tests := []struct {
		name      string
		threshold logLevel
		line      string
		wantPass  bool
	}{
		{"info阈值丢弃debug", levelInfo, "x [DEBUG] hidden\n", false},
		{"info阈值放行info", levelInfo, "x [INFO] shown\n", true},
		{"info阈值放行warn", levelInfo, "x [WARN] shown\n", true},
		{"debug阈值放行debug", levelDebug, "x [DEBUG] shown\n", true},
		{"warn阈值丢弃info", levelWarn, "x [INFO] hidden\n", false},
		{"无前缀始终放行", levelWarn, "[GIN] 200\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := &levelFilterWriter{threshold: tt.threshold, out: &buf}
			n, err := w.Write([]byte(tt.line))
			if err != nil {
				t.Fatalf("Write error: %v", err)
			}
			// Write 始终应返回完整长度（即便丢弃），避免 log 报错
			if n != len(tt.line) {
				t.Errorf("Write returned n=%d, want %d", n, len(tt.line))
			}
			passed := buf.Len() > 0
			if passed != tt.wantPass {
				t.Errorf("line %q passed=%v, want %v", tt.line, passed, tt.wantPass)
			}
		})
	}
}
