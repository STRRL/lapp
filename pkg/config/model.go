package config

import "os"

// DefaultModel is the fallback LLM model when none is specified.
const DefaultModel = "google/gemini-3-flash-preview"

// ResolveModel returns the model to use, checking the explicit value first,
// then the MODEL_NAME environment variable, and finally the default.
func ResolveModel(model string) string {
	if model != "" {
		return model
	}
	if env := os.Getenv("MODEL_NAME"); env != "" {
		return env
	}
	return DefaultModel
}
