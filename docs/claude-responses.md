# Claude Responses compatibility

Native Anthropic channels support `POST /v1/responses` through a stateless
Responses ↔ Messages bridge. Existing `/v1/messages` and `/v1/chat/completions`
remain supported. Body pass-through settings do not bypass protocol conversion.

Supported: string/message-array input, instructions and system/developer text,
image URL/base64 input, function tools (including strict schemas), parallel tool
controls, function results, JSON schema output, model-aware reasoning effort,
basic native web search with URL citations, and verbosity style hints,
JSON responses and incremental Responses SSE events. SSE has ordered
`sequence_number`, stable item/output/content indices, argument deltas and a
single completed/incomplete terminal response containing usage. `max_tokens`
becomes `status=incomplete`, with reason `max_output_tokens`.

```python
from openai import OpenAI

client = OpenAI(api_key="YOUR_API_KEY", base_url="https://apimaster.ai/v1")
response = client.responses.create(
    model="claude-opus-5-5",
    input="What is 17 * 23? Answer briefly.",
    max_output_tokens=2000,
    reasoning={"effort": "low"},
    store=False,
    include=["reasoning.encrypted_content"],
)
print(response.output_text)
```

For tool follow-ups, send the original input + all `response.output` items +
matching `function_call_output` items. Preserve reasoning items unchanged:
`encrypted_content` contains a namespaced `anthropic_v1:` envelope of the
provider's actual signed thinking/redacted-thinking block. It is opaque and is
only usable with this Claude bridge, not an OpenAI model. The bridge never
fabricates signatures or accepts OpenAI encrypted reasoning as Claude state.
A summary without that envelope is display-only. Web search also returns a
standard reasoning item with `anthropic_turn_v1:` opaque state containing the
original assistant turn, search results and encrypted citation references.
Copy this item unchanged along with the rest of the output when continuing.

Opus 5.5, Sonnet 5.5, Fable 5.1 and Mythos 5.1 reject forced tool selection;
use `tool_choice="auto"`. Claude's restrictions still apply to strict schemas.
The bridge preserves schemas and lets the provider validate its schema limits.

Unsupported features return HTTP 400 before calling the provider: stored or
background responses, previous_response_id/conversation state, compact,
built-in tools other than basic web search, MCP/custom tools, OpenAI file/audio input, unsupported include
expansions, prompt templates, automatic truncation, top_logprobs,
max_tool_calls, and OpenAI-specific prompt cache retention controls.
`web_search` and `web_search_preview` map to Claude's basic
`web_search_20250305` tool, preserving domain filters and approximate location.
OpenAI search-context-size is accepted as a hint; Claude controls the result
context. This does not enable Claude dynamic filtering/code execution.
`external_web_access=false` has no Claude equivalent and returns 400.
`text.verbosity=low/high` maps to a user-facing style instruction, medium is the
default style. `prompt_cache_key` enables native 5-minute automatic prefix
caching; the provider determines cache hits from the prefix rather than that
OpenAI routing key. `client_metadata` remains a client-side hint.

Use `store=False`, the full input history, and Messages-compatible
`cache_control` on input content blocks for explicit prompt caching.
The bridge does not provide Responses retrieval/deletion/cancellation APIs.

## Usage and billing

Public Responses usage follows inclusive input semantics:
`input_tokens = non-cache input + cache read + cache creation`.
Cache counters and 5-minute/1-hour creation splits are preserved. Reasoning
tokens are a breakdown of `output_tokens`, never an additional charge.

Internal settlement retains `usage_semantic=anthropic` and final upstream format
Claude. Existing frozen channel prices, group multipliers, cache TTL prices and
context tiers apply exactly as for native Messages. Web search fees are charged
once from provider `server_tool_use.web_search_requests`, using the configured
per-thousand price frozen before the upstream request. Merely declaring a search
tool incurs no fee. Native Claude stream search usage is tracked as well.
User accounting includes the actual settled search surcharge. Because token
procurement schedules do not supply a tool-call procurement rate, such records
explicitly mark `tool_procurement_price_unconfigured` and accounting status
`partial`; the channel-cost amount is the verified token subtotal, not a claim
of free search. No supplier fee is invented. No local token estimate is
substituted for missing terminal usage. Invalid/unfinished upstream responses do
not become successful billable Responses responses. A terminal stream remains
billable if the client disconnects immediately after completion; premature
cancellation follows the existing failure settlement policy.

Protocol references checked 2026-09-30:
- [OpenAI function calling](https://developers.openai.com/api/docs/guides/function-calling)
- [OpenAI Responses streaming](https://developers.openai.com/api/docs/guides/streaming-responses)
- [Claude thinking/tool workflows](https://platform.claude.com/docs/en/build-with-claude/thinking-tool-workflows)
- [Claude thinking restrictions](https://platform.claude.com/docs/en/build-with-claude/thinking)
- [Claude structured outputs](https://platform.claude.com/docs/en/build-with-claude/structured-outputs)
