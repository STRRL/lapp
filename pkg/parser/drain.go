package parser

import (
	"fmt"
	"sync"

	"github.com/jaeyo/go-drain3/pkg/drain3"
)

// DrainParser uses the Drain algorithm to discover log templates online.
type DrainParser struct {
	mu    sync.Mutex
	drain *drain3.Drain
}

// NewDrainParser creates a DrainParser with default Drain parameters.
func NewDrainParser() *DrainParser {
	d, _ := drain3.NewDrain(
		drain3.WithDepth(4),
		drain3.WithSimTh(0.4),
	)
	return &DrainParser{drain: d}
}

// Parse feeds a log line into Drain and returns the matching cluster info.
func (p *DrainParser) Parse(content string) Result {
	p.mu.Lock()
	defer p.mu.Unlock()

	cluster, _, err := p.drain.AddLogMessage(content)
	if err != nil || cluster == nil {
		return Result{Matched: false}
	}

	return Result{
		Matched:    true,
		TemplateID: fmt.Sprintf("D%d", cluster.ClusterId),
		Template:   cluster.GetTemplate(),
	}
}

// Templates returns all Drain clusters discovered so far.
func (p *DrainParser) Templates() []Template {
	p.mu.Lock()
	defer p.mu.Unlock()

	clusters := p.drain.GetClusters()
	templates := make([]Template, 0, len(clusters))
	for _, c := range clusters {
		templates = append(templates, Template{
			ID:      fmt.Sprintf("D%d", c.ClusterId),
			Pattern: c.GetTemplate(),
		})
	}
	return templates
}
