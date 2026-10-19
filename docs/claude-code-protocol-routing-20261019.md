# Claude Code channel protocol boundary

A POST to `/v1/chat/completions` carrying a `claude-cli/` User-Agent was classified as Claude Code and could select a CLI-only channel. In the investigated case the provider returned 403 and the gateway fell back to a compatible channel, adding avoidable failures and latency. A client identity header alone does not establish that the request uses the native protocol.

Claude Code classification now requires POST `/v1/messages`, followed by the existing User-Agent or beta-header detection. Chat, Responses, playground, count-token and lookalike paths remain outside this classification, so initial selection, fallback and explicit/affinity validation exclude CLI-only routes. Native Claude Code Messages requests retain access to both compatible general channels and CLI-only channels. Existing Codex and Gemini classification is unchanged.

Regression tests reproduce the former failure and cover both identity header forms, query parameters, invalid paths/methods, aliases, repeated selection, explicit validation and absent request contexts. Full Go tests and relevant vet checks are required before deployment.

This fix addresses a confirmed routing defect. It does not establish the cause of every cache miss or guarantee cache reuse. No cache usage, TTL, prices, historical charges or refunds are rewritten. Supplier-internal account routing and exact historical request-prefix identity remain unverified.
