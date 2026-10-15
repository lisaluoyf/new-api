# Seedance error responses

Seedance generation submissions, task queries, failed content downloads and
terminal webhooks expose a structured error with a stable code and a useful
message. Applies to Seedance 2.0, 2.5, 2.0-fast, 2.0-mini and supported aliases.

```json
{
  "error": {
    "code": "InputImageSensitiveContentDetected",
    "message": "The input image may contain sensitive information. Please replace it and try again."
  }
}
```

Submissions retain the existing top-level `code`, `message` and `data` fields
alongside `error`, so existing integrations can continue reading those fields.
HTTP status still describes the submission failure. Wrapped client errors from
providers that return HTTP 200 are translated to their actual 400/422/429 status.

For `/v1/videos/{task_id}`, read `error.code` and `error.message` when
`status=failed`. Compatibility task-query responses continue to use their
existing envelope: read `data.status` and `data.error`; an outer `code=success`
means the lookup succeeded, not that the video generation succeeded.
Failed webhook events use the same error inside `data.error`.

Official input/output sensitive-content codes retain their exact classification
and subcodes such as `.PolicyViolation` and `.PrivacyInformation`. Supported
parameter and quota error families also retain their codes. Messages are
filtered to remove credentials, private URLs, internal routing and material IDs.
Unknown or missing provider codes use a generic `task_failed` for generation
failures or `invalid_request` for rejected submissions; no specific review
reason is inferred from an ambiguous failure. Server/credential failures return
the generic `video_service_error`; unknown 429 errors use `rate_limit_exceeded`.

During recovery, tasks remain queued/processing without a terminal error.
If recovery material review times out, the final error uses
`asset_review_timeout`; an uncertain backup submission uses
`submission_outcome_unknown`. These failures never inherit a stale error from
the first route. Existing fallback eligibility and settlement remain unchanged.

Historical task queries can expose a saved structured error without rewriting
task, billing or audit records. Previously delivered webhook events remain
immutable and are not replayed by this change.
