package parser

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// JSONParser detects JSON-formatted log lines and extracts structure.
type JSONParser struct {
	mu   sync.Mutex
	seen map[string]Template
}

// NewJSONParser creates a new JSONParser.
func NewJSONParser() *JSONParser {
	return &JSONParser{
		seen: make(map[string]Template),
	}
}

// Parse checks if the content is valid JSON and extracts template info.
func (p *JSONParser) Parse(content string) Result {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || trimmed[0] != '{' {
		return Result{Matched: false}
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return Result{Matched: false}
	}

	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tid := hashKeys(keys)

	// Extract message field if present
	tmpl := strings.Join(keys, ", ")
	msg := extractMessageField(obj)
	if msg != "" {
		tmpl = msg
	}

	p.mu.Lock()
	if _, exists := p.seen[tid]; !exists {
		p.seen[tid] = Template{
			ID:      tid,
			Pattern: strings.Join(keys, ", "),
		}
	}
	p.mu.Unlock()

	params := make(map[string]string, len(obj))
	for k, v := range obj {
		params[k] = string(v)
	}

	return Result{
		Matched:   true,
		PatternID: tid,
		Pattern:   tmpl,
		Params:    params,
	}
}

// Templates returns all observed JSON structures.
func (p *JSONParser) Templates() []Template {
	p.mu.Lock()
	defer p.mu.Unlock()

	templates := make([]Template, 0, len(p.seen))
	for _, t := range p.seen {
		templates = append(templates, t)
	}
	return templates
}

// extractMessageField looks for common message field names in a JSON object.
func extractMessageField(obj map[string]json.RawMessage) string {
	for _, key := range []string{"message", "msg"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return ""
}

// hashKeys returns a short hex hash of the sorted key list.
func hashKeys(keys []string) string {
	h := sha256.Sum256([]byte(strings.Join(keys, "\x00")))
	return fmt.Sprintf("J%x", h[:6])
}
