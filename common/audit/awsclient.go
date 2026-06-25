package audit

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/firehose"
	firehoseTypes "github.com/aws/aws-sdk-go-v2/service/firehose/types"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	glueTypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/songquanpeng/one-api/common/logger"
)

type awsAuditClient struct {
	cfg  *auditConfig
	fh   *firehose.Client
	ath  *athena.Client
	glue *glue.Client
}

func newAWSClient(cfg *auditConfig) *awsAuditClient {
	creds := aws.NewCredentialsCache(
		credentials.NewStaticCredentialsProvider(cfg.AWSAccessKey, cfg.AWSSecretKey, ""),
	)
	opts := aws.Config{
		Region:      cfg.AWSRegion,
		Credentials: creds,
	}
	return &awsAuditClient{
		cfg:  cfg,
		fh:   firehose.NewFromConfig(opts),
		ath:  athena.NewFromConfig(opts),
		glue: glue.NewFromConfig(opts),
	}
}

func (c *awsAuditClient) Close() error {
	return nil
}

const (
	maxRecordsPerBatch = 500
	maxBytesPerBatch   = 4 * 1024 * 1024
)

func (c *awsAuditClient) putRecordBatch(ctx context.Context, batch []*AuditRecord) (sent int, err error) {
	records := make([]firehoseTypes.Record, 0, min(len(batch), maxRecordsPerBatch))
	var totalBytes int

	for _, r := range batch {
		data := []byte(toNDJSONLine(r))
		if totalBytes+len(data) > maxBytesPerBatch && len(records) > 0 {
			if err := c.sendBatch(ctx, records); err != nil {
				return sent, err
			}
			sent += len(records)
			records = records[:0]
			totalBytes = 0
		}
		records = append(records, firehoseTypes.Record{Data: data})
		totalBytes += len(data)
		if len(records) >= maxRecordsPerBatch {
			if err := c.sendBatch(ctx, records); err != nil {
				return sent, err
			}
			sent += len(records)
			records = records[:0]
			totalBytes = 0
		}
	}
	if len(records) > 0 {
		if err := c.sendBatch(ctx, records); err != nil {
			return sent, err
		}
		sent += len(records)
	}
	return sent, nil
}

func (c *awsAuditClient) sendBatch(ctx context.Context, records []firehoseTypes.Record) error {
	out, err := c.fh.PutRecordBatch(ctx, &firehose.PutRecordBatchInput{
		DeliveryStreamName: aws.String(c.cfg.FirehoseStream),
		Records:            records,
	})
	if err != nil {
		return fmt.Errorf("firehose PutRecordBatch: %w", err)
	}
	if out.FailedPutCount != nil && *out.FailedPutCount > 0 {
		var retryRecords []firehoseTypes.Record
		for i, resp := range out.RequestResponses {
			if resp.ErrorCode != nil {
				retryRecords = append(retryRecords, records[i])
			}
		}
		if len(retryRecords) > 0 {
			retryOut, err := c.fh.PutRecordBatch(ctx, &firehose.PutRecordBatchInput{
				DeliveryStreamName: aws.String(c.cfg.FirehoseStream),
				Records:            retryRecords,
			})
			if err != nil {
				return fmt.Errorf("firehose retry: %w", err)
			}
			if retryOut.FailedPutCount != nil && *retryOut.FailedPutCount > 0 {
				return fmt.Errorf("firehose: %d records still failed after retry", *retryOut.FailedPutCount)
			}
		}
	}
	return nil
}

func (c *awsAuditClient) ensureGlueResources(ctx context.Context) error {
	_, err := c.glue.CreateDatabase(ctx, &glue.CreateDatabaseInput{
		DatabaseInput: &glueTypes.DatabaseInput{
			Name: aws.String(c.cfg.AthenaDatabase),
		},
	})
	if err != nil && !isAlreadyExistsError(err) {
		return fmt.Errorf("glue CreateDatabase: %w", err)
	}

	columns := []glueTypes.Column{
		{Name: aws.String("event_time"), Type: aws.String("timestamp")},
		{Name: aws.String("x_request_id"), Type: aws.String("string")},
		{Name: aws.String("user_id"), Type: aws.String("int")},
		{Name: aws.String("username"), Type: aws.String("string")},
		{Name: aws.String("channel_id"), Type: aws.String("int")},
		{Name: aws.String("token_name"), Type: aws.String("string")},
		{Name: aws.String("origin_model"), Type: aws.String("string")},
		{Name: aws.String("actual_model"), Type: aws.String("string")},
		{Name: aws.String("is_stream"), Type: aws.String("boolean")},
		{Name: aws.String("status_code"), Type: aws.String("int")},
		{Name: aws.String("duration_ms"), Type: aws.String("bigint")},
		{Name: aws.String("original_req_headers"), Type: aws.String("string")},
		{Name: aws.String("original_req_body"), Type: aws.String("string")},
		{Name: aws.String("converted_req_headers"), Type: aws.String("string")},
		{Name: aws.String("converted_req_body"), Type: aws.String("string")},
		{Name: aws.String("converted_same_as_original"), Type: aws.String("boolean")},
		{Name: aws.String("upstream_response"), Type: aws.String("string")},
		{Name: aws.String("client_response"), Type: aws.String("string")},
		{Name: aws.String("truncated_fields"), Type: aws.String("array<string>")},
		{Name: aws.String("dropped_note"), Type: aws.String("string")},
	}

	_, err = c.glue.CreateTable(ctx, &glue.CreateTableInput{
		DatabaseName: aws.String(c.cfg.AthenaDatabase),
		OpenTableFormatInput: &glueTypes.OpenTableFormatInput{
			IcebergInput: &glueTypes.IcebergInput{
				MetadataOperation: glueTypes.MetadataOperationCreate,
				Version:           aws.String("2"),
			},
		},
		TableInput: &glueTypes.TableInput{
			Name: aws.String(c.cfg.AthenaTable),
			StorageDescriptor: &glueTypes.StorageDescriptor{
				Columns:  columns,
				Location: aws.String(c.cfg.S3DataLocation),
			},
			TableType: aws.String("EXTERNAL_TABLE"),
			Parameters: map[string]string{
				"table_type":              "ICEBERG",
				"format":                  "parquet",
				"write_compression":       "zstd",
				"optimize_rewrite_delete_file_threshold": "2",
			},
		},
	})
	if err != nil && !isAlreadyExistsError(err) {
		return fmt.Errorf("glue CreateTable: %w", err)
	}

	logger.SysLog("audit: Glue resources ensured (database=" + c.cfg.AthenaDatabase + ", table=" + c.cfg.AthenaTable + ")")
	return nil
}

func isAlreadyExistsError(err error) bool {
	var alreadyExists *glueTypes.AlreadyExistsException
	return errors.As(err, &alreadyExists)
}
