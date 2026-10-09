# Seedance resolutions and delivery verification

The admin **Channel Data** page adds a rightmost **Resolution** multi-select for every Seedance model. Seedance 2.5 offers `480P`, `480P-input`, `720P`, `720P-input`, `1080P`, `1080P-input`; Seedance 2.0 also offers `4K` and `4K-input`; fast/mini use their available 480P/720P tariffs. `-input` means reference **video**, not image. Draft upgrades inherit the original reference-video inputs and use the normalized upgrade resolution.

Selections are saved with `PUT /api/admin/channel-data/resolutions` using `{ "channel_id": 273, "model": "seedance-2.5", "resolutions": ["480P", "720P-input"] }`. Missing configuration enables all supported variants for backwards compatibility. An explicit empty array excludes the route for that model. The selection is independent of price configuration and takes effect on the next request across all instances without waiting for cache expiry. Requests snapshot eligibility for consistent retries. Capability restrictions apply before priority selection, combine with account provider settings, and apply to cheapest routing, retries and affinity. Explicitly pinned channels, media-library accounts and origin-task routes reject disabled variants rather than silently switching accounts.

## API

`POST https://apimaster.ai/v1/videos/seedance-verify`, authenticated using a normal API key, reuses the exact detector behind `/video/seedance-verify`. It does not submit a generation or debit generation quota. Existing token authentication and rate limits apply.

JSON URL mode (HTTPS public video URLs only):

```sh
curl https://apimaster.ai/v1/videos/seedance-verify \
  -H "Authorization: Bearer $APIMASTER_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"seedance-2.5","video_url":"https://your-public-cdn.example/video.mp4"}'
```

Upload mode:

```sh
curl https://apimaster.ai/v1/videos/seedance-verify \
  -H "Authorization: Bearer $APIMASTER_API_KEY" \
  -F model=seedance-2.5 -F file=@video.mp4
```

The response contains `model`, `status` (`pass`, `suspicious`, `notcomplete`), `reason`, `detected_at` (UTC Unix seconds) and the original detector `result`. HTTP 200 means analysis completed; only `status=pass` means verification passed. Invalid requests return 400/413 and unavailable downloads/detector return 502. Maximum file size: 500 MiB; total timeout: 180 seconds. Uploaded media and downloads are private temporary files removed after use.

## Every delivered video

Every successful Seedance 2.0 / 2.5 / 2.0-fast / 2.0-mini task inserts a unique `pending` row in `video_verification_runs` **in the same transaction** as its completion and webhook event. Immediate successful inserts and guarded bulk completions use the same hook. There is no cooldown or lossy process-local queue. The indexed outbox survives restarts and Redis outages; no customer task table is scanned. An outbox write failure rolls back completion so the normal task poller can retry the transaction without losing the verification.

Only the singleton master worker consumes the outbox, polling every two seconds. A global Redis lease limits downloads/detection to one at a time during blue/green overlap when Redis is available. Persisted route/run leases and unique task IDs prevent duplicate completed runs, and expired processing runs are retried. Pre-upgrade Redis stream events are imported into the outbox, preserving jobs during deployment.

Each delivered upstream video is downloaded safely and streamed to `POST /api/video/seedance-verify` at `APIMASTER_FLASK_URL`. Lease: 300 seconds. HTTPS, public DNS/IP pinning, redirect validation and cross-host credential stripping protect downloads. Credentials come from the task snapshot. Configured proxies or inaccessible media produce a failed/inconclusive check and an alert.

Only complete evidence with a matching version, unexpired baseline and five unique passing checks (`claim`, `dimensions`, `family`, `x264`, `frames`) adds a green point to the corresponding model/channel fingerprint history. Fast/mini are checked against the SD2.0 delivery pipeline: this verifies the version family, and does not prove the exact fast/mini variant. No pass evidence is manufactured. Public history exposes only safe check values and timestamps, and can lag by the existing 120-second cache.

Every non-pass outcome, including unknown fingerprints, missing evidence, expired baselines, download failures or detector timeouts, creates a durable Feishu alert. The model-detection group receives the model name, channel name and ID, failure reason, and UTC detection time. The independent alert worker runs once per minute and retries after 1, 5 and 15 minutes; exhausted alerts remain for audit. Detection results do not change delivered videos, generation charges, channel enablement or routing.

Migrations add `seedance_channel_resolutions`; existing verification state/run/alert tables remain compatible, with an indexed lease field for recovery. Internal history and failures remain auditable through `channel_detect_logs`, the admin history and `video_verification_alerts`.

Validation includes resolution/reference routing, priority fallback with and without memory cache, task transaction rollback and deduplication, every-video execution, technical failure alerts and delivery retries, safe downloads, and upload API reuse of the page detector. Production follows child commit/push → GHCR build → parent pointer commit/push → `go-live.sh` → live acceptance.
