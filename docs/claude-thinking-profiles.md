# Claude thinking compatibility profiles

Verified against Anthropic's [thinking table](https://platform.claude.com/docs/en/build-with-claude/thinking)
and [effort reference](https://platform.claude.com/docs/en/build-with-claude/effort) on 2026-09-29.

The OpenAI-to-Anthropic converter uses a shared capability registry instead of
generating manual thinking budgets for every Claude model. It applies to both
streaming and nonstreaming requests through that converter. Native Messages and
channels that bypass conversion retain their upstream protocol behavior.

| Profile | Built-in models | Behavior without reasoning parameters | Lowest thinking mode |
| --- | --- | --- | --- |
| `adaptive_opt_in` | Opus 4.7, 4.8 | Leave thinking absent (off) | `disabled` |
| `adaptive_default_on` | Sonnet 5 | Adaptive, summarized | `disabled` |
| `adaptive_default_on_high` | Opus 5 | Adaptive, summarized | `disabled`, only at low/medium/high effort |
| `adaptive_required` | Opus 5.5; Fable 5 / 5.1; Mythos 5 / 5.1 | Adaptive, summarized | Cannot disable |
| `adaptive_between_tools` | Sonnet 5.5 | Adaptive, summarized | Explicit `between_tools`, only at low/medium/high effort |
| `legacy` | Explicit administrator override only | Previous converter | Previous converter |

All adaptive profiles support `low`, `medium`, `high`, `xhigh`, `max`. An explicit
`reasoning_effort` is forwarded under `output_config.effort`, never converted to a
token budget. With no requested effort, the converter does not send one: the
upstream keeps its own default (medium for Opus 5.5, high for the other listed models).
Priority is `reasoning_effort` > `reasoning.effort` > model effort suffix.

The compatibility surface requests `display=summarized`. Explicit
`thinking.display` is respected; `reasoning.exclude=true` requests `omitted`.
An upstream may still return no summary. The gateway cannot manufacture one.

Explicit manual `budget_tokens`, `reasoning.max_tokens`, unsupported disabling,
and conflicting settings return a readable HTTP 400 before an upstream call.
Sonnet 5.5's `between_tools` accepts only its `type` field, cannot combine with
`xhigh`/`max`, and cannot satisfy `reasoning.exclude=true` because its progress
updates remain visible. It is deliberately not treated as full thinking disable.

Custom sampling fields (`temperature`, `top_p`, `top_k`) are omitted by these
OpenAI compatibility profiles because these models reject custom sampling.
Native Messages requests are not rewritten. `max_tokens` remains the client's
hard output limit and is not inflated to accommodate a synthetic thinking budget.

## Adding a future model without a gateway release

After verifying that a model matches one of these profiles, use the existing
root-admin options API (`PUT /api/option/`) to save a JSON-string map:

```json
{
  "key": "claude.thinking_model_profiles",
  "value": "{\"claude-example-future\":\"adaptive_between_tools\"}"
}
```

The example model is a placeholder, not an available model. Merge any existing
overrides before saving: the map is replaced as a whole. Built-in entries remain
available and can be overridden with an exact ID; `legacy` opts a model back into
the previous conversion behavior. Invalid profile names fail closed on use.
Normal option synchronization propagates changes to serving instances.

Matching recognizes an exact registered ID, its effort or `-thinking` aliases,
and valid `-YYYYMMDD` snapshots. It does not guess that a larger version number
has the same capabilities (`claude-sonnet-5-50` is not Sonnet 5.5). Unknown models
retain the legacy path until registered. Previously supported older models such
as Opus/Sonnet 4.6 and Sonnet/Haiku 4.5 keep their existing behavior.

This mechanism covers future models with the same rules; genuinely new rules
(different effort values, a new thinking type, etc.) still require a new profile
implementation and regression tests. It is not automatic capability discovery.
