package semantic

import "regexp"

// Sensitive-looking bits that should not leave the machine by default.
// Keep in sync with frontend/src/preview.tsx, which highlights the same ones.
var sensitive = []struct {
	placeholder string
	re          *regexp.Regexp
}{
	{"<email>", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)},
	{"<ip>", regexp.MustCompile(`(?i)\b(?:\d{1,3}\.){3}\d{1,3}\b|\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b`)},
	{"<token>", regexp.MustCompile(`\b(?:eyJ[\w-]{10,}\.[\w-]{10,}\.[\w-]{5,}|(?:sk|pk|ghp|gho|xox[abp])[-_][A-Za-z0-9_-]{12,}|[A-Za-z0-9+/_-]{32,}={0,2})\b`)},
}

// Mask replaces emails, IP addresses and token-like strings with
// <email>, <ip> and <token>.
func Mask(s string) string {
	for _, m := range sensitive {
		s = m.re.ReplaceAllString(s, m.placeholder)
	}
	return s
}

// MaskInputs returns copies of the inputs with patterns and samples masked.
func MaskInputs(in []PatternInput) []PatternInput {
	out := make([]PatternInput, len(in))
	for i, p := range in {
		samples := make([]string, len(p.Samples))
		for j, s := range p.Samples {
			samples[j] = Mask(s)
		}
		out[i] = PatternInput{PatternUUIDString: p.PatternUUIDString, Pattern: Mask(p.Pattern), Samples: samples}
	}
	return out
}
