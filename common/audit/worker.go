package audit

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
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
	var buf bytes.Buffer
	for _, r := range batch {
		buf.WriteString(toNDJSONLine(r))
	}
	// 内存够：直接走内存→GCS；否则落盘
	if buf.Len() < pkgConfig.MaxBufferMB*1024*1024 {
		gz := gzipBytes(buf.Bytes())
		obj := fmt.Sprintf("audit/%s/%d.ndjson.gz", time.Now().UTC().Format("2006/01/02"), time.Now().UnixNano())
		if err := gcp.uploadAndLoad(context.Background(), obj, gz); err != nil {
			logger.SysError("audit: 内存直传失败，转落盘: " + err.Error())
			spillBatch(buf.Bytes())
		}
		return
	}
	spillBatch(buf.Bytes())
}

func spillBatch(ndjson []byte) {
	if _, err := spill.write(ndjson); err != nil {
		// 磁盘也满 → 丢弃 + 计数
		atomicAddDropped(1)
		logger.SysError("audit: 磁盘缓冲已满，丢弃批次: " + err.Error())
	}
}

func gzipBytes(b []byte) []byte {
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	_, _ = gw.Write(b)
	_ = gw.Close()
	return out.Bytes()
}

func uploaderLoop() {
	defer func() { recover() }()
	ticker := time.NewTicker(pkgConfig.FlushInterval)
	defer ticker.Stop()
	for range ticker.C {
		files, _ := spill.scan()
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			// spill 文件已是 gzip；按统一对象名上传
			obj := fmt.Sprintf("audit/%s/spill-%d.ndjson.gz", time.Now().UTC().Format("2006/01/02"), time.Now().UnixNano())
			if err := gcp.uploadAndLoad(context.Background(), obj, data); err != nil {
				logger.SysError("audit: spill 文件上传失败，保留待重试: " + err.Error())
				continue
			}
			_ = os.Remove(f)
		}
	}
}
