package audit

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/songquanpeng/one-api/common/logger"
)

func ingestLoop() {
	defer func() { recover() }()
	var batch []*AuditRecord
	var memBytes int
	ticker := time.NewTicker(pkgConfig.FlushInterval)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		dispatch(batch)
		batch = nil
		memBytes = 0
	}
	for {
		select {
		case r, ok := <-recordChan:
			if !ok {
				flush()
				return
			}
			batch = append(batch, r)
			memBytes += r.Size()
			if len(batch) >= pkgConfig.BatchSize || memBytes >= pkgConfig.MaxBufferMB*1024*1024 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func dispatch(batch []*AuditRecord) {
	if testDispatch != nil {
		testDispatch(batch)
		return
	}
	sent, err := awsClient.putRecordBatch(context.Background(), batch)
	if err != nil {
		unsent := batch[sent:]
		logger.SysError(fmt.Sprintf("audit: putRecordBatch 失败 (sent=%d, unsent=%d)，转落盘: %s", sent, len(unsent), err.Error()))
		spillBatch(unsent)
	}
}

func spillBatch(batch []*AuditRecord) {
	var buf bytes.Buffer
	for _, r := range batch {
		buf.WriteString(toNDJSONLine(r))
	}
	if _, err := spill.write(buf.Bytes()); err != nil {
		atomicAddDropped(int64(len(batch)))
		logger.SysError(fmt.Sprintf("audit: 磁盘缓冲已满，丢弃 %d 条记录: %s", len(batch), err.Error()))
	}
}

func uploaderLoop(ctx context.Context) {
	defer func() { recover() }()
	ticker := time.NewTicker(pkgConfig.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			files, _ := spill.scan()
			for _, f := range files {
				records, err := readSpillFile(f)
				if err != nil {
					logger.SysError("audit: 读取 spill 文件失败: " + err.Error())
					continue
				}
				if len(records) == 0 {
					_ = os.Remove(f)
					continue
				}
				sent, err := awsClient.putRecordBatch(context.Background(), records)
				if err != nil {
					logger.SysError(fmt.Sprintf("audit: spill 重放部分失败 (sent=%d, total=%d)，保留待重试: %s", sent, len(records), err.Error()))
					if sent > 0 {
						spillBatch(records[sent:])
					}
					continue
				}
				_ = os.Remove(f)
			}
		}
	}
}

func readSpillFile(path string) ([]*AuditRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	raw, err := io.ReadAll(gr)
	if err != nil {
		return nil, err
	}
	var records []*AuditRecord
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row firehoseRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		r := rowToRecord(row)
		records = append(records, r)
	}
	return records, nil
}

func rowToRecord(row firehoseRow) *AuditRecord {
	t, _ := time.Parse(timeFormatISO, row.EventTime)
	return &AuditRecord{
		EventTime:               t,
		XRequestID:              row.XRequestID,
		UserID:                  row.UserID,
		Username:                row.Username,
		ChannelID:               row.ChannelID,
		TokenName:               row.TokenName,
		OriginModel:             row.OriginModel,
		ActualModel:             row.ActualModel,
		IsStream:                row.IsStream,
		StatusCode:              row.StatusCode,
		DurationMS:              row.DurationMS,
		OriginalReqHeaders:      row.OriginalReqHeaders,
		OriginalReqBody:         row.OriginalReqBody,
		ConvertedReqHeaders:     row.ConvertedReqHeaders,
		ConvertedReqBody:        row.ConvertedReqBody,
		ConvertedSameAsOriginal: row.ConvertedSameAsOriginal,
		UpstreamResponse:        row.UpstreamResponse,
		ClientResponse:          row.ClientResponse,
		TruncatedFields:         row.TruncatedFields,
		DroppedNote:             row.DroppedNote,
	}
}
