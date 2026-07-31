package util

import (
	"fmt"
	"strings"
	"testing"
)

func TestDetectRequestType(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		want string
	}{
		{
			name: "responses",
			path: "/v1/responses",
			body: `{"model":"gpt-5.6-sol","input":[{"role":"user","content":"hello"}]}`,
			want: RequestTypeResponses,
		},
		{
			name: "compact v1",
			path: "/v1/responses/compact",
			body: `{"model":"gpt-5.6-sol","input":[]}`,
			want: RequestTypeCompactV1,
		},
		{
			name: "compact v2 minified",
			path: "/v1/responses",
			body: `{"model":"gpt-5.6-sol","input":[{"role":"user","content":"hello"},{"type":"compaction_trigger"}]}`,
			want: RequestTypeCompactV2,
		},
		{
			name: "compact v2 spaced",
			path: "/v1/responses/",
			body: `{ "input": [{ "type" : "compaction_trigger" }] }`,
			want: RequestTypeCompactV2,
		},
		{
			name: "keyword in user text is not compact",
			path: "/v1/responses",
			body: `{"input":[{"role":"user","content":"search for compaction_trigger"}]}`,
			want: RequestTypeResponses,
		},
		{
			name: "search",
			path: CodexSearchPath,
			body: `{"model":"gpt-5.6-sol","query":"Go"}`,
			want: RequestTypeSearch,
		},
		{
			name: "other endpoint",
			path: "/v1/messages",
			body: `{"model":"claude"}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectRequestType(tt.path, []byte(tt.body)); got != tt.want {
				t.Fatalf("DetectRequestType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectRequestType_CompactTriggerAfterCaptureLimit(t *testing.T) {
	padding := strings.Repeat("x", 1024*1024+128)
	body := fmt.Sprintf(`{"input":[{"role":"user","content":"%s"},{"type":"compaction_trigger"}]}`, padding)

	if got := DetectRequestType("/v1/responses", []byte(body)); got != RequestTypeCompactV2 {
		t.Fatalf("DetectRequestType() = %q, want %q", got, RequestTypeCompactV2)
	}
}
