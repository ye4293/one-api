# 验证记录（2026-11-11）

- Base: 1b4ee436；修复分支 fix/response-origin-routing。
- go build ./... && go vet ./...：通过。
- 相关包 Response/Origin/Correlation/Log/Retry/ScanSSEOwned 回归，含 race：通过。
- go test ./...：未全绿；以下失败在修复前的 1b4ee436 已复现，无新增失败。

- `github.com/songquanpeng/one-api/common/image` / `TestDecode`
- `github.com/songquanpeng/one-api/common/image` / `TestDecode/Decode:jpeg`
- `github.com/songquanpeng/one-api/controller` / `TestEvaluateUsageBasedChannelDisable_MixedChannels`
- `github.com/songquanpeng/one-api/controller` / `TestEvaluateUsageBasedChannelDisable_SingleChannelTriggered`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/后台失败退款`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/后台完成`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/回调提交无轮询地址时后台查询`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/回调提交无轮询地址时客户端查询`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/客户端失败退款`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/客户端完成`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/本站回调失败退款`
- `github.com/songquanpeng/one-api/controller` / `TestFluxVideoUpscaleLifecycle/本站回调完成`
- `github.com/songquanpeng/one-api/relay/channel/anthropic` / `TestFilterBetaFlags`
- `github.com/songquanpeng/one-api/relay/channel/anthropic` / `TestFilterBetaFlags/vertex_allows_files-api`
- `github.com/songquanpeng/one-api/relay/channel/kling` / `TestGetModelNameFromRequest`
- `github.com/songquanpeng/one-api/relay/channel/kling` / `TestGetModelNameFromRequest/有model`
