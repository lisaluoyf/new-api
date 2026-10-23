# Kling V4 integration preflight — 2026-10-23

Status: implementation prepared in isolated feature worktrees; not released or enabled.

## Price source requiring confirmation

The requested Kling developer pricing page (`https://kling.ai/dev/pricing`) currently lists Kling 3.0 Turbo, Kling 3.0 and Kling 3.0 Omni in both its English and Chinese configuration. No Kling V4 entry was present in the fetched page.

APIMart's public Kling 4.0 model page labels these as its "Official" reference prices (USD/billable second):

| Variant | Supplier-labelled reference price |
|---|---:|
| 720P | 0.19625 |
| 1080P | 0.29375 |
| 720P-input | 0.1175 |
| 1080P-input | 0.17625 |

These are not independently verified Kling-published V4 prices. The user has been asked whether to adopt them or supply Kling's tariff. No production price option or channel has been changed. Configuration requires an explicit price file and provenance; unconfigured variants fail closed.

## Implementation

- APIMaster backend: `/home/roma/apimaster-kling-v4-backend-20261023`.
- RomaAPI: `/home/roma/romaapi-kling-v4-20261023`.
- APIMaster docs/configuration/acceptance scripts: `/home/roma/apimaster-kling-v4-20261023`.
- Dedicated `apimart-kling-v4` task adaptor in both gateways; public requests and results use the existing video task lifecycle.
- All five modes, 720p/1080p, image roles, mode-specific defaults and parameter validation.
- Input-video billing measures duration, sums before ceiling and caps at 30 seconds. Edit/reference-video quote: output + max(output,input). Extension charges only new seconds.
- Preserve channel/account coefficients, round the final quote once, and persist output/input/billable durations and tier in the log/accounting snapshot.
- Existing task pipeline supplies failure refunds and authenticated media delivery. Usage logs recognize V4 video results and use their video proxy.
- English and Chinese reference documents cover every documented parameter, examples, polling/download, billing and unsupported fields. All 15 locale sidebars/overview pages link to V4; other locales use the established English fallback.
- Public docs contain no upstream endpoint, channel name, credential or provider task identifier.

## Live upstream evidence

One explicitly authorized paid upstream smoke generation completed:

- Requested 3 seconds, 720p, text-to-video.
- Actual MP4: H.264, 1280×720, 3.041995 seconds; AAC stereo (2 channels).
- Full `ffmpeg` decode succeeded with no error output.
- Upstream terminal `cost`: USD 0.4239 (720p procurement USD 0.1413/requested second).
- Private raw upstream evidence is retained under `/var/tmp/kling-v4-20261023/` on APIMaster production; credentials were used only in memory.
- Documentation discrepancy: the supplied generation document advertises `GET /v1/videos/generations/{task_id}`; real service returns 404 and explicitly directs clients to `GET /v1/tasks/{task_id}`. Adaptor uses the measured working path. Public gateway polling remains `/v1/videos/{task_id}`.

## Verification and remaining release work

- Contract/adaptor tests cover all modes, fractional duration, input cap, malformed parameters, unconfigured-price rejection, full payload preservation and public-result proxying.
- Synthetic tier-accounting tests verify that customer charges and structured accounting agree, without asserting unconfirmed production prices.
- Frontend media-preview tests pass on both gateways.
- Documentation sidebar and document-internal links pass. Whole-site link validation separately identifies the pre-existing `zh/veo-4-api.md` link to `/docs/zh/api/videos/veo-3.1`; confirmed present in the baseline and left outside this change.
- Local full frontend typechecks exceeded available memory; release must pass CI typechecks/build before production publication. Initial Go controller linking exhausted `/tmp`; retries use a dedicated disk-backed `GOTMPDIR`.
- `scripts/kling_v4_channels_20261023.py` prepares explicit prices and disabled/isolated master channel through the admin API; Roma channel catalog must mirror master.
- `scripts/test_kling_v4_acceptance_20261023.py` is prepared for paid public-API acceptance on each gateway: five modes, 1080p, invalid requests/no charge, repeated polling/no extra charge, expected quote/log reconciliation, video decode/stereo, log media and test-token cleanup. It has not been run against the new gateway adapter because production is not released.
- After tariff confirmation: configure isolated pricing/channel, publish immutable tested revisions through the existing APIMaster and Roma release workflows, complete both live acceptance reports, enable public routing, verify public model listing/prices/docs, archive final evidence.
