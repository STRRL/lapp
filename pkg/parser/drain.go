package parser

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jaeyo/go-drain3/pkg/drain3"
)

// DrainParser uses the Drain algorithm to discover log templates online.
type DrainParser struct {
	mu           sync.Mutex
	drain        *drain3.Drain
	clusterUUIDs map[int64]string
}

// NewDrainParser creates a DrainParser with default Drain parameters.
func NewDrainParser() (*DrainParser, error) {
	d, err := drain3.NewDrain(
		drain3.WithDepth(4),
		drain3.WithSimTh(0.4),
	)
	if err != nil {
		return nil, fmt.Errorf("create drain: %w", err)
	}
	return &DrainParser{
		drain:        d,
		clusterUUIDs: make(map[int64]string),
	}, nil
}

// Parse feeds a log line into Drain and returns the matching cluster info.
func (p *DrainParser) Parse(content string) Result {
	p.mu.Lock()
	defer p.mu.Unlock()

	cluster, _, err := p.drain.AddLogMessage(content)
	if err != nil || cluster == nil {
		return Result{Matched: false}
	}

	id, ok := p.clusterUUIDs[cluster.ClusterId]
	if !ok {
		id = uuid.New().String()
		p.clusterUUIDs[cluster.ClusterId] = id
	}

	return Result{
		Matched:   true,
		PatternID: id,
		Pattern:   cluster.GetTemplate(),
	}
}

// Templates returns all Drain clusters discovered so far.
func (p *DrainParser) Templates() []Template {
	p.mu.Lock()
	defer p.mu.Unlock()

	clusters := p.drain.GetClusters()
	templates := make([]Template, 0, len(clusters))
	for _, c := range clusters {
		id, ok := p.clusterUUIDs[c.ClusterId]
		if !ok {
			// Skip clusters not seen during Parse — they have no matching log_entries rows
			continue
		}
		templates = append(templates, Template{
			ID:      id,
			Pattern: c.GetTemplate(),
		})
	}
	return templates
}
