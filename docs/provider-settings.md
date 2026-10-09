# User provider settings

The console's **Provider setting / 渠道配置** page stores account-wide, model-specific channel restrictions. Each model has at most one rule, with an enabled switch and a non-empty channel ID list:

- `exclude`: remove selected channels from the original eligible pool.
- `include`: intersect the original eligible pool with selected channels.

Disabled or absent rules retain automatic routing. Channel status, model/group access, client compatibility, price ordering, health/feedback, retry limits and billing are not overridden. Retries, official fallback and synchronous/asynchronous image racing must stay within the same allowed pool. Exhaustion returns the existing no-channel error; it never widens the pool.

Rules apply to all API keys belonging to the account. New rules default to disabled. FreeModel retains its isolated routing plan and is not configurable here. Polling previously submitted tasks continues on the submitting channel; changing a rule does not cancel existing work.

Playground uses the authenticated account's settings before selecting its first channel. New video remixes and Midjourney continuations validate the origin channel before reusing it; if that channel is forbidden, submission is rejected rather than bypassing the rule. Models enabled in the user's usable groups remain configurable even when hidden from the public marketplace; marketplace visibility and routing access are independent.

## Authenticated user API

- `GET /api/user/provider-settings`: returns `data.rules` and the model dropdown's `data.models`.
- `PUT /api/user/provider-settings`: replaces the current user's rules with `{ "rules": [{ "enabled": true, "model": "gpt-5.4", "mode": "exclude", "channel_ids": [38, 224] }] }`. An empty array clears all rules. User IDs supplied in the payload are not used.
- `GET /api/user/provider-settings/channels?model=...`: returns the sanitized channel/price catalog for a configurable model, without names, credentials, procurement pricing or upstream URLs.

At most 128 model rules and 512 IDs per rule are accepted. Channel IDs are positive, sorted and deduplicated. New selections must come from the model's current channel catalog; previously saved selections are retained when a channel becomes unavailable, so users can disable, remove or revise stale rules. Saving never enables a channel.

Rules live in the existing `users.setting` JSON and use its existing user cache invalidation. No schema migration is required. Each request uses its authenticated settings snapshot; background image hedging captures the same model rule instead of reading a recycled Gin context. Restricted cheapest selections bypass the shared, unscoped first-choice cache.
