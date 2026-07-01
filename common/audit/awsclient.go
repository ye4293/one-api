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
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/songquanpeng/one-api/common/logger"
)

type awsAuditClient struct {
	cfg  *auditConfig
	fh   *firehose.Client
	ath  *athena.Client
	glue *glue.Client
	s3c  *s3.Client
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
		s3c:  s3.NewFromConfig(opts),
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

	if c.cfg.AthenaWorkgroup != "" && c.cfg.S3OutputLocation != "" {
		if err := c.ensureTableViaAthena(ctx); err != nil {
			return err
		}
	} else {
		if err := c.ensureTableViaGlue(ctx); err != nil {
			return err
		}
	}

	logger.SysLog("audit: Glue resources ensured (database=" + c.cfg.AthenaDatabase + ", table=" + c.cfg.AthenaTable + ")")
	return nil
}

// ensureTableViaAthena 用一条 Athena SQL 建表并设置 day(event_time) 分区，幂等。
func (c *awsAuditClient) ensureTableViaAthena(ctx context.Context) error {
	tCtx, cancel := context.WithTimeout(ctx, 120*1e9) // 120s
	defer cancel()
	sql := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s (
  event_time                 timestamp,
  x_request_id              string,
  user_id                   int,
  username                  string,
  channel_id                int,
  token_name                string,
  origin_model              string,
  actual_model              string,
  is_stream                 boolean,
  status_code               int,
  duration_ms               bigint,
  original_req_headers      string,
  original_req_body         string,
  converted_req_headers     string,
  converted_req_body        string,
  converted_same_as_original boolean,
  upstream_response         string,
  client_response           string,
  truncated_fields          array<string>,
  dropped_note              string
)
PARTITIONED BY (day(event_time))
LOCATION '%s'
TBLPROPERTIES (
  'table_type'='ICEBERG',
  'format'='parquet',
  'write_compression'='zstd'
)`, c.cfg.AthenaDatabase, c.cfg.AthenaTable, c.cfg.S3DataLocation)
	_, err := c.executeQuery(tCtx, sql)
	if err != nil {
		return fmt.Errorf("athena CreateTable: %w", err)
	}
	logger.SysLog("audit: table with day(event_time) partition ensured via Athena")
	return nil
}

// ensureTableViaGlue 通过 Glue API 建表（无分区），在未配置 Athena workgroup 时使用。
func (c *awsAuditClient) ensureTableViaGlue(ctx context.Context) error {
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
	_, err := c.glue.CreateTable(ctx, &glue.CreateTableInput{
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
				"format":            "parquet",
				"write_compression": "zstd",
			},
		},
	})
	if err != nil && !isAlreadyExistsError(err) {
		return fmt.Errorf("glue CreateTable: %w", err)
	}
	return nil
}

func isAlreadyExistsError(err error) bool {
	var alreadyExists *glueTypes.AlreadyExistsException
	return errors.As(err, &alreadyExists)
}
