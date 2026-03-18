package channel

import (
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/relay/util"
)

// BaseVideoAdaptor 为所有视频适配器提供默认的 Init 和 GetPrePaymentQuota 实现。
// 供应商通过嵌入此结构体来复用，覆盖有差异的方法。
type BaseVideoAdaptor struct {
	Meta *util.RelayMeta
}

func (b *BaseVideoAdaptor) Init(meta *util.RelayMeta) { b.Meta = meta }

// GetPrePaymentQuota 默认预扣 0.2 美元。预扣费不同的供应商覆盖此方法。
func (b *BaseVideoAdaptor) GetPrePaymentQuota() int64 {
	return int64(0.2 * config.QuotaPerUnit)
}
