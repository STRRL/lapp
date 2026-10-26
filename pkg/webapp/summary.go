package webapp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Summary is the machine-readable view of one log's patterns, the same
// facts the pattern list page shows. Printed by `lapp summary --json`.
type Summary struct {
	Service         string           `json:"service"`
	Source          string           `json:"source"`
	Lines           int              `json:"lines"`
	From            *time.Time       `json:"from,omitempty"`
	To              *time.Time       `json:"to,omitempty"`
	Slot            string           `json:"slot,omitempty"`
	FilteredLines   int              `json:"filteredLines"`
	FilteredPercent float64          `json:"filteredPercent"`
	StartHere       []SummaryFinding `json:"startHere"`
	Patterns        []SummaryPattern `json:"patterns"`
	Unmatched       SummaryUnmatched `json:"unmatched"`
	Compare         *SummaryCompare  `json:"compare,omitempty"`
}

type SummaryFinding struct {
	Template string `json:"template"`
	Name     string `json:"name"`
	Lines    int    `json:"lines"`
	Reason   string `json:"reason"`
}

type SummaryPattern struct {
	Template  string        `json:"template"`
	Name      string        `json:"name"`
	Lines     int           `json:"lines"`
	Percent   float64       `json:"percent"`
	Level     string        `json:"level,omitempty"`
	Filtered  bool          `json:"filtered"`
	Noise     *SummaryNoise `json:"noise,omitempty"`
	Spike     *SummarySpike `json:"spike,omitempty"`
	FirstSeen *time.Time    `json:"firstSeen,omitempty"`
	SinceLast string        `json:"sinceLast,omitempty"`
	Ratio     float64       `json:"ratio,omitempty"`
	Samples   []string      `json:"samples"`
}

type SummaryNoise struct {
	Suggestion string `json:"suggestion"` // likely
	Reason     string `json:"reason"`
}

type SummarySpike struct {
	At    time.Time `json:"at"`
	Lines int       `json:"lines"`
	Per   string    `json:"per"`
}

type SummaryUnmatched struct {
	Lines   int      `json:"lines"`
	Samples []string `json:"samples"`
}

type SummaryCompare struct {
	Previous string    `json:"previous"`
	At       time.Time `json:"at"`
	New      int       `json:"new"`
	Changed  int       `json:"changed"`
	Gone     []string  `json:"gone"`
}

// SummarizeLog finds patterns in a log (no LLM, nothing kept) and returns
// its summary, applying the saved noise rules for its service.
func SummarizeLog(ctx context.Context, root, service, name string, body io.Reader) (*Summary, error) {
	root, err := resolveRoot(root)
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "lapp-summary-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	d := newDemoAPI(root, "", "")
	if service == "" {
		service = serviceFromFileName(filepath.Base(name))
	} else {
		service = cleanServiceName(service)
	}
	if err := d.landLog(ctx, filepath.Join(scratch, "ws"), filepath.Base(name), service, body); err != nil {
		return nil, err
	}
	a, err := (&demoAPI{root: scratch, rulesDir: d.rulesDir, jobs: d.jobs}).analyze(ctx, "ws")
	if err != nil {
		return nil, err
	}
	s := buildSummary(a)
	s.Source = filepath.Base(name)
	return s, nil
}

// SummarizeWorkspace summarizes an existing import, including the
// comparison with the previous import of the same service.
func SummarizeWorkspace(ctx context.Context, root, id string) (*Summary, error) {
	root, err := resolveRoot(root)
	if err != nil {
		return nil, err
	}
	a, err := newDemoAPI(root, "", "").analyze(ctx, id)
	if err != nil {
		return nil, err
	}
	return buildSummary(a), nil
}

func buildSummary(a *analysis) *Summary {
	r := a.resp
	total := max(r.Workspace.LineCount, 1)
	s := &Summary{
		Service: r.Workspace.Service, Source: r.Workspace.Title, Lines: r.Workspace.LineCount,
		From: r.From, To: r.To, StartHere: []SummaryFinding{}, Patterns: []SummaryPattern{},
		Unmatched: SummaryUnmatched{Lines: r.Unmatched.Count, Samples: r.Unmatched.Samples[:min(20, len(r.Unmatched.Samples))]},
	}
	if r.BucketSeconds > 0 {
		s.Slot = bucketUnit(r.BucketSeconds)
	}
	slotTime := func(i int) time.Time {
		return r.BucketStart.Add(time.Duration(int64(i)*r.BucketSeconds) * time.Second)
	}
	for _, p := range r.Patterns {
		sp := SummaryPattern{
			Template: p.Template, Name: p.Name, Lines: p.Count, Percent: pct1(p.Count, total), Level: p.Level,
			Filtered: p.Filtered, SinceLast: p.SinceLast, Ratio: p.Ratio, Samples: p.Samples[:min(3, len(p.Samples))],
		}
		if p.Filtered {
			s.FilteredLines += p.Count
		}
		if p.Suggestion == "noise" {
			sp.Noise = &SummaryNoise{Suggestion: "likely", Reason: p.Reason}
		}
		if p.SpikeAt != nil && r.BucketStart != nil {
			sp.Spike = &SummarySpike{At: slotTime(*p.SpikeAt), Lines: p.Buckets[*p.SpikeAt], Per: bucketUnit(r.BucketSeconds)}
		}
		if p.NewAt != nil && r.BucketStart != nil {
			t := slotTime(*p.NewAt)
			sp.FirstSeen = &t
		}
		s.Patterns = append(s.Patterns, sp)
	}
	s.FilteredPercent = pct1(s.FilteredLines, total)
	byID := map[string]demoPattern{}
	for _, p := range r.Patterns {
		byID[p.ID] = p
	}
	for _, c := range r.StartHere {
		if p := byID[c.ID]; !p.Filtered && len(s.StartHere) < startHereShown {
			s.StartHere = append(s.StartHere, SummaryFinding{Template: p.Template, Name: p.Name, Lines: p.Count, Reason: c.Reason})
		}
	}
	if c := r.Compare; c != nil {
		sc := &SummaryCompare{Previous: c.PreviousTitle, At: c.PreviousAt, New: c.New, Changed: c.Changed, Gone: []string{}}
		for _, g := range c.Gone {
			sc.Gone = append(sc.Gone, g.Template)
		}
		s.Compare = sc
	}
	return s
}

// startHereShown is how many "Start here" candidates are shown, on the
// page and in `lapp summary`.
const startHereShown = 5

func pct1(part, total int) float64 { return round1(float64(part) * 100 / float64(total)) }

// startHereCandidate is one pattern for the "Start here" card, in order,
// with the reason it is there.
type startHereCandidate struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// startHere is the one place the "Start here" rules live. It ranks every
// pattern, hidden or not, so the page can drop hidden ones and show the next
// without a new request: steady errors (by lines), spiking errors (by spike),
// other errors (new or up since last time first, then by lines), warnings
// (spiking first, then by lines), other spikes, then patterns new since the
// last import of the service (non-errors only from 3 lines). Error means an
// error level or error-like words. A pattern first seen within this import is
// not a reason on its own: everything is new on a service's first import.
func startHere(r demoPatternsResponse) []startHereCandidate {
	isErr := func(p demoPattern) bool { return isErrorLevel(p.Level) || isErrorLike(p.Template) }
	isWarn := func(p demoPattern) bool { return !isErr(p) && p.Level == "warn" }
	spike := func(p demoPattern) int {
		if p.SpikeAt == nil {
			return 0
		}
		return p.Buckets[*p.SpikeAt]
	}
	vsLast := func(p demoPattern) bool { return p.SinceLast == "new" || p.SinceLast == "up" }
	byLines := func(a, b demoPattern) bool { return a.Count > b.Count }
	bySpike := func(a, b demoPattern) bool { return spike(a) > spike(b) }
	first := func(in func(demoPattern) bool, then func(a, b demoPattern) bool) func(a, b demoPattern) bool {
		return func(a, b demoPattern) bool {
			if in(a) != in(b) {
				return in(a)
			}
			return then(a, b)
		}
	}
	steady := func(p demoPattern) bool { return isErr(p) && p.SteadyError && p.SpikeAt == nil }
	groups := []struct {
		in   func(demoPattern) bool
		less func(a, b demoPattern) bool
	}{
		{steady, byLines},
		{func(p demoPattern) bool { return isErr(p) && p.SpikeAt != nil }, bySpike},
		{isErr, first(vsLast, byLines)},
		{isWarn, first(func(p demoPattern) bool { return p.SpikeAt != nil }, func(a, b demoPattern) bool {
			return spike(a) > spike(b) || spike(a) == spike(b) && a.Count > b.Count
		})},
		{func(p demoPattern) bool { return p.SpikeAt != nil }, bySpike},
		{func(p demoPattern) bool { return p.SinceLast == "new" && p.Count >= 3 }, byLines},
	}
	out := []startHereCandidate{}
	seen := map[string]bool{}
	for _, grp := range groups {
		var g []demoPattern
		for _, p := range r.Patterns {
			if !seen[p.ID] && grp.in(p) {
				g = append(g, p)
				seen[p.ID] = true
			}
		}
		sort.SliceStable(g, func(i, j int) bool { return grp.less(g[i], g[j]) })
		for _, p := range g {
			out = append(out, startHereCandidate{ID: p.ID, Reason: startReason(p, r, isErr(p), isWarn(p), steady(p))})
		}
	}
	return out
}

func startReason(p demoPattern, r demoPatternsResponse, isErr, isWarn, steady bool) string {
	share := formatPercent(float64(p.Count)/float64(max(r.Workspace.LineCount, 1))) + " of lines"
	if steady {
		return "Steady error · " + share
	}
	kind := ""
	switch {
	case isErr:
		kind = "Error"
	case isWarn:
		kind = "Warning"
	}
	if p.SpikeAt != nil && r.BucketStart != nil {
		text := fmt.Sprintf("spiked to %d%s %s", p.Buckets[*p.SpikeAt], perUnit(r.BucketSeconds), atSlot(r, *p.SpikeAt))
		if kind == "" {
			return strings.ToUpper(text[:1]) + text[1:]
		}
		return kind + " · " + text
	}
	switch {
	case p.SinceLast == "new" && kind != "":
		return "New " + strings.ToLower(kind) + " since last time"
	case p.SinceLast == "new":
		return "New since last time"
	case p.SinceLast == "up" && kind != "":
		return fmt.Sprintf("%s · %g× last time", kind, p.Ratio)
	case kind != "":
		return kind + " · " + share
	}
	return ""
}

// WriteText prints the summary for people.
func (s *Summary) WriteText(w io.Writer) {
	fmt.Fprintf(w, "%s · %s · %d lines", s.Service, s.Source, s.Lines)
	if s.From != nil && s.To != nil {
		fmt.Fprintf(w, " · %s – %s", fmtTime(*s.From), fmtTime(*s.To))
	}
	hidden := "Nothing hidden"
	if s.FilteredLines > 0 {
		hidden = fmt.Sprintf("%.1f%% hidden as noise", s.FilteredPercent)
	}
	fmt.Fprintf(w, "\n%s · %d patterns · %d unmatched lines\n", hidden, len(s.Patterns), s.Unmatched.Lines)
	if s.Compare != nil {
		fmt.Fprintf(w, "Since last time (%s): %d new · %d gone · %d changed a lot\n", s.Compare.Previous, s.Compare.New, len(s.Compare.Gone), s.Compare.Changed)
	}
	fmt.Fprintln(w, "\nStart here:")
	if len(s.StartHere) == 0 {
		fmt.Fprintln(w, "  Nothing stands out: no errors, spikes or new patterns.")
	}
	for _, f := range s.StartHere {
		fmt.Fprintf(w, "  %6d  %s\n          %s\n", f.Lines, f.Name, f.Reason)
	}
	fmt.Fprintln(w, "\nPatterns:")
	for _, p := range s.Patterns {
		mark := " "
		switch {
		case p.Filtered:
			mark = "~"
		case p.Noise != nil && p.Noise.Suggestion == "likely":
			mark = "?"
		}
		fmt.Fprintf(w, " %s%6d %5.1f%%  %s\n", mark, p.Lines, p.Percent, p.Name)
	}
	fmt.Fprintln(w, "\n(~ = hidden as noise, ? = likely noise)")
}

// Same wording as the page: "Dec 4 06:00" for hourly slots, "Jul 10" for days.
func slotLabel(r demoPatternsResponse, i int) string {
	t := r.BucketStart.Add(time.Duration(int64(i)*r.BucketSeconds) * time.Second)
	if r.BucketSeconds >= 86400 {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 15:04")
}

func atSlot(r demoPatternsResponse, i int) string {
	if r.BucketSeconds >= 86400 {
		return "on " + slotLabel(r, i)
	}
	return "at " + slotLabel(r, i)
}

func perUnit(seconds int64) string {
	return " per " + bucketUnit(seconds)
}
