# Seedance error audit and repair — 2026-10-19

Audit window: 2026-10-18 in UTC+8. Production baseline: `a90332ebbe9a307b00dec127684ac2e220e252d4`.

206 accepted tasks finished as 147 successes and 59 failures. Failure reasons: 33 sensitive-content rejections, 19 copyright rejections, 3 invalid image aspect ratios, 1 invalid image width, 1 invalid media input and 2 provider-side material synchronization timeouts. All 59 failed tasks have matching refund log entries. These are task records, not a reconstruction of every wallet transaction.

Another 29 submission receipts were not accepted and 9 had uncertain acceptance. Retained provider logs prove 24 image-role parameter rejections, 2 insufficient-balance rejections, 4 input copyright rejections wrapped as HTTP 502 and 1 material-certification rejection (409). Seven historical receipts lack enough retained diagnostic evidence; three of them have the same two-image `image_urls` shape as the confirmed role failures. Do not infer their exact cause, replay them, or alter their acceptance state based on this resemblance. Seven uncertain VideoFee submissions have refunded billing holds.

## Repairs

- Default ordinary Ark-compatible reference images to `reference_image`, including native `content`, while preserving explicit roles and Tencent first/last-frame behavior. Public `image_urls` is documented as reference input and must not reach Ark without a role.
- Expose stable, sanitized material-certification and wrapped content-policy errors. Keep the upstream HTTP status so the existing financial handling is unchanged. A recognized certification rejection with no task identity confirms non-acceptance; 5xx responses and any task identity remain uncertain.
- Preflight unique raw images on VideoFee channel 273 before pre-consumption: both dimensions 300–6000px, aspect ratio 0.4–2.5, image data under 30 MiB. Reuse dimensions within the request, retain configured fetch/redirect restrictions and optional image worker, and bound the combined fetch phase to 20 seconds. Approved `asset://` references use their existing material-library validation.
- Persist private bounded error code, sanitized message, HTTP status, provider error reference and time on durable submission receipts. Observe middleware errors as well as relay errors. Replays expose the safe error without another generation POST. Recording an error is owner-checked and never changes an accepted original or marks an uncertain generation safe to resubmit.

The migration adds five nullable diagnostic columns to `video_submissions`; it uses the existing GORM migration path and types supported by SQLite, PostgreSQL and MySQL. No historical records are rewritten by this release. Content-review decisions and provider-side timeouts remain actual upstream outcomes.

## Validation

Full `go test -p 2 ./...` and `go vet -p 2 ./...` passed. Go embedding directories were populated with local test-only HTML fixtures; no frontend source changed or production frontend build is claimed. Regression coverage includes explicit image roles, reference-video pricing selection, Tencent frames, durable concurrent replays, owner checks, accepted-state monotonicity, private error redaction, local rejection replay, dimension checks and bounded cached media fetches. No paid video generation was issued for this repair.
