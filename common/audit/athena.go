package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	athenaTypes "github.com/aws/aws-sdk-go-v2/service/athena/types"
)

const (
	athenaPollInterval  = 500 * time.Millisecond
	athenaDefaultMaxWait = 30 * time.Second
)

func athenaDeadline(ctx context.Context) time.Time {
	if dl, ok := ctx.Deadline(); ok {
		return dl
	}
	return time.Now().Add(athenaDefaultMaxWait)
}

func (c *awsAuditClient) executeQuery(ctx context.Context, sql string) (*athena.GetQueryResultsOutput, error) {
	startOut, err := c.ath.StartQueryExecution(ctx, &athena.StartQueryExecutionInput{
		QueryString: aws.String(sql),
		QueryExecutionContext: &athenaTypes.QueryExecutionContext{
			Database: aws.String(c.cfg.AthenaDatabase),
		},
		WorkGroup: aws.String(c.cfg.AthenaWorkgroup),
		ResultConfiguration: &athenaTypes.ResultConfiguration{
			OutputLocation: aws.String(c.cfg.S3OutputLocation),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("athena StartQueryExecution: %w", err)
	}

	qid := startOut.QueryExecutionId

	if err := c.waitForQuery(ctx, qid); err != nil {
		return nil, err
	}

	results, err := c.ath.GetQueryResults(ctx, &athena.GetQueryResultsInput{
		QueryExecutionId: qid,
	})
	if err != nil {
		return nil, fmt.Errorf("athena GetQueryResults: %w", err)
	}
	return results, nil
}

func (c *awsAuditClient) executeQueryAllPages(ctx context.Context, sql string) ([]athenaTypes.Row, []athenaTypes.ColumnInfo, error) {
	startOut, err := c.ath.StartQueryExecution(ctx, &athena.StartQueryExecutionInput{
		QueryString: aws.String(sql),
		QueryExecutionContext: &athenaTypes.QueryExecutionContext{
			Database: aws.String(c.cfg.AthenaDatabase),
		},
		WorkGroup: aws.String(c.cfg.AthenaWorkgroup),
		ResultConfiguration: &athenaTypes.ResultConfiguration{
			OutputLocation: aws.String(c.cfg.S3OutputLocation),
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("athena StartQueryExecution: %w", err)
	}

	qid := startOut.QueryExecutionId

	if err := c.waitForQuery(ctx, qid); err != nil {
		return nil, nil, err
	}

	var allRows []athenaTypes.Row
	var columns []athenaTypes.ColumnInfo
	var nextToken *string
	first := true

	for {
		input := &athena.GetQueryResultsInput{
			QueryExecutionId: qid,
			NextToken:        nextToken,
		}
		results, err := c.ath.GetQueryResults(ctx, input)
		if err != nil {
			return nil, nil, fmt.Errorf("athena GetQueryResults: %w", err)
		}
		if first {
			columns = results.ResultSet.ResultSetMetadata.ColumnInfo
			if len(results.ResultSet.Rows) > 1 {
				allRows = append(allRows, results.ResultSet.Rows[1:]...)
			}
			first = false
		} else {
			allRows = append(allRows, results.ResultSet.Rows...)
		}
		if results.NextToken == nil {
			break
		}
		nextToken = results.NextToken
	}

	return allRows, columns, nil
}

func (c *awsAuditClient) waitForQuery(ctx context.Context, qid *string) error {
	deadline := athenaDeadline(ctx)
	for {
		if time.Now().After(deadline) {
			_, _ = c.ath.StopQueryExecution(ctx, &athena.StopQueryExecutionInput{
				QueryExecutionId: qid,
			})
			return fmt.Errorf("athena query timed out (deadline %v)", deadline.Format(time.RFC3339))
		}

		statusOut, err := c.ath.GetQueryExecution(ctx, &athena.GetQueryExecutionInput{
			QueryExecutionId: qid,
		})
		if err != nil {
			return fmt.Errorf("athena GetQueryExecution: %w", err)
		}

		state := statusOut.QueryExecution.Status.State
		switch state {
		case athenaTypes.QueryExecutionStateSucceeded:
			return nil
		case athenaTypes.QueryExecutionStateFailed:
			reason := ""
			if statusOut.QueryExecution.Status.StateChangeReason != nil {
				reason = *statusOut.QueryExecution.Status.StateChangeReason
			}
			return fmt.Errorf("athena query failed: %s", reason)
		case athenaTypes.QueryExecutionStateCancelled:
			return fmt.Errorf("athena query cancelled")
		default:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(athenaPollInterval):
			}
		}
	}
}
