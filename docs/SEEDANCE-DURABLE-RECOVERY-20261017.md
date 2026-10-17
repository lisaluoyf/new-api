# SD submission lifecycle and original-task recovery — 2026-10-17

Internal engineering report. This file is not public API documentation.

## Scope and capability audit

The shared submission path covers Seedance 2.0, Fast, Mini, 2.5 and the supported
`doubao-seedance-2.0` alias, across configured supplier adapters. Ten production
channel configurations were inventoried; six were enabled at audit time.

Read-only calls to documented task-list routes returned HTTP 200 for channel
272 and HTTP 405 for channel 292. The latter remains unverified despite its
published documentation. Existing task-ID polling is already implemented for
the other enabled adapters. A task list is not a lookup by idempotency key.
Forwarding an idempotency header is not evidence of provider deduplication.
No new provider endpoint is required or assumed. No generation was submitted
as part of this audit or production verification.

## Changes

- Every supported SD generation request gets a durable local submission receipt,
  including clients that omit `Idempotency-Key`. An internally generated claim
  protects the lifecycle of that one request; it does **not** deduplicate two
  separate client requests. Clients still need their own stable key for replay.
- Before crossing the paid generation send boundary, freeze the original public
  task ID, model/channel, original credential fingerprint, task polling context,
  logical inputs, callback configuration, pricing and billing receipt. Failures
  to save this snapshot fail before generation dispatch. The snapshot contains
  no duplicate supplier API key and is excluded from public JSON.
- Observe the one original SD POST for up to 90 seconds even after client
  disconnect, subject to a shorter configured client timeout. This also covers
  legacy paths that lack a durable claim. Already-canceled requests are not sent;
  paid POST redirects and transport rewind/replay remain disabled.
- Save the accepted upstream ID and response, then explicitly checkpoint local
  financial settlement. Only after confirmed acceptance **and** that checkpoint
  may the worker restore a missing original task and pending billing receipt.
  Restore preserves frozen prices and inputs, never performs a generation POST,
  never replays billing or consumption logging, and uses only the matching
  supplier credential. Removed credentials require manual reconciliation.
- The ready marker, task and receipt are committed together. A submission row
  lock serializes the worker and late controller; repeats cannot duplicate tasks.
  Recovery snapshots are cleared after task persistence succeeds.
- Expired pre-send claims become `not_accepted`; expired post-send claims become
  `acceptance_unknown`, without a resend. Deferred candidates rotate through the
  bounded worker batch, so one blocked row cannot permanently starve others.
- Any matching billing hold, including a refunded hold, blocks automatic creation
  of a billable polling task. Existing refund policy is unchanged. Uncertain
  acceptance receipts remain independently available after customer refund.

## Deliberate limits

A crash after supplier acceptance but before the upstream ID is saved can still
leave acceptance unknown when no authoritative key lookup or correlated callback
exists. A crash between financial settlement and its durable checkpoint is also
held for financial review rather than guessed/recharged. Historical unknown
requests lacking snapshots are not reconstructed from prompt/time similarity.
The earlier incident remains unresolved automatically; this change neither binds
its candidate supplier UUID nor claws back the customer's completed refund.

This release does not invent generic supplier callbacks, change explicit business
cancellation semantics, or change the existing 24-hour task timeout policy. Once
persisted, existing task workers poll and synchronize the original task normally.

## Verification

Tests cover all four public SD models across six configured channel identities,
frozen prices and original IDs, missing local task recovery, repeated scans,
concurrent controller/worker insertion, uncertain billing, refunded/pending holds,
credential rotation, identity/receipt conflicts, lease expiration, keyless requests,
legacy disconnect handling, paid POST redirect blocking and caller cancellation.
SQLite and PostgreSQL recovery tests, race tests, full `go test ./...`,
`go vet ./...`, and transport fault tests passed locally before rollout.
Production checks use read-only health, image revision, schema and task evidence.
