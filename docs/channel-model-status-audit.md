# Channel Model Status Audit

Model enable/disable operations now write the routing ability, current metadata,
and a `channel_model_events` row in one transaction. A channel row lock serializes
metadata updates across the worker and traffic replicas. Probe results update
fresh metadata and must match the disable event they originally tested.

Manual operations record the authenticated administrator ID. Automatic events
record `health_probe`, `recovery_probe`, or `fingerprint_recovery` as the source.
Enabling a model removes its current automatic-disable metadata but retains all
events. Failure to save an event rolls back the routing change.

The channel-data API exposes `status_source`: `enabled`, `manual`, `auto`,
`channel_manual`, `channel_auto`, or `unknown`. The history button uses the
admin-only `GET /api/admin/channel-data/status-history?channel_id=214&model=gpt-5.6-sol`
endpoint, with `before_id` pagination (50 rows per page).

## Existing Missing Reasons

No historical events are fabricated during migration. Disabled abilities without
manual/automatic metadata display "Historical disable reason missing" and remain
disabled when channels or abilities are rebuilt. Their automatic recovery is not
silently enabled; an operator must verify them before deciding to enable them.

On 2026-09-08, channels 214 and 219 had disabled `gpt-5.6-sol` abilities without
matching metadata. Retained logs contain a disable trigger for channel 214 at
05:51:24 UTC: HTTP 502/503, 10 failures out of 27 recent requests, and a failed
HTTP 502 confirmation probe. This trigger is a historical clue, not a committed
audit event. No conclusive trigger for channel 219 was found in retained logs.

## Deployment

Use the normal workspace push, GHCR build, and production `go-live` workflow.
The singleton worker migrates the new event table before the candidate traffic
container starts. Migration does not change existing channel/model states.

Verification: Go transaction/recovery/history tests, frontend typecheck and build,
then production status API, admin history endpoint, and route smoke checks.
