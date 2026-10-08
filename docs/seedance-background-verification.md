# Seedance delivery verification

Successful Seedance 2.0 / 2.5 task commits notify a bounded, nonblocking local queue. A separate publisher writes only the task database ID and completion-event timestamp to `apimaster:video-verification:v1` in Redis. No network call runs inside the user request or the task-completion transaction. Redis/queue failures may lose an internal verification event; they never fail the user task. No user task table is scanned.

Only the singleton master worker consumes the stream. Idle reads block on Redis. A global Redis lease limits downloads/detection to one at a time, including blue/green overlap. Persisted channel/model state and unique task runs prevent duplicate completed runs across restarts. Expired interrupted leases can be recovered from pending stream events.

`VideoVerificationInterval` in `service/video_verification.go` is the sole cooldown configuration, initially ten minutes. All completed outcomes start the cooldown. Events completed during that window are skipped even if consumed later; after expiry, only a new successful completion event can trigger verification. `doubao-seedance-2.0` normalizes to `seedance-2.0`; fast/mini/pro variants are not covered.

The delivered upstream video is downloaded to a private temporary file, then streamed to `POST /api/video/seedance-verify` at `APIMASTER_FLASK_URL`. Total detection timeout: 180 seconds; lease: 300 seconds; download maximum: 500 MiB. Files are removed after the run. HTTPS, public DNS/IP pinning, redirect validation and cross-host credential stripping protect downloads. Channels requiring a configured proxy, non-public URLs or unsupported video access are logged as inconclusive rather than weakening these safeguards. Provider credentials come from the task snapshot, never the current channel key.

Public marketplace history includes a green video point only when the detector identifies the claimed version, returns `match`, reports an unexpired baseline and passes every unique check: `claim`, `dimensions`, `family`, `x264`, `frames`. Video history never uses text fingerprint boosting or silently manufactured pass results. Public fields are an explicit allowlist: timestamp, status, kind and five check values. Task IDs, video URLs, channel keys, detector notes and expected/internal metadata are not exposed. Existing public caching can delay visibility by up to 120 seconds.

Only an explicit mismatch between known SD2.0/SD2.5 pipelines or a failed known fingerprint emits a durable alert to the model-detection Feishu group. Unknown containers/parameters, missing evidence, warn/skip, unsupported encodings, download failures, timeouts and stale baselines remain internal `notcomplete` results. The independent alert worker queries only its indexed alert table once per minute and retries failures after 1, 5 and 15 minutes. Exhausted alerts remain in `video_verification_alerts` for audit. This never changes user delivery, billing, refunds, webhooks, channel status, priority or routing; it never generates extra videos.

Database migrations add `video_verification_states`, `video_verification_runs`, `video_verification_alerts` and the optional `channel_detect_logs.video_checks_json` field. Internal results can be audited through `channel_detect_logs` and the admin model-data history; public results remain pass-only.

Validation:

```sh
go test ./model ./service ./controller -run 'VideoVerification|ClassifyVerifiedVideo|PublicVideoFingerprint|NormalizeVerifiedVideo|TaskWebhook|PublicMarketplaceItem' -count=1
```

The frontend shares a strict five-check sanitizer and localized hover/focus/touch tooltips with timestamps. Its TypeScript, ESLint and marketplace/unit suites run before deployment. Production follows child-repository commit/push → GHCR build → parent submodule/frontend commit/push → `go-live.sh` → revision and worker/schema verification.
