package parser

import (
	"sync"

	"github.com/trivago/grok"
)

// grokDef maps a template ID to its grok pattern expression.
var grokDefs = []struct {
	id      string
	pattern string
}{
	{"SYSLOG", "%{SYSLOGTIMESTAMP:timestamp} %{SYSLOGHOST:logsource} %{SYSLOGPROG}: %{GREEDYDATA:message}"},
	{"COMMONAPACHE", "%{COMMONAPACHELOG}"},
	{"COMBINEDAPACHE", "%{COMBINEDAPACHELOG}"},
}

type compiledGrokPattern struct {
	id       string
	compiled *grok.CompiledGrok
}

// GrokParser matches log lines against a set of predefined grok patterns.
type GrokParser struct {
	mu       sync.Mutex
	patterns []compiledGrokPattern
	seen     map[string]bool
}

// NewGrokParser creates a GrokParser with pre-compiled patterns.
func NewGrokParser() (*GrokParser, error) {
	g, err := grok.New(grok.Config{
		NamedCapturesOnly: true,
	})
	if err != nil {
		return nil, err
	}

	compiled := make([]compiledGrokPattern, 0, len(grokDefs))
	for _, def := range grokDefs {
		c, err := g.Compile(def.pattern)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, compiledGrokPattern{
			id:       def.id,
			compiled: c,
		})
	}

	return &GrokParser{
		patterns: compiled,
		seen:     make(map[string]bool),
	}, nil
}

// Parse tries each grok pattern in order and returns the first match.
func (p *GrokParser) Parse(content string) Result {
	for _, pat := range p.patterns {
		fields := pat.compiled.ParseString(content)
		if len(fields) == 0 {
			continue
		}

		p.mu.Lock()
		p.seen[pat.id] = true
		p.mu.Unlock()

		return Result{
			Matched:    true,
			TemplateID: pat.id,
			Template:   pat.id,
			Params:     fields,
		}
	}
	return Result{Matched: false}
}

// Templates returns the grok patterns that have matched at least once.
func (p *GrokParser) Templates() []Template {
	p.mu.Lock()
	defer p.mu.Unlock()

	templates := make([]Template, 0, len(p.seen))
	for _, def := range grokDefs {
		if p.seen[def.id] {
			templates = append(templates, Template{
				ID:      def.id,
				Pattern: def.pattern,
			})
		}
	}
	return templates
}
