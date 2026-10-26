package victorialogs

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// serviceLabels are the stream labels that name a service, in preference
// order. OpenTelemetry deployments use service.name; Kubernetes and
// Prometheus style setups use app or job.
var serviceLabels = []string{"service.name", "service_name", "service", "app", "job"}

// CombinedQuery narrows a LogsQL filter to the given stream IDs. The filter
// is wrapped in parentheses so its own OR terms keep their meaning.
func CombinedQuery(query string, streamIDs []string) string {
	query = strings.TrimSpace(query)
	if len(streamIDs) == 0 {
		return queryOrAll(query)
	}
	combined := "_stream_id:in(" + strings.Join(streamIDs, ",") + ")"
	if query != "" && query != "*" {
		combined += " (" + query + ")"
	}
	return combined
}

// ServiceLabel returns the raw service name for a stream such as
// {app="api",env="prod"}: the first label value found in serviceLabels, or
// "stream-<hash>" when no such label exists, so the name stays stable.
func ServiceLabel(stream string) string {
	labels := parseStreamLabels(stream)
	for _, key := range serviceLabels {
		if value := strings.TrimSpace(labels[key]); value != "" {
			return value
		}
	}
	sum := sha256.Sum256([]byte(stream))
	return "stream-" + hex.EncodeToString(sum[:])[:8]
}

// parseStreamLabels reads the {k="v",...} form VictoriaLogs uses for the
// _stream field. Malformed input yields the labels parsed so far.
func parseStreamLabels(stream string) map[string]string {
	labels := map[string]string{}
	rest := strings.TrimSpace(stream)
	rest = strings.TrimPrefix(rest, "{")
	rest = strings.TrimSuffix(rest, "}")
	for rest != "" {
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		quoted, err := strconv.QuotedPrefix(after)
		if err != nil {
			break
		}
		value, err := strconv.Unquote(quoted)
		if err != nil {
			break
		}
		labels[strings.TrimSpace(key)] = value
		rest = strings.TrimPrefix(strings.TrimSpace(after[len(quoted):]), ",")
	}
	return labels
}
