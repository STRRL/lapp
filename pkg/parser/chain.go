package parser

// ChainParser tries multiple parsers in order and returns the first match.
type ChainParser struct {
	parsers []Parser
}

// NewChainParser creates a ChainParser that delegates to the given parsers.
func NewChainParser(parsers ...Parser) *ChainParser {
	return &ChainParser{parsers: parsers}
}

// Parse iterates through parsers and returns the first matched result.
func (c *ChainParser) Parse(content string) Result {
	for _, p := range c.parsers {
		result := p.Parse(content)
		if result.Matched {
			return result
		}
	}
	return Result{Matched: false}
}

// Templates aggregates templates from all parsers.
func (c *ChainParser) Templates() []Template {
	var all []Template
	for _, p := range c.parsers {
		all = append(all, p.Templates()...)
	}
	return all
}
