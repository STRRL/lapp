package pattern

import (
	"sync"

	"github.com/go-errors/errors"
	"github.com/google/uuid"
	"github.com/jaeyo/go-drain3/pkg/drain3"
)

// DrainParser uses the Drain algorithm to discover log templates online.
type DrainParser struct {
	mu    sync.Mutex
	drain *drain3.Drain
	// clusterUUIDs maps Drain cluster IDs to stable UUIDs for consistent template identification.
	clusterUUIDs map[int64]uuid.UUID
}

// NewDrainParser creates a DrainParser with default Drain parameters.
func NewDrainParser() (*DrainParser, error) {
	d, err := drain3.NewDrain(
		drain3.WithDepth(4),
		drain3.WithSimTh(0.4),
		drain3.WithExtraDelimiter([]string{"|", "=", ","}),
	)
	if err != nil {
		return nil, errors.Errorf("create drain: %w", err)
	}
	return &DrainParser{
		drain:        d,
		clusterUUIDs: make(map[int64]uuid.UUID),
	}, nil
}

// Feed processes a batch of log lines through the Drain algorithm.
func (p *DrainParser) Feed(contents []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, content := range contents {
		cluster, _, err := p.drain.AddLogMessage(content)
		if err != nil {
			return errors.Errorf("drain add: %w", err)
		}
		if cluster == nil {
			continue
		}
		if _, ok := p.clusterUUIDs[cluster.ClusterId]; !ok {
			p.clusterUUIDs[cluster.ClusterId] = uuid.New()
		}
	}
	return nil
}

// Templates returns all Drain clusters discovered so far with their counts.
func (p *DrainParser) Templates() ([]DrainCluster, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	clusters := p.drain.GetClusters()
	templates := make([]DrainCluster, 0, len(clusters))
	for _, c := range clusters {
		id, ok := p.clusterUUIDs[c.ClusterId]
		if !ok {
			continue
		}
		templates = append(templates, DrainCluster{
			ID:      id,
			Pattern: c.GetTemplate(),
			Count:   int(c.Size),
		})
	}
	return templates, nil
}
