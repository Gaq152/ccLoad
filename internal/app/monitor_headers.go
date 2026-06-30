package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const monitorHeaderValueLimit = 512

func headersForMonitor(headers http.Header, host string) string {
	if len(headers) == 0 && host == "" {
		return ""
	}

	out := make(map[string][]string, len(headers)+1)
	if host != "" {
		out["Host"] = []string{host}
	}

	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		values := headers.Values(key)
		if len(values) == 0 {
			continue
		}
		if isSensitiveMonitorHeader(key) {
			out[key] = redactMonitorHeaderValues(values)
		} else {
			out[key] = trimMonitorHeaderValues(values)
		}
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

func isSensitiveMonitorHeader(name string) bool {
	lowerName := strings.ToLower(name)
	return strings.Contains(lowerName, "authorization") ||
		strings.Contains(lowerName, "cookie") ||
		strings.Contains(lowerName, "token") ||
		strings.Contains(lowerName, "api-key") ||
		strings.Contains(lowerName, "x-goog-api-key") ||
		strings.Contains(lowerName, "chatgpt-account-id")
}

func redactMonitorHeaderValues(values []string) []string {
	redacted := make([]string, 0, len(values))
	for _, value := range values {
		redacted = append(redacted, fmt.Sprintf("<redacted len=%d>", len(value)))
	}
	return redacted
}

func trimMonitorHeaderValues(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if len(value) > monitorHeaderValueLimit {
			trimmed = append(trimmed, fmt.Sprintf("%s...(truncated, len=%d)", value[:monitorHeaderValueLimit], len(value)))
		} else {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}
