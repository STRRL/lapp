package gcplog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	loggingv2 "cloud.google.com/go/logging/apiv2"
	loggingpb "cloud.google.com/go/logging/apiv2/loggingpb"
	"github.com/go-errors/errors"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
)

// adcHint is appended to fetch failures so credential problems point people
// at Application Default Credentials setup. LAPP never manages provider auth.
const adcHint = "if this is a credential problem, run `gcloud auth application-default login`"

const (
	maxPageSize   = 10_000
	pageTimeout   = 5 * time.Minute
	maxRetryDelay = time.Minute
)

// FetchRequest describes one pull from GCP Cloud Logging. Filter is the user
// supplied Cloud Logging filter; the time range is combined with it when
// querying. Limit caps the number of returned entries.
type FetchRequest struct {
	Project string
	Filter  string
	From    time.Time
	To      time.Time
	Limit   int
}

// FetchResult reports facts about one completed fetch.
type FetchResult struct {
	Truncated bool
}

// Fetch pulls matching entries from GCP Cloud Logging using Application
// Default Credentials and sends each converted NDJSON line to writeLine.
func Fetch(ctx context.Context, req FetchRequest, writeLine func(string) error) (FetchResult, error) {
	if writeLine == nil {
		return FetchResult{}, errors.New("log line writer is required")
	}
	client, err := loggingv2.NewClient(ctx)
	if err != nil {
		return FetchResult{}, errors.Errorf("create cloud logging client (%s): %w", adcHint, err)
	}
	defer func() { _ = client.Close() }()

	it := client.ListLogEntries(ctx, &loggingpb.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + req.Project},
		Filter:        CombinedFilter(req.Filter, req.From, req.To),
		PageSize:      pageSize(req.Limit),
	}, gax.WithTimeout(pageTimeout), gax.WithRetry(newListRetryer))

	var result FetchResult
	count := 0
	for {
		entry, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return FetchResult{}, errors.Errorf("list cloud logging entries (%s): %w", adcHint, err)
		}
		if req.Limit > 0 && count >= req.Limit {
			result.Truncated = true
			break
		}
		converted, err := entryFromProto(entry)
		if err != nil {
			return FetchResult{}, err
		}
		line, err := ConvertEntry(converted)
		if err != nil {
			return FetchResult{}, err
		}
		if err := writeLine(line); err != nil {
			return FetchResult{}, errors.Errorf("write cloud logging entry: %w", err)
		}
		count++
	}
	return result, nil
}

func pageSize(limit int) int32 {
	if limit > 0 && limit < maxPageSize {
		return int32(limit + 1)
	}
	return maxPageSize
}

func newListRetryer() gax.Retryer {
	return gax.OnCodes([]codes.Code{
		codes.ResourceExhausted,
		codes.DeadlineExceeded,
		codes.Internal,
		codes.Unavailable,
	}, gax.Backoff{
		Initial:    time.Second,
		Max:        maxRetryDelay,
		Multiplier: 2,
	})
}

// CombinedFilter joins the resolved time range with the user supplied filter.
// The time range is a separate concept from the filter; they are only
// combined here at query time.
func CombinedFilter(filter string, from, to time.Time) string {
	parts := []string{
		fmt.Sprintf("timestamp >= %q", from.UTC().Format(time.RFC3339Nano)),
		fmt.Sprintf("timestamp <= %q", to.UTC().Format(time.RFC3339Nano)),
	}
	if strings.TrimSpace(filter) != "" {
		parts = append(parts, "("+filter+")")
	}
	return strings.Join(parts, " AND ")
}

// entryFromProto maps a provider entry onto the neutral Entry shape consumed
// by ConvertEntry.
func entryFromProto(e *loggingpb.LogEntry) (Entry, error) {
	out := Entry{}
	if timestamp := e.GetTimestamp(); timestamp != nil {
		if err := timestamp.CheckValid(); err != nil {
			return Entry{}, errors.Errorf("invalid cloud logging timestamp: %w", err)
		}
		out.Timestamp = timestamp.AsTime()
	}
	if severity := strings.ToUpper(e.GetSeverity().String()); severity != DefaultSeverity {
		out.Severity = severity
	}
	switch payload := e.Payload.(type) {
	case nil:
	case *loggingpb.LogEntry_TextPayload:
		out.TextPayload = &payload.TextPayload
	case *loggingpb.LogEntry_JsonPayload:
		if payload.JsonPayload == nil {
			break
		}
		if data, err := protojson.Marshal(payload.JsonPayload); err == nil {
			out.JSONPayload = data
		} else {
			text := fmt.Sprint(payload)
			out.TextPayload = &text
		}
	case *loggingpb.LogEntry_ProtoPayload:
		if payload.ProtoPayload == nil {
			break
		}
		message, err := payload.ProtoPayload.UnmarshalNew()
		if err == nil {
			data, marshalErr := protojson.Marshal(message)
			if marshalErr == nil {
				out.JSONPayload = data
				break
			}
		}
		data, marshalErr := json.Marshal(map[string]string{
			"@type": payload.ProtoPayload.GetTypeUrl(),
			"value": base64.StdEncoding.EncodeToString(payload.ProtoPayload.GetValue()),
		})
		if marshalErr == nil {
			out.JSONPayload = data
		} else {
			text := fmt.Sprint(payload)
			out.TextPayload = &text
		}
	default:
		return Entry{}, errors.Errorf("unknown cloud logging payload type %T", e.Payload)
	}
	return out, nil
}
