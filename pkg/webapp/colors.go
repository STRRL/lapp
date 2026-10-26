package webapp

import "github.com/go-errors/errors"

const (
	// seriesColorCount is the number of series colors, --s1…--s8 in
	// frontend/src/design.css.
	seriesColorCount = 8
	// colorScreenTop is how many visible patterns are told apart on one
	// screen: the volume chart's top patterns.
	colorScreenTop = 8
)

// withColors sets every pattern's Color from the service's saved colors and
// saves the colors of patterns seen for the first time (and of collision
// fixes). Only the page's pattern responses call it, so summaries and
// exports never write rules.
func (d *demoAPI) withColors(a *analysis) error {
	rules, _ := d.loadRules(a.service)
	if _, changed := assignColors(rules.Colors, a.resp.Patterns); !changed {
		return nil
	}
	// Assign again on the rules loaded under rulesMu, so a concurrent Hide
	// or another page load never drops a decision or a color.
	if err := d.updateRules(a.service, func(r *noiseRules) { r.Colors, _ = assignColors(r.Colors, a.resp.Patterns) }); err != nil {
		return errors.Errorf("save pattern colors: %w", err)
	}
	return nil
}

// assignColors gives each pattern (sorted by lines, most first) a color
// index that is fixed by its template within a service:
//   - a template seen for the first time gets a color none of the top
//     visible patterns uses, or, when all are used, the least-used one;
//   - a saved color never changes, except when two of the top visible
//     patterns share one: the lower-ranked one is reassigned once.
//
// It returns the updated map (saved is not modified) and whether it changed.
func assignColors(saved map[string]int, patterns []demoPattern) (map[string]int, bool) {
	colors := make(map[string]int, len(saved)+len(patterns))
	for t, c := range saved {
		if c >= 0 && c < seriesColorCount {
			colors[t] = c
		}
	}
	changed := len(colors) != len(saved)

	var top []string
	for _, p := range patterns {
		if !p.Filtered && len(top) < colorScreenTop {
			top = append(top, p.Template)
		}
	}
	// pick returns the least-used color among those no other top pattern
	// uses (all colors when every one is taken); ties go to the lowest index.
	pick := func(self string) int {
		inTop := make([]bool, seriesColorCount)
		free := false
		for _, t := range top {
			if c, ok := colors[t]; ok && t != self {
				inTop[c] = true
			}
		}
		for _, used := range inTop {
			free = free || !used
		}
		uses := make([]int, seriesColorCount)
		for _, c := range colors {
			uses[c]++
		}
		best := -1
		for c := range seriesColorCount {
			if free && inTop[c] {
				continue
			}
			if best < 0 || uses[c] < uses[best] {
				best = c
			}
		}
		return best
	}

	for _, p := range patterns {
		if _, ok := colors[p.Template]; !ok {
			colors[p.Template] = pick(p.Template)
			changed = true
		}
	}
	seen := make([]bool, seriesColorCount)
	for _, t := range top {
		if seen[colors[t]] {
			colors[t] = pick(t)
			changed = true
		}
		seen[colors[t]] = true
	}
	for i := range patterns {
		patterns[i].Color = colors[patterns[i].Template]
	}
	return colors, changed
}
