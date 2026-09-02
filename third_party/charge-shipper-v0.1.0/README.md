# billship

`billship` is the producer SDK for sending charge source logs to Amazon SQS. It
uses a bounded in-memory buffer, batches up to 10 records per request, retries
transient failures, and exposes delivery statistics.

## Requirements

- Go 1.25 or newer
- An SQS queue and AWS credentials with permission to send messages to it

## Install

```bash
go env -w GOPRIVATE=github.com/changshiaos/charge
go get github.com/changshiaos/charge/server/shipper@v0.1.0
```

The import path ends in `shipper`, while the Go package name is `billship`.
Using an explicit import alias is recommended.

This is a private nested Go module. Release tags use the full subdirectory
prefix (for example `server/shipper/v0.1.0`), while callers require the short
module version (`v0.1.0`). See the repository's
[release and producer CI guide](../../docs/shipper版本发布与生产者CI接入.md) for the
release procedure and a least-privilege GitHub Actions setup.

## Usage

```go
package main

import (
	"context"
	"log"
	"time"

	billship "github.com/changshiaos/charge/server/shipper"
)

func main() {
	err := billship.Init(billship.Config{
		QueueURL:       "https://sqs.us-east-1.amazonaws.com/123456789012/charge",
		Region:         "us-east-1",
		Enabled:        true,
		BufferSize:     10_000,
		BatchSize:      10,
		BatchWait:      200 * time.Millisecond,
		SendConcurrency: 8,
		Logger: func(level, msg string, kv ...any) {
			log.Printf("level=%s msg=%s fields=%v", level, msg, kv)
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	billship.Ship(billship.Record{
		SiteID:     "site-001",
		Model:      "gpt-4.1",
		SourceType: "one-api",
		LogID:      12345,
		CreatedAt:  time.Now().Unix(),
		Body:       []byte(`{"id":12345,"quota":100}`),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := billship.Shutdown(ctx); err != nil {
		log.Printf("billship shutdown: %v", err)
	}
}
```

Call `Init` once during process startup. `Ship` is non-blocking: invalid records
or records submitted while the buffer is full are logged and counted instead
of blocking the caller. Call `Shutdown` during graceful process termination to
flush buffered records. A process cannot initialize the package again after it
has been shut down.

Every `Record` must provide non-empty `SiteID`, `Model`, `SourceType`, and
`Body`. The caller must not mutate `Body` after calling `Ship`, because the SDK
retains the byte slice while sending asynchronously.

Use `billship.Stats()` to obtain a point-in-time `Snapshot` containing enqueue,
drop, send, retry, validation failure, and in-flight counters.

AWS credentials are loaded through the standard AWS SDK for Go v2 credential
chain, including environment variables, shared configuration files, and
workload IAM roles.
