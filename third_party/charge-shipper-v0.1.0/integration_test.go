//go:build integration

package billship

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// 需先 make up 起 LocalStack（:4566）。运行： go test -tags integration ./... -run TestIntegration -v
func TestIntegrationShipRoundTrip(t *testing.T) {
	endpoint := os.Getenv("AWS_ENDPOINT_URL")
	if endpoint == "" {
		endpoint = "http://localhost:4566"
	}
	ctx := context.Background()

	// LocalStack 联调需清空代理环境变量（见 CLAUDE.md），并用 fake 凭证绕过 IMDSv2。
	os.Unsetenv("HTTP_PROXY")
	os.Unsetenv("HTTPS_PROXY")
	os.Unsetenv("NO_PROXY")
	os.Setenv("AWS_ACCESS_KEY_ID", "test")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	// 直接建一个指向 LocalStack 的客户端建/清队列并收消息。
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithBaseEndpoint(endpoint),
	)
	if err != nil {
		t.Fatalf("load cfg: %v", err)
	}
	cli := sqs.NewFromConfig(awsCfg)
	cq, err := cli.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("bill-shipper-it")})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	queueURL := aws.ToString(cq.QueueUrl)
	_, _ = cli.PurgeQueue(ctx, &sqs.PurgeQueueInput{QueueUrl: cq.QueueUrl})

	// LocalStack 需要 endpoint；用 AWS_ENDPOINT_URL 环境变量让 SDK 默认链接管。
	os.Setenv("AWS_ENDPOINT_URL", endpoint)
	os.Setenv("AWS_REGION", "us-east-1")

	resetSingleton()
	if err := Init(Config{
		QueueURL: queueURL, Region: "us-east-1",
		SiteID: "site-it", SourceType: "new-api", Enabled: true,
		BatchSize: 10, BatchWait: 50 * time.Millisecond, SendConcurrency: 2,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	const n = 25
	for i := 0; i < n; i++ {
		Ship(Record{SiteID: "site-it", Model: "gpt-4", SourceType: "new-api", LogID: int64(i), Body: []byte(`{"id":1}`)})
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := Shutdown(sctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	resetSingleton()

	// 收回消息，校验数量与信封属性。
	got := 0
	deadline := time.Now().Add(10 * time.Second)
	for got < n && time.Now().Before(deadline) {
		out, err := cli.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:              cq.QueueUrl,
			MaxNumberOfMessages:   10,
			WaitTimeSeconds:       1,
			MessageAttributeNames: []string{"All"},
		})
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		for _, m := range out.Messages {
			assertAttr(t, m.MessageAttributes, attrSiteID, "site-it")
			assertAttr(t, m.MessageAttributes, attrModel, "gpt-4")
			assertAttr(t, m.MessageAttributes, attrSourceType, "new-api")
			got++
			_, _ = cli.DeleteMessage(ctx, &sqs.DeleteMessageInput{QueueUrl: cq.QueueUrl, ReceiptHandle: m.ReceiptHandle})
		}
	}
	if got != n {
		t.Errorf("received %d messages, want %d", got, n)
	}
}

func assertAttr(t *testing.T, attrs map[string]types.MessageAttributeValue, key, want string) {
	t.Helper()
	av, ok := attrs[key]
	if !ok {
		t.Errorf("missing attr %q", key)
		return
	}
	if aws.ToString(av.StringValue) != want {
		t.Errorf("attr %q = %q, want %q", key, aws.ToString(av.StringValue), want)
	}
}
