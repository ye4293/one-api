package audit

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/bigquery/storage/managedwriter"
	"cloud.google.com/go/bigquery/storage/managedwriter/adapt"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type gcpClient struct {
	cfg        *config
	bq         *bigquery.Client
	mw         *managedwriter.Client
	writer     *managedwriter.ManagedStream
	descriptor protoreflect.MessageDescriptor
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
	mw, err := managedwriter.NewClient(ctx, cfg.GCPProject, opts...)
	if err != nil {
		return nil, fmt.Errorf("managedwriter client: %w", err)
	}
	g := &gcpClient{cfg: cfg, bq: bq, mw: mw}
	if err := g.initWriter(ctx); err != nil {
		mw.Close()
		return nil, fmt.Errorf("init writer: %w", err)
	}
	return g, nil
}

func (g *gcpClient) initWriter(ctx context.Context) error {
	storageSchema, err := adapt.BQSchemaToStorageTableSchema(buildBQSchema())
	if err != nil {
		return fmt.Errorf("schema conversion: %w", err)
	}
	descriptor, err := adapt.StorageSchemaToProto2Descriptor(storageSchema, "AuditRow")
	if err != nil {
		return fmt.Errorf("proto descriptor: %w", err)
	}
	messageDescriptor, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok {
		return fmt.Errorf("unexpected descriptor type %T", descriptor)
	}
	g.descriptor = messageDescriptor

	dp, err := adapt.NormalizeDescriptor(messageDescriptor)
	if err != nil {
		return fmt.Errorf("normalize descriptor: %w", err)
	}

	tableName := managedwriter.TableParentFromParts(g.cfg.GCPProject, g.cfg.BQDataset, g.cfg.BQTable)
	g.writer, err = g.mw.NewManagedStream(ctx,
		managedwriter.WithDestinationTable(tableName),
		managedwriter.WithType(managedwriter.DefaultStream),
		managedwriter.WithSchemaDescriptor(dp),
		managedwriter.EnableWriteRetries(true),
	)
	if err != nil {
		return fmt.Errorf("new managed stream: %w", err)
	}
	return nil
}

func (g *gcpClient) ensureTable(ctx context.Context) error {
	ds := g.bq.Dataset(g.cfg.BQDataset)
	if _, err := ds.Metadata(ctx); err != nil {
		if e := ds.Create(ctx, &bigquery.DatasetMetadata{}); e != nil {
			return fmt.Errorf("create dataset: %w", e)
		}
	}
	tbl := ds.Table(g.cfg.BQTable)
	if _, err := tbl.Metadata(ctx); err == nil {
		return nil
	}
	tp := &bigquery.TimePartitioning{Field: "event_time", Type: bigquery.DayPartitioningType}
	if g.cfg.PartitionExpireDays > 0 {
		tp.Expiration = time.Duration(g.cfg.PartitionExpireDays) * 24 * time.Hour
	}
	return tbl.Create(ctx, &bigquery.TableMetadata{
		Schema:           buildBQSchema(),
		TimePartitioning: tp,
		Clustering:       &bigquery.Clustering{Fields: []string{"x_request_id", "actual_model", "channel_id", "user_id"}},
	})
}

func (g *gcpClient) appendRows(ctx context.Context, batch []*AuditRecord) error {
	rows := make([][]byte, 0, len(batch))
	for _, r := range batch {
		b, err := marshalProtoRow(r, g.descriptor)
		if err != nil {
			continue
		}
		rows = append(rows, b)
	}
	if len(rows) == 0 {
		return nil
	}
	result, err := g.writer.AppendRows(ctx, rows)
	if err != nil {
		return fmt.Errorf("append rows: %w", err)
	}
	_, err = result.GetResult(ctx)
	if err != nil {
		return fmt.Errorf("append result: %w", err)
	}
	return nil
}

func (g *gcpClient) Close() error {
	var firstErr error
	if g.writer != nil {
		if err := g.writer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if g.mw != nil {
		if err := g.mw.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func marshalProtoRow(r *AuditRecord, md protoreflect.MessageDescriptor) ([]byte, error) {
	row := dynamicpb.NewMessage(md)

	row.Set(md.Fields().ByName("event_time"), protoreflect.ValueOfInt64(r.EventTime.UnixMicro()))
	row.Set(md.Fields().ByName("x_request_id"), protoreflect.ValueOfString(r.XRequestID))
	row.Set(md.Fields().ByName("user_id"), protoreflect.ValueOfInt64(int64(r.UserID)))
	row.Set(md.Fields().ByName("username"), protoreflect.ValueOfString(r.Username))
	row.Set(md.Fields().ByName("channel_id"), protoreflect.ValueOfInt64(int64(r.ChannelID)))
	row.Set(md.Fields().ByName("token_name"), protoreflect.ValueOfString(r.TokenName))
	row.Set(md.Fields().ByName("origin_model"), protoreflect.ValueOfString(r.OriginModel))
	row.Set(md.Fields().ByName("actual_model"), protoreflect.ValueOfString(r.ActualModel))
	row.Set(md.Fields().ByName("is_stream"), protoreflect.ValueOfBool(r.IsStream))
	row.Set(md.Fields().ByName("status_code"), protoreflect.ValueOfInt64(int64(r.StatusCode)))
	row.Set(md.Fields().ByName("duration_ms"), protoreflect.ValueOfInt64(r.DurationMS))
	row.Set(md.Fields().ByName("original_req_headers"), protoreflect.ValueOfString(r.OriginalReqHeaders))
	row.Set(md.Fields().ByName("original_req_body"), protoreflect.ValueOfString(r.OriginalReqBody))
	row.Set(md.Fields().ByName("converted_req_headers"), protoreflect.ValueOfString(r.ConvertedReqHeaders))

	convBody := r.ConvertedReqBody
	if r.ConvertedSameAsOriginal {
		convBody = ""
	}
	row.Set(md.Fields().ByName("converted_req_body"), protoreflect.ValueOfString(convBody))
	row.Set(md.Fields().ByName("converted_same_as_original"), protoreflect.ValueOfBool(r.ConvertedSameAsOriginal))
	row.Set(md.Fields().ByName("upstream_response"), protoreflect.ValueOfString(r.UpstreamResponse))
	row.Set(md.Fields().ByName("client_response"), protoreflect.ValueOfString(r.ClientResponse))
	row.Set(md.Fields().ByName("dropped_note"), protoreflect.ValueOfString(r.DroppedNote))

	if len(r.TruncatedFields) > 0 {
		fd := md.Fields().ByName("truncated_fields")
		list := row.Mutable(fd).List()
		for _, s := range r.TruncatedFields {
			list.Append(protoreflect.ValueOfString(s))
		}
	}

	return proto.Marshal(row)
}
