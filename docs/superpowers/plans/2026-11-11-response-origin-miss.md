# Response Origin Miss Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Allow eligible encrypted reasoning replays to reach upstream on a confirmed Redis miss, while preserving hard references and known origin constraints.

**Architecture:** Add distinct model lookup errors and a conservative request eligibility check in middleware. Clear only the local routing ID on eligible misses; preserve body and correlation context. Keep existing relay behavior.

**Tech Stack:** Go, Gin, go-redis, SQLite and miniredis tests.

**Spec:** `docs/superpowers/specs/2026-11-11-response-origin-miss-design.md`

## Global Constraints

- No mapping deletion, rebinding, production writes, new dependencies, or generic cross-region retry.
- Existing successful origin selection stays constrained.
- Run `go test ./...` and `go build ./... && go vet ./...` before commit.

## Review Focus

- A body with only opaque state must not silently become a fresh conversation.
- A header plus replay must preserve the original header and correlation logs.
- Redis backend failure must not bypass an existing origin.
- The second model lookup must not recreate the original failure.
- Known origin with no compatible channel stays 503 and retains the mapping.

### Task 1: Classify lookup failures and permit bounded replay

**Files:** `model/response_origin.go`, `model/response_log.go`, `model/response_log_test.go`, `middleware/response_correlation.go`, `middleware/distributor.go`, `controller/response_origin_miss_test.go`.

**Interfaces:** Keep `LookupResponseOrigin(id string) (ResponseOrigin, error)` and `captureResponseCorrelation(c *gin.Context) (string, error)`. Add `ErrResponseOriginNotFound` and `ErrResponseOriginUnavailable`; add `canReplayResponsesWithoutOrigin(c *gin.Context) bool` for eligibility.

- [x] Write integration tests `TestResponseOriginMissReplay`, `TestResponseOriginMissHardReferences`, `TestResponseOriginBackendFailure`, `TestResponseOriginMissKnownMapping`: assert repeated valid replays reach relay unchanged, only new IDs are registered, hard references remain 409, infrastructure faults become 503, known origin constraints remain.
- [x] Run `go test ./controller -run 'TestResponseOrigin(Miss|Backend)' -count=1`; expect current code to fail replay and backend assertions.
- [x] Implement distinct missing/unavailable errors, strict replay shape eligibility, and distributor status/selection handling. Keep malformed mappings rejected.
- [x] Run the regression command plus related model/middleware/relay tests; expect PASS.
- [x] Run full tests, build and vet; inspect failures, update the local AGENTS lesson and record results.
- [x] Review the complete change with a fresh reviewer; fix relevant findings and verify; commit as `fix: allow encrypted reasoning replay on missing response origin`.

## Execution record and validation

- Baseline: `119c11b9`, isolated worktree, no production Redis/SQL writes.
- Initial RED reproduced repeated replay 409 and backend failures misreported as 409. Boundary RED caught empty messages and ID-only reasoning. Fresh review RED reproduced custom-tool 409 and fabricated historical provenance after `newRetryContext`.
- Review fixes: include self-carried custom-tool records; suppress historical `origin_*` log fields for ID-less retry snapshots. Both are small corrections to the original scope, not changes to retry constraints.
- Relevant command: `go test ./model ./middleware ./controller ./relay/controller -run 'ResponseOrigin|ResponseCorrelation|ResponseLog|AzureResponses|SanitizeAzureResponses' -count=1`.
- Final result: relevant tests PASS in all four packages; `go build ./... && go vet ./...` PASS; `git diff --check` PASS. `go test ./...` still fails only in the same baseline groups listed below.
- Full suite has pre-existing failures, reproduced before implementation: `common/image.TestDecode/Decode:jpeg` (external Wikimedia TLS timeout followed by nil dereference); `controller.TestEvaluateUsageBasedChannelDisable_SingleChannelTriggered` and `TestEvaluateUsageBasedChannelDisable_MixedChannels` (expected disable callbacks not triggered); `controller.TestFluxVideoUpscaleLifecycle` (8 cases expect a different submit response); `relay/channel/anthropic.TestFilterBetaFlags/vertex_allows_files-api` (flag mismatch); `relay/channel/kling.TestGetModelNameFromRequest/有model` (empty parsed model).

## Explicit limits retained after review

- Legacy disabled-channel supplementation and retry key-index handling are separate pre-existing issues. Leaving them unchanged can still reject some known-origin requests or cause upstream decryption failures.
- Existing Azure sanitizer can discard reasoning and compaction state. This change permits only reasoning replay on misses and rejects compaction; it does not promise lossless recovery of encrypted reasoning rejected upstream.
- Client-history completeness cannot be established by the gateway. Input is forwarded intact at the middleware boundary; existing downstream model-mapping reserialization remains unchanged.
- `RedisEnabled=true` with an uninitialized nil client can panic in the earlier user-group lookup. This pre-existing invalid initialization state is outside origin lookup handling; the gateway test covers disabled Redis with no client, not that state.
- Baseline failures above remain outside this small fix. A green targeted run/build/vet does not mean the full suite is green or production is deployed.
