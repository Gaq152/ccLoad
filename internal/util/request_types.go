package util

import (
	"bytes"
	"encoding/json"
	"strings"
)

// 请求类型用于日志和监控展示。空值表示当前没有需要特别区分的端点类型。
const (
	RequestTypeResponses = "responses"
	RequestTypeCompactV1 = "compact_v1"
	RequestTypeCompactV2 = "compact_v2"
	RequestTypeSearch    = "search"
)

const compactionTriggerType = "compaction_trigger"

// DetectRequestType 根据原始入站路径和完整请求体识别 Codex 请求类型。
//
// V2 压缩仍使用 /responses，只有请求体中的 compaction_trigger 能将它与普通
// Responses 请求区分开。识别必须发生在监控截断请求体之前。
func DetectRequestType(requestPath string, body []byte) string {
	path := strings.TrimSuffix(requestPath, "/")

	switch {
	case strings.HasSuffix(path, "/responses/compact"):
		return RequestTypeCompactV1
	case path == CodexSearchPath || strings.HasSuffix(path, "/alpha/search"):
		return RequestTypeSearch
	case strings.HasSuffix(path, "/responses"):
		if hasJSONTypeValue(body, compactionTriggerType) {
			return RequestTypeCompactV2
		}
		return RequestTypeResponses
	default:
		return ""
	}
}

// hasJSONTypeValue 先用字节检索快速排除绝大多数普通请求；命中后再按 JSON
// token 验证，避免用户消息中仅仅提到 "compaction_trigger" 时被误判。
func hasJSONTypeValue(body []byte, target string) bool {
	if len(body) == 0 || !bytes.Contains(body, []byte(target)) {
		return false
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	first, err := decoder.Token()
	if err != nil {
		return false
	}
	found, err := jsonTokenContainsType(decoder, first, target)
	return err == nil && found
}

func jsonTokenContainsType(decoder *json.Decoder, token json.Token, target string) (bool, error) {
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return false, nil
	}

	switch delim {
	case '{':
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false, err
			}
			key, _ := keyToken.(string)

			valueToken, err := decoder.Token()
			if err != nil {
				return false, err
			}
			if key == "type" {
				if value, ok := valueToken.(string); ok && value == target {
					return true, nil
				}
			}
			if found, err := jsonTokenContainsType(decoder, valueToken, target); err != nil || found {
				return found, err
			}
		}
		_, err := decoder.Token() // consume }
		return false, err
	case '[':
		for decoder.More() {
			valueToken, err := decoder.Token()
			if err != nil {
				return false, err
			}
			if found, err := jsonTokenContainsType(decoder, valueToken, target); err != nil || found {
				return found, err
			}
		}
		_, err := decoder.Token() // consume ]
		return false, err
	default:
		return false, nil
	}
}
