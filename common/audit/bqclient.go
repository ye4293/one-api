package audit

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

type gcpClient struct {
	cfg *config
	bq  *bigquery.Client
	gcs *storage.Client
}

func buildBQSchema() bigquery.Schema {
	str := bigquery.StringFieldType
	return bigquery.Schema{
		{Name: "event_time", Type: bigquery.TimestampFieldType},
		{Name: "x_request_id", Type: str},
		{Name: "user_id", Type: bigquery.IntegerFieldType},
		{Name: "username", Type: str},
		{Name: "channel_id", Type: bigquery.IntegerFieldType},
		{Name: "token_name", Type: str},
		{Name: "origin_model", Type: str},
		{Name: "actual_model", Type: str},
		{Name: "is_stream", Type: bigquery.BooleanFieldType},
		{Name: "status_code", Type: bigquery.IntegerFieldType},
		{Name: "duration_ms", Type: bigquery.IntegerFieldType},
		{Name: "original_req_headers", Type: str},
		{Name: "original_req_body", Type: str},
		{Name: "converted_req_headers", Type: str},
		{Name: "converted_req_body", Type: str},
		{Name: "converted_same_as_original", Type: bigquery.BooleanFieldType},
		{Name: "upstream_response", Type: str},
		{Name: "client_response", Type: str},
		{Name: "truncated_fields", Type: str, Repeated: true},
		{Name: "dropped_note", Type: str},
	}
}

func newGCPClient(ctx context.Context, cfg *config) (*gcpClient, error) {
	var opts []option.ClientOption
	if cfg.CredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(cfg.CredentialsFile))
	}
	bq, err := bigquery.NewClient(ctx, cfg.GCPProject, opts...)
	if err != nil {
		return nil, fmt.Errorf("bigquery client: %w", err)
	}
	gcs, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gcs client: %w", err)
	}
	return &gcpClient{cfg: cfg, bq: bq, gcs: gcs}, nil
}

// ensureTable 幂等建表（按 event_time 天分区，可选过期）。
func (g *gcpClient) ensureTable(ctx context.Context) error {
	ds := g.bq.Dataset(g.cfg.BQDataset)
	if _, err := ds.Metadata(ctx); err != nil {
		if e := ds.Create(ctx, &bigquery.DatasetMetadata{}); e != nil {
			return fmt.Errorf("create dataset: %w", e)
		}
	}
	tbl := ds.Table(g.cfg.BQTable)
	if _, err := tbl.Metadata(ctx); err == nil {
		return nil // 已存在
	}
	tp := &bigquery.TimePartitioning{Field: "event_time", Type: bigquery.DayPartitioningType}
	if g.cfg.PartitionExpireDays > 0 {
		tp.Expiration = time.Duration(g.cfg.PartitionExpireDays) * 24 * time.Hour
	}
	return tbl.Create(ctx, &bigquery.TableMetadata{
		Schema:           buildBQSchema(),
		TimePartitioning: tp,
	})
}

// uploadAndLoad 上传 gzip NDJSON 到 GCS 后提交 load job。
func (g *gcpClient) uploadAndLoad(ctx context.Context, objectName string, gzData []byte) error {
	obj := g.gcs.Bucket(g.cfg.GCSBucket).Object(objectName)
	w := obj.NewWriter(ctx)
	if _, err := w.Write(gzData); err != nil {
		w.Close()
		return fmt.Errorf("gcs write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("gcs close: %w", err)
	}
	gcsRef := bigquery.NewGCSReference(fmt.Sprintf("gs://%s/%s", g.cfg.GCSBucket, objectName))
	gcsRef.SourceFormat = bigquery.JSON
	gcsRef.Compression = bigquery.Gzip
	loader := g.bq.Dataset(g.cfg.BQDataset).Table(g.cfg.BQTable).LoaderFrom(gcsRef)
	loader.WriteDisposition = bigquery.WriteAppend
	job, err := loader.Run(ctx)
	if err != nil {
		return fmt.Errorf("load job run: %w", err)
	}
	status, err := job.Wait(ctx)
	if err != nil {
		return fmt.Errorf("load job wait: %w", err)
	}
	if err := status.Err(); err != nil {
		return fmt.Errorf("load job failed: %w", err)
	}
	// 加载成功后删除中转对象
	_ = obj.Delete(ctx)
	return nil
}
