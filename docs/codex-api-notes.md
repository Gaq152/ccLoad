# Codex / ChatGPT API 笔记

## 用量查询接口

**URL**: `https://chatgpt.com/backend-api/wham/usage`

**Method**: GET

**Headers**:
- `Authorization: Bearer {access_token}`
- `chatgpt-account-id: {account_id}`

**响应示例**:
```json
{
    "plan_type": "team",
    "rate_limit": {
        "allowed": true,
        "limit_reached": false,
        "primary_window": {
            "used_percent": 2,
            "limit_window_seconds": 18000,
            "reset_after_seconds": 10848,
            "reset_at": 1766053355
        },
        "secondary_window": {
            "used_percent": 1,
            "limit_window_seconds": 604800,
            "reset_after_seconds": 597648,
            "reset_at": 1766640155
        }
    },
    "code_review_rate_limit": {
        "allowed": true,
        "limit_reached": false,
        "primary_window": {
            "used_percent": 0,
            "limit_window_seconds": 604800,
            "reset_after_seconds": 604800,
            "reset_at": 1766647307
        },
        "secondary_window": null
    },
    "credits": {
        "has_credits": false,
        "unlimited": false,
        "balance": null,
        "approx_local_messages": null,
        "approx_cloud_messages": null
    }
}
```

---

## OAuth 认证配置 (done-hub 分析)

### 授权流程

| 配置项 | 值 |
|--------|-----|
| Authorize URL | `https://auth.openai.com/oauth/authorize` |
| Token URL (首次) | `https://auth.openai.com/oauth/token` |
| Token URL (刷新) | `https://auth0.openai.com/oauth/token` |
| Client ID (Codex CLI) | `app_EMoamEEZ73f0CkXaXp7hrann` |
| Client ID (done-hub) | `pdlLIX2Y72MIl2rhLhTE9VV9bN905kBh` |
| Redirect URI | `http://localhost:1455/auth/callback` |
| Scope | `openid profile email offline_access` |

### Token 刷新参数

```
POST https://auth0.openai.com/oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token
client_id={client_id}
refresh_token={refresh_token}
```

### JWT 解析 account_id

```javascript
// 从 JWT claims 中提取
claims["https://api.openai.com/auth"]["chatgpt_account_id"]
```

---

## Codex Responses API

**URL**: `https://chatgpt.com/backend-api/codex/responses`

**Method**: POST

**Headers**:
```
Content-Type: application/json
Accept: text/event-stream
Host: chatgpt.com
Authorization: Bearer {access_token}
chatgpt-account-id: {account_id}
User-Agent: codex_cli_rs/0.72.0 (Windows 10.0.19045; x86_64) vscode/1.107.0
version: 0.72.0
conversation_id: {uuid}
session_id: {uuid}
originator: codex_cli_rs
```

**请求体关键字段**:
```json
{
  "model": "gpt-5.1-codex-max",
  "stream": true,
  "store": false,
  "instructions": "You are Codex, based on GPT-5...",
  "input": [
    {
      "type": "message",
      "role": "user",
      "content": [{"type": "input_text", "text": "<environment_context>...</environment_context>"}]
    },
    {
      "type": "message",
      "role": "user",
      "content": [{"type": "input_text", "text": "用户消息"}]
    }
  ],
  "tools": [...],
  "tool_choice": "auto",
  "parallel_tool_calls": false,
  "reasoning": {"effort": "xhigh", "summary": "auto"},
  "include": ["reasoning.encrypted_content"],
  "prompt_cache_key": "{uuid}"
}
```

### SSE 响应格式

```
event: response.created
data: {...}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"文本增量",...}

event: response.output_text.done
data: {"type":"response.output_text.done","text":"完整文本",...}

event: response.completed
data: {...usage统计...}
```

---

## 请求格式转换规则

1. 模型名 `gpt-5-*` → 统一为 `gpt-5`
2. 强制 `store=false`
3. 强制 `stream=true` (Codex API 只支持流式)
4. 同时存在 `temperature` 与 `top_p` 时移除 `top_p`
