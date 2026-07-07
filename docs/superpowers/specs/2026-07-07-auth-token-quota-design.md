# Auth Token Quota Design

## Goal

Support dollar-denominated quota limits for ccLoad API access tokens so shared tokens can be capped and automatically paused after they spend their budget.

## Scope

- Applies to service-issued API access tokens managed on `/web/tokens.html` (`sk-ccl-...`).
- Does not apply to upstream channel API keys.
- Quotas are expressed in USD and use existing token cost accounting (`total_cost_usd`).
- Existing tokens and newly created tokens default to unlimited quota.
- A token quota can be unlimited or any non-negative numeric dollar amount.
- When successful request cost pushes cumulative usage to or above the quota, the token is automatically disabled.
- Expiry presets add `1 day` and `7 days`.

## Data Model

Add `auth_tokens.quota_limit_usd`:

- `NULL` means unlimited quota.
- Non-null values must be `>= 0`.
- Existing rows migrate to `NULL`.
- New rows default to `NULL`.

Expose the field as `quota_limit_usd` in `model.AuthToken`.

## Backend Behavior

Creation and update endpoints accept `quota_limit_usd`:

- Missing or `null` means unlimited.
- Any finite value `>= 0` is accepted.
- Negative, NaN, or infinite values are rejected with HTTP 400.

`UpdateTokenStats` remains the accounting source of truth. After a successful request increments `total_cost_usd`, if `quota_limit_usd` is non-null and the new total is greater than or equal to the quota, the same database update sets `is_active = 0`. The auth token cache is reloaded after an automatic pause so subsequent requests are rejected immediately.

The request that reaches the quota is allowed to complete because actual output cost is only known at completion time.

## Frontend Behavior

The token drawer gets a quota selector:

- Unlimited by default.
- Custom USD numeric input with `min=0` and decimal support.
- Edit mode preloads existing quota.

The token list shows quota beside cost:

- Unlimited tokens show "无限".
- Limited tokens show `used / limit`.
- If the token is auto-paused, the existing disabled state and enable action are used. The user can raise the quota and re-enable.

Expiry preset additions:

- `1天后过期`
- `7天后过期`

## Testing

Add backend tests for:

- New token defaults to unlimited quota.
- Creating or updating with a finite non-negative quota persists the value.
- Negative quota is rejected.
- Updating token stats disables a limited token when usage reaches the quota.
- Unlimited tokens remain active after usage updates.

Run `go test -tags go_json ./internal/... -v` after implementation.
