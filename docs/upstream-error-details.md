# Customer-visible upstream error details

Upstream failures use a shared `upstream_error` object on APIMaster and RomaAPI. It is independent of channel IDs and model allowlists. Adding another route using an existing adapter inherits the behavior. New adapters must use the shared request/error helpers and preserve typed errors rather than replace their cause with a string.

## Public contract

Public code/message views redact routing identities and vendor namespaces. HTTP status mapping, internal error codes/messages, retry, acceptance, refund and channel-health semantics remain unchanged. The additional object preserves the upstream response separately:

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

`status_code` is the observed upstream HTTP status, before public status mapping. It is omitted when only a stored asynchronous error or transport error is available. `request_id` is optional and comes only from response identifier headers. Details, nested failure objects and actionable leaf codes remain available. Vendor namespaces are removed; neutral functional families such as `InvalidParameter.UnsupportedImageFormat` remain intact. A response identifier containing an identity or other private value is omitted. The system does not infer a media file's actual format or an underlying timeout when the upstream did not supply that information.

## Redaction and limits

The common sanitizer removes credentials, authorization/cookie headers, request payloads, prompts, private URLs and bare domains, local paths and account/channel metadata. It also hides configured routing names, internal deployment/model identifiers and provider/vendor/supplier identity fields. Aliases come from channel configuration, the actual upstream request host and response identity metadata; adding another configured channel does not require maintaining a vendor blacklist. Error text and structured details are preserved after redaction. This is an error diagnostic, not an unfiltered dump of upstream requests or responses.

Diagnostic serialization has a conservative byte budget within 48 KiB; oversized/deep payloads explicitly set `truncated: true`. Capturing responses does not buffer successful streams. Non-streaming task submissions also capture HTTP 200 envelopes so an embedded error or malformed task result is observable.

## Persistence and historical data

The nullable/empty `video_submissions.error_details` TEXT column retains the same sanitized object for idempotent receipt queries. Recording it never changes acceptance state or financial records. Local/material rejections produce an error log independently of channel-health attribution. Successfully recorded attempt errors are not logged a second time by the finalizer.

Owned historical asynchronous task logs can read failure details from existing task evidence without rewriting billing rows. Historical submission errors whose original response was never stored cannot be reconstructed. No synthetic upstream reason is added and no historical financial/acceptance state is changed.

## Regression coverage

Tests cover unknown future model/channel/provider identifiers, long error messages, nested redaction, explicit truncation, original response preservation, successful stream behavior, HTTP 200 task errors, API/receipt/log consistency, duplicate suppression and account/token ownership.

## Identity privacy regression coverage

Public copies cover legacy code/message/metadata fields, structured diagnostics, receipts and historical customer logs. The original error object and raw typed conversion remain available for internal retry, violation-fee and health classification. Customer video task platform labels use a generic video label instead of a private adapter name.

Fixtures test mixed-case supplier names, two-letter channel aliases, unconfigured provider names declared by response metadata, bare domains, internal model names, vendor-prefixed error codes, embedded JSON and provider-bearing request IDs. The API/receipt/log integration fixture also verifies that names are absent while the actual input format failure and parameter index remain readable. Customer-owned request model fields and media links are preserved by the log read policy.

Oversized public envelopes are decoded into fresh DTOs so fields omitted by the byte budget cannot retain unsanitized source values. Truncation is explicitly propagated to the public diagnostic, including the oversized-envelope fallback. Generic protocol keys such as upstream_error remain intact even when they coincide with a routing alias; proprietary identity-bearing keys are redacted.


## Streaming and SDK failures (2026-10-21 follow-up)

Started SSE responses report a terminal protocol error with the same sanitized `upstream_error` used for customer logs. Claude/Gemini Responses conversions preserve details on `response.failed`; duplicate terminal writes and writes after client cancellation are suppressed. Shared SSE writers also sanitize error frames forwarded by adapters, while ordinary business frames remain byte-for-byte unchanged. Stream capture inspects individual error frames only and never accumulates successful content.

SDK HTTP clients use a cloned transport wrapper to observe error bodies before SDK deserialization. Bedrock invocation and event-stream errors retain typed error codes/messages, available request IDs and modeled original-message/status fields. The actual HTTP handshake status stays distinct from a modeled original status. Retries clear prior attempt evidence; SDK credentials and identities are redacted in API/event/log views. RomaAPI now checks SDK event-stream errors rather than emitting a successful completion after an unexpected SDK exception; already completed streams retain their prior completion behavior.
