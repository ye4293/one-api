package common

import (
	"context"
	"fmt"
	"math"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/songquanpeng/one-api/common/logger"
)

var relayGoPool gopool.Pool

// ChannelDisablePool 专用于异步处理渠道/Key禁用的协程池。
// 容量 200：禁用操作低频，gopool 提供 panic 隔离，单次 panic 不影响进程。
var ChannelDisablePool gopool.Pool

func init() {
	relayGoPool = gopool.NewPool("gopool.RelayPool", math.MaxInt32, gopool.NewConfig())
	relayGoPool.SetPanicHandler(func(ctx context.Context, i interface{}) {
		if stopChan, ok := ctx.Value("stop_chan").(chan bool); ok {
			SafeSendBool(stopChan, true)
		}
		//logger.Error(ctx,errors.New(fmt.Sprintf("panic in gopool.RelayPool: %v", i)))
	})

	ChannelDisablePool = gopool.NewPool("channel.disable", 200, gopool.NewConfig())
	ChannelDisablePool.SetPanicHandler(func(_ context.Context, i interface{}) {
		logger.SysError(fmt.Sprintf("panic in channel.disable pool: %v", i))
	})
}

func RelayCtxGo(ctx context.Context, f func()) {
	relayGoPool.CtxGo(ctx, f)
}
