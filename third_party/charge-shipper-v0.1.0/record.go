package billship

import "errors"

// Record 是网关每条 type=2 日志的投递单元。Body 为 logs 行 JSON 原样序列化。
// 契约：Ship 返回后调用方不得再修改 Body（SDK 异步持有该 slice，不内部拷贝以省分配）。
type Record struct {
	SiteID     string // 路由属性
	Model      string // 路由属性 = 网关 ModelName
	SourceType string // "one-api" / "new-api"
	LogID      int64  // 源 logs.id —— 补数据锚点
	CreatedAt  int64  // 源 logs.created_at（Unix 秒）
	Body       []byte // logs 行 JSON
}

var (
	errEmptyAttr = errors.New("billship: empty routing attribute")
	errEmptyBody = errors.New("billship: empty body")
	errTooLarge  = errors.New("billship: message exceeds 256KB")
)

// validate 在入队前拦截会带崩整批的非法记录（空属性 / 空 Body / 整条含属性超 256KB）。
// 空 Body 会被 SQS 以 InvalidParameterValue 整批拒收（连累同批其余合法条），故必须入队前拦掉。
func validate(r Record) error {
	if r.SiteID == "" || r.Model == "" || r.SourceType == "" {
		return errEmptyAttr
	}
	if len(r.Body) == 0 {
		return errEmptyBody
	}
	if recordSize(r) > maxMessageBytes {
		return errTooLarge
	}
	return nil
}

// recordSize 估算单条 SQS 消息占用：Body + 每个属性的 name + DataType + value。
// 用于攒批时的 256KB 聚合切批与入队前校验；口径与 SQS 的 256KB 计量一致
// （属性的 name/DataType/value 三部分都计入，不能只算 Body）。
func recordSize(r Record) int {
	return len(r.Body) +
		attrBytes(attrSiteID, r.SiteID) +
		attrBytes(attrModel, r.Model) +
		attrBytes(attrSourceType, r.SourceType)
}

func attrBytes(name, val string) int {
	return len(name) + len(attrDataType) + len(val)
}
