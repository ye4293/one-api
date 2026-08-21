package billship

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// 信封契约（对齐消费侧架构详设 §3，不新增不改动）。
const (
	attrSiteID     = "site_id"
	attrModel      = "model"
	attrSourceType = "source_type"
	attrDataType   = "String" // MessageAttribute DataType；SQS 把它也计入 256KB

	maxMessageBytes = 256 * 1024 // SQS 单条 / 单批聚合上限
)

// buildEntry 把一条 Record 组装成 SendMessageBatch 的一个条目。
func buildEntry(id string, r Record) types.SendMessageBatchRequestEntry {
	return types.SendMessageBatchRequestEntry{
		Id:          aws.String(id),
		MessageBody: aws.String(string(r.Body)),
		MessageAttributes: map[string]types.MessageAttributeValue{
			attrSiteID:     {DataType: aws.String(attrDataType), StringValue: aws.String(r.SiteID)},
			attrModel:      {DataType: aws.String(attrDataType), StringValue: aws.String(r.Model)},
			attrSourceType: {DataType: aws.String(attrDataType), StringValue: aws.String(r.SourceType)},
		},
	}
}
