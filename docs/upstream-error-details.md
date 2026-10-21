# Customer-visible upstream error details

Upstream failures use a shared `upstream_error` object on APIMaster and RomaAPI. It is independent of channel IDs and model allowlists. Adding another route using an existing adapter inherits the behavior. New adapters must use the shared request/error helpers and preserve typed errors rather than replace their cause with a string.

## Public contract

Existing `code`, `message`, HTTP status, retry, acceptance, refund and channel-health semantics remain unchanged. The additional object preserves the upstream response separately:

```json
{
  "upstream_error": {
    "status_code": 400,
    "request_id": "opaque-provider-request-id",
    "body": {
      "error": {
        "code": "InvalidParameter.UnsupportedImageFormat",
        "message": "The upstream's complete error message",
        "details": {"field": "image[0]"}
      }
    }
  }
}
```

Field locations:

- OpenAI/Claude-compatible API failures: `error.upstream_error`.
- Task submission failures: `upstream_error`; Seedance envelopes also include `error.upstream_error`.
- Failed asynchronous video/task queries and customer task lists: `error.upstream_error`.
- Durable submission receipts: `error.upstream_error`.
- Customer logs: `other.upstream_error` and the log's full detail text. RomaAPI's minimal token log endpoint exposes `upstream_error` without adding routing, account or procurement fields.

`status_code` is the observed upstream HTTP status, before public status mapping. It is omitted when only a stored asynchronous error or transport error is available. `request_id` is optional and comes only from response identifier headers. Body identifiers, details, nested error objects and unknown provider error codes remain available. The system does not infer a media file's actual format or an underlying timeout when the upstream did not supply that information.

## Redaction and limits

The common sanitizer removes credentials, authorization/cookie headers, request payloads, prompts, private URLs, local paths and account/channel metadata. Error text and structured details are preserved after redaction. This is an error diagnostic, not an unfiltered dump of upstream requests or responses.

Diagnostic serialization has a conservative byte budget within 48 KiB; oversized/deep payloads explicitly set `truncated: true`. Capturing responses does not buffer successful streams. Non-streaming task submissions also capture HTTP 200 envelopes so an embedded error or malformed task result is observable.

## Persistence and historical data

The nullable/empty `video_submissions.error_details` TEXT column retains the same sanitized object for idempotent receipt queries. Recording it never changes acceptance state or financial records. Local/material rejections produce an error log independently of channel-health attribution. Successfully recorded attempt errors are not logged a second time by the finalizer.

Owned historical asynchronous task logs can read failure details from existing task evidence without rewriting billing rows. Historical submission errors whose original response was never stored cannot be reconstructed. No synthetic upstream reason is added and no historical financial/acceptance state is changed.

## Regression coverage

Tests cover unknown future model/channel/provider identifiers, long error messages, nested redaction, explicit truncation, original response preservation, successful stream behavior, HTTP 200 task errors, API/receipt/log consistency, duplicate suppression and account/token ownership.
