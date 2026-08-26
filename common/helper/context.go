package helper

import "context"

// DetachCancel 返回一个断开 cancel 但保留全部 value 的派生 context。
//
// 使用场景：`go someAsyncWork(ctx, ...)` 里的 ctx 来自 `c.Request.Context()`，
// gin 写完 HTTP 响应后会 cancel 该 ctx；异步 goroutine 里的下游调用（如 Redis
// `pipe.Exec(ctx)` / DB 查询 / 日志埋点）拿到已 cancel 的 ctx 立即报
// "context canceled"，导致打点丢失。用 `context.WithoutCancel` 断开 cancel 信号，
// 但保留 request-id、tracing、日志关联等 value —— 需要 Go 1.21+。
//
// 集中封装是为了：后续如需注入 timeout / logger 只改一处，且调用点意图更清晰。
func DetachCancel(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}
