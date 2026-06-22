package audit

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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
	if err := gcp.appendRows(context.Background(), batch); err != nil {
		logger.SysError("audit: appendRows 失败，转落盘: " + err.Error())
		spillBatch(batch)
	}
}

func spillBatch(batch []*AuditRecord) {
	var buf bytes.Buffer
	for _, r := range batch {
		buf.WriteString(toNDJSONLine(r))
	}
	if _, err := spill.write(buf.Bytes()); err != nil {
		atomicAddDropped(1)
		logger.SysError("audit: 磁盘缓冲已满，丢弃批次: " + err.Error())
	}
}

func uploaderLoop() {
	defer func() { recover() }()
	ticker := time.NewTicker(pkgConfig.FlushInterval)
	defer ticker.Stop()
	for range ticker.C {
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
			if err := gcp.appendRows(context.Background(), records); err != nil {
				logger.SysError("audit: spill 重放失败，保留待重试: " + err.Error())
				continue
			}
			_ = os.Remove(f)
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
		var row bqRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		r := bqRowToRecord(row)
		records = append(records, r)
	}
	return records, nil
}

func bqRowToRecord(row bqRow) *AuditRecord {
	t, _ := time.Parse("2006-01-02 15:04:05.000000", row.EventTime)
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
