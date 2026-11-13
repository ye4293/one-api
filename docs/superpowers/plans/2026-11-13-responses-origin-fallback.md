# Responses Origin Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow repeated Responses continuation when origin metadata is missing, invalid or unreadable, preserving request history and known-origin routing.

**Architecture:** Capture one origin lookup decision at distribution. Failed Responses lookups use eligible random channel selection with a request-scoped fallback reason, bypassing rule-affinity preselection; retry and log consumers preserve that decision. Remove the history-shape gate and leave upstream payload validation intact.

**Tech Stack:** Go, Gin, GORM/SQLite, go-redis/miniredis, httptest.

**Spec:** `docs/superpowers/specs/2026-11-13-responses-origin-fallback-design.md`

## Global Constraints

- Scope `/v1/responses` and `/v1/responses/compact`; existing Chat and Gemini policy unchanged.
- Preserve body, header, parent correlation, known-source type/region/key constraints and bounded retry policy.
- Record `not_found`, `unavailable`, `invalid` in existing correlation metadata; no schema changes or request-body logging.
- Never delete, refresh or rebind historical mappings on fallback success; register only newly generated IDs.
- User approval of this design and inline implementation is established by the preceding discussion and “开始实施修复”; no additional approval gate.

## Review Focus

- Redis recovers between distribution and retry: an unknown parent must not gain invented provenance mid-request.
- Redis fails after a successful lookup: cached request provenance must still permit known-origin routing.
- A recoverable backend error coexists with group/model restrictions or no eligible channel: fallback must preserve those restrictions.
- A historical reference is rejected by upstream: preserve upstream status/body and failure accounting.
- Non-Responses endpoints and conflicting header/body IDs must keep their existing behavior.

---

### Task 1: Allow unresolved Responses origins through normal routing

**Files:**
- Modify: `middleware/distributor.go`, `middleware/response_correlation.go`
- Modify: `model/response_origin.go`, `model/response_log.go`, `controller/retry_policy.go`
- Modify: `relay/controller/azure_responses.go`, `relay/controller/opeai_response.go` (skip destructive history sanitization only for fallback requests)
- Test: `controller/response_origin_miss_test.go`, `controller/response_origin_fallback_test.go`, `model/response_log_test.go`
- Update local ignored `AGENTS.md` and this plan with verification evidence.

**Interfaces:**
- Consume `LookupResponseOrigin(id string) (ResponseOrigin, error)` and existing error sentinels.
- Add `WithResponseOriginFallback(ctx context.Context, lookupErr error) context.Context` and `ResponseOriginFallbackReason(ctx context.Context) string`; store only a classified reason.
- Keep `newRetryContext`, `AppendResponseCorrelationOther` and existing selection interfaces unchanged.

- [x] Add failing integration tests for `additional_tools` / `agent_message` before and after TTL expiry, repeated continuation, missing/invalid/failed origin lookup, upstream reference rejection, preserved body/header/old mappings and only-new-ID registration.
- [x] Update obsolete Responses hard-reference/Redis 503 assertions to forward; retain Chat restrictions and add header/body conflict and no-channel/group/model permission regressions.
- [x] Add failures for Redis recovery before `newRetryContext` and a Redis error on a redundant second lookup after a successful first lookup; assert historical log metadata is not fabricated.
- [x] Add correlation metadata tests for fallback reasons in existing JSON and semicolon formats, with parent retained and no false origin fields.
- [x] Run `go test ./model ./middleware ./controller -run 'ResponseOrigin|ResponseCorrelation|ResponseLog' -count=1`; expect new forwarding cases to fail with 409/503 or retry provenance/log assertions.
- [x] Implement classified fallback context, Responses path policy, one-lookup distribution, sticky retry decision and correlation metadata. Delete the obsolete replay whitelist. Keep known and non-Responses behavior unchanged.
- [x] Run targeted tests and `go test -race ./model ./middleware ./controller ./relay/controller -run 'ResponseOrigin|ResponseCorrelation|ResponseLog|AzureResponses|Retry' -count=1`; expect PASS.
- [x] Run `go build ./... && go vet ./...`; expect exit 0. Run `go test ./...`; inspect every failure and compare baseline without expanding this fix.
- [x] Review the complete diff with a fresh reviewer, address material issues with failing tests, update lessons/verification, and commit the feature.

## Verification Record

Verified on 2026-11-13 against baseline `f493376e04fb2957bd19823eadc1affa077ae7e5`:

- RED: new tests reproduced repeated 409/503, duplicate origin lookup failure and rediscovered header provenance. The relay test also caught Azure stripping encrypted history before the fallback guard was added.
- `go build ./... && go vet ./...`: exit 0.
- `go test ./model ./middleware ./controller ./relay/controller -run 'ResponseOrigin|ResponseCorrelation|ResponseLog|AzureResponses' -count=1 -timeout=90s`: all four packages passed.
- `go test -race ./model ./middleware ./controller ./relay/controller -run 'ResponseOrigin|ResponseCorrelation|ResponseLog|AzureResponses|Retry' -count=1 -timeout=120s`: all four packages passed, no race reports. macOS linker emitted `malformed LC_DYSYMTAB` warnings without failing the command.
- Full relay coverage includes upstream 400 rejection without history stripping, first-attempt 429 followed by success on another channel, persisted error/consumption logs, and unchanged parent mappings after an origin lookup error.
- `git diff --check`: passed.
- `go test ./...`: failed on both baseline and branch, with exactly the same failed test/subtest names (programmatically compared, zero additions/removals):

| Package | Existing failure | Observed cause |
| --- | --- | --- |
| `common/image` | `TestDecode/Decode:jpeg` | Wikimedia TLS handshake timeout followed by nil dereference in the test |
| `controller` | `TestEvaluateUsageBasedChannelDisable_SingleChannelTriggered`, `TestEvaluateUsageBasedChannelDisable_MixedChannels` | Existing channel-disable assertions |
| `controller` | `TestFluxVideoUpscaleLifecycle` (eight subtests) | Expected native polling URL differs from current Flux URL |
| `relay/channel/anthropic` | `TestFilterBetaFlags/vertex_allows_files-api` | Expected files-api beta flag differs from current filter |
| `relay/channel/kling` | `TestGetModelNameFromRequest/有model` | Expected model name differs from current extraction |

Local diagnostic outputs: `/tmp/one-api-origin-fallback-baseline-full.log`, `/tmp/one-api-origin-fallback-full.log`, `/tmp/one-api-origin-fallback-race.log`.

Implementation decisions: disable Azure encrypted-history stripping only for fallback requests; keep the existing HTTP `status_<http>` error-code normalization while preserving upstream HTTP status and message. This change handles origin lookup failures; authentication/quota Redis dependencies retain their existing behavior. New origin registration remains best-effort during a Redis outage.

Independent review found one Important issue: rule-affinity preselection could accept an enabled channel whose requested model ability was disabled. `TestResponseOriginFallbackIgnoresRuleAffinity` reproduced the failure in all four missing/read-error × replacement/exhaustion cases, then passed after fallback requests bypassed rule-affinity preselection. The test also verifies that affinity cannot suppress fallback retry. No Critical or Minor findings. Build/vet, targeted race tests and the full-suite baseline comparison were repeated after this fix with the same results above.

Review dispositions and limits:

- Fallback bypasses rule-affinity preference as well as its retry suppression. This follows the approved random-channel fallback; the tradeoff is losing rule-affinity locality for these unresolved continuations. Known origins and ordinary non-fallback requests retain their existing routing.
- Azure history stripping remains unchanged for non-fallback requests. Extending that change would alter legacy requests beyond this fix; their existing history-loss risk remains. For fallback requests the guard intentionally returns an upstream failure rather than silently continuing with stripped history.
- Existing upstream HTTP error-code/body normalization remains unchanged. HTTP status/message are preserved; consumers still receive normalized machine codes rather than guaranteed original error JSON.
- Authentication/quota Redis handling remains unchanged. Origin lookup failure alone can fall back, but a complete Redis outage can still fail elsewhere and prevent new origin registration.
- Existing model-mapping serialization remains unchanged in the shared relay. The fallback adds no history rewriting, and byte-for-byte preservation tests use unmapped models; configured model mapping can still normalize JSON or omit unknown top-level fields as before.

The feature is retained on the local repair branch; no push or deployment is included.
