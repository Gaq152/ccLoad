package model

import (
	"strconv"
	"time"
)

// JSONTime stores timestamps as Unix seconds in JSON.
type JSONTime struct {
	time.Time
}

// MarshalJSON implements json.Marshaler.
func (jt JSONTime) MarshalJSON() ([]byte, error) {
	if jt.Time.IsZero() {
		return []byte("0"), nil
	}
	return []byte(strconv.FormatInt(jt.Time.Unix(), 10)), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (jt *JSONTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == "0" {
		jt.Time = time.Time{}
		return nil
	}
	ts, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return err
	}
	jt.Time = time.Unix(ts, 0)
	return nil
}

// LogEntry is a request log record.
type LogEntry struct {
	ID            int64    `json:"id"`
	Time          JSONTime `json:"time"`
	Model         string   `json:"model"`
	ChannelID     int64    `json:"channel_id"`
	ChannelName   string   `json:"channel_name,omitempty"`
	ChannelType   string   `json:"channel_type,omitempty"`
	StatusCode    int      `json:"status_code"`
	Message       string   `json:"message"`
	Duration      float64  `json:"duration"`
	IsStreaming   bool     `json:"is_streaming"`
	FirstByteTime float64  `json:"first_byte_time"`
	APIKeyUsed    string   `json:"api_key_used"`
	APIKeyHash    string   `json:"api_key_hash,omitempty"`
	APIBaseURL    string   `json:"api_base_url"`
	AuthTokenID   int64    `json:"auth_token_id"`
	AuthTokenName string   `json:"auth_token_name,omitempty"`
	ClientIP      string   `json:"client_ip"`

	InputTokens              int     `json:"input_tokens"`
	OutputTokens             int     `json:"output_tokens"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens"`
	Cost                     float64 `json:"cost"`
	IsFast                   bool    `json:"is_fast"`
	ServiceTier              string  `json:"service_tier,omitempty"`
	FastMultiplier           float64 `json:"fast_multiplier"`
}

// LogFilter is the request log query filter.
type LogFilter struct {
	ChannelID       *int64
	ChannelIDLike   string
	ChannelName     string
	ChannelNameLike string
	Model           string
	ModelLike       string
	StatusCode      *int
	StatusCodeLike  string
	ChannelType     string
	AuthTokenID     *int64
}
