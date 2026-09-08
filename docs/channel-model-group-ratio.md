# Per-model channel Group Ratio

Channel settings accept `model_group_ratios`, a JSON object with exact
client-facing model names and positive finite numeric multipliers:

```json
{
  "manual_group_ratio": 0.5,
  "model_group_ratios": {
    "gpt-5.6-sol": 0.2,
    "deepseek-v3.2": 1
  }
}
```

The model override replaces the channel default; it does not multiply it.
Unlisted models retain existing default/upstream behavior. A model override
also works without a channel default, including manual official-price fallback.
Deleting an override restores the original default/upstream price.

Pricing snapshots continue to store the channel default/upstream price and
Group Ratio. Overrides are applied at read time as `price / stored_ratio *
model_ratio`, across input, output, cache read and cache write prices. They
are never persisted into shared upstream model rows. Model mapping selects
the upstream price first; the original client-facing model selects the
override. Two models mapping to the same upstream can have different ratios.

The same resolution is used by channel data, the marketplace, channel price
selection, channel-based billing and procurement/consume-log unit prices.
Existing separate FreeModel routing and platform-fixed video billing rules
remain separate from channel Group Ratio pricing. Historical bills are not
rewritten.

Editing a channel invalidates route and marketplace caches; completion of
the asynchronous upstream price refresh invalidates them again. A marketplace
request started before invalidation cannot repopulate the cache afterward.

The channel form keeps rows in form state, validates missing models, duplicate
models and non-positive ratios, and serializes only after successful validation.
The API validates the settings object independently. Existing configurations
without `model_group_ratios` need no migration.
