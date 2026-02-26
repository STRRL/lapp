package parser

// LLMParser is a placeholder for future LLM-based log parsing.
type LLMParser struct{}

// NewLLMParser creates a new LLMParser stub.
func NewLLMParser() *LLMParser {
	return &LLMParser{}
}

// Parse always returns an unmatched result.
// The real implementation will call an LLM to identify templates.
func (p *LLMParser) Parse(content string) Result {
	return Result{Matched: false}
}

// Templates returns an empty slice.
func (p *LLMParser) Templates() []Template {
	return nil
}
