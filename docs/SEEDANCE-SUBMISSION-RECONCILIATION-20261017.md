# Seedance submission reconciliation, 2026-10-17

## Customer incident

Original public task: `task_N57SnCyWv6XP2A9mzB7WUtilrz0QCgtE`.
Request: `202610171404036205585148268d9d6VlfQJCgs`, 2026-10-17 14:04:03 UTC.

The client connection canceled after approximately 15 seconds. Gateway logs show
`POST /v1/videos/generations` ending with a local 500; the reported client-facing
503 may come from the intervening relay. The upstream POST ended with
`context canceled` without a response or task ID. This is **not** rejection proof.
The original upstream key was
`apimaster-task_N57SnCyWv6XP2A9mzB7WUtilrz0QCgtE`.

Read-only checks found one `acceptance_unknown` receipt on channel 273, no local
task, no Seedance billing receipt, and billing hold 110617 for 1,460,262 quota
units ($2.920524). At investigation time the hold was pending, not confirmed
consumption. No generation was resubmitted and no replacement task was created.
The upstream's published task lookup requires its task UUID; the public API has
no documented read-only idempotency-key lookup. Upstream acceptance and supplier
charges therefore require an upstream lookup by the original key. Missing local
records, timeout, 404, or a customer-safe refund must not be reported as rejection.

## Changes

- After a durable Seedance submission crosses its send boundary, client
  cancellation no longer discards the original HTTP response. The one original
  POST is observed for up to 90 seconds, subject to a shorter configured HTTP
  client timeout. Closing its response releases the context. A request canceled
  before dispatch does not start a new upstream request. Redirect and transport
  replay protections remain enabled. No retry POST is added.
- Receipt lookup reconciles existing original task and billing receipt evidence.
  A master worker also scans recoverable receipts each minute. User, model,
  channel, public task ID, original upstream ID (if recorded), and billing
  receipt identity must agree. Duplicate or incomplete evidence stays unresolved.
- Reconciliation updates only the acceptance receipt to `accepted` and
  `task_ready=true`. It never creates a task, reserves funds, changes billing,
  calls a generation endpoint, or converts uncertainty to `not_accepted`.
- The normal task worker continues querying results and settling existing bills.

## Limits

This prevents loss of an acceptance response on client disconnect and repairs
receipts when the original task and bill were already persisted. It does not
reconstruct an upstream task from an opaque timeout or recover an accepted
response lost in a process crash before task persistence. Those cases require
an authoritative upstream lookup and the original frozen billing context; this
patch does not claim otherwise. Historical unknown receipts stay retry-unsafe.
Existing billing-hold refund policy is unchanged. A refund does not establish
upstream non-acceptance or permit reusing a generation key.

## Validation

Transport tests cancel the caller after the server receives the original POST,
then verify that its delayed response body remains readable and only one POST
occurred. They also cover pre-dispatch cancellation and redirects. Model tests
cover lookup/background repair, repeated scans, original billing amounts,
missing task/bill, and conflicting user/channel/model/upstream/receipt identity.

Verified locally: focused tests across model, service, relay, channel, middleware
and controller packages; `go vet` on affected packages; PostgreSQL submission
and reconciliation tests; race tests for reconciliation, cancellation and redirect
protection. All passed. At 14:31:05 UTC the original hold remained pending;
acceptance was unknown and no local consumption/refund log was recorded for the
original request. This report is internal, not customer-facing API documentation.
