# Responses Stream Errors Implementation Plan

> **For agentic workers:** Use executing-plans to implement this repair inline with TDD.

**Goal:** Detect upstream stream errors without false success or mixing retry streams.

**Architecture:** Reuse the established scanner limits and one error parser at both stream boundaries. Return errors before settlement, and stop the outer retry loop after a response is written.

**Tech Stack:** Go, Gin, bufio.Scanner, existing SQLite test helpers.

**Spec:** docs/plans/2026-11-11-responses-stream-errors-design.md

## Global Constraints

Preserve upstream SSE and error fields; retain 429 for upstream stream errors, use 502 for prefix read errors; avoid unrelated baseline repairs; build and vet before commit.

## Review Focus

Check preserved prefix read-ahead bytes, malformed errors, late errors after the prefix, started response retry prevention, and async request cancellation.

## Task 1: Repair and verify Responses stream errors

**Files:** relay/controller/opeai_response.go; relay/controller/openai_response_stream_test.go; controller/relay.go; controller/responses_stream_test.go; relay/helper/stream_scanner.go; AGENTS.md.

**Interfaces:** Keep bufferStreamPrefix and doNativeOpenaiResponseStream signatures. Reuse common/helper.DetachCancel and recordFinalErrorLog.

- [x] Add regression tests for oversized prefix events and scanner errors; run go test ./relay/controller -run 'TestResponseStream|TestBufferStreamPrefix' -count=1; expect assertions to fail on the current implementation.
- [x] Add shared error parsing, scanner limit/error handling, late error propagation, and detached settlement context; rerun the same tests; expect PASS.
- [x] Add an outer-controller test exercising a started stream that fails; expect retry or trailing JSON before the guard is implemented.
- [x] Stop started-response retries while recording final failure; expect controller regression PASS.
- [x] Update AGENTS.md and run go build ./... && go vet ./..., scoped tests and go test ./...; expect build/vet/scoped PASS, report baseline failures by name.
- [x] Review the complete diff and commit the independently validated repair.

## Validation record

- Original 216,710-byte upstream payload replay: PASS; quota error preserved, response not committed, no consumption recorded.
- Regression tests and targeted go test -race: PASS, including errors during later retry attempts and event-only errors.
- go build ./... && go vet ./...: PASS.
- go test ./...: same six top-level baseline failures; no new failing tests.

Baseline failure names:

- TestDecode
- TestEvaluateUsageBasedChannelDisable_MixedChannels
- TestEvaluateUsageBasedChannelDisable_SingleChannelTriggered
- TestFilterBetaFlags
- TestFluxVideoUpscaleLifecycle
- TestGetModelNameFromRequest

Review decisions: retain the existing incomplete-response billing policy; propagate reported stream interruptions without changing timeout/write internals; keep independent baseline repairs out of this change. Dedicated incomplete-response and malformed-error fixture coverage is deferred.
