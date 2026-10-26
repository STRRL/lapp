package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func colorPatterns(templates ...string) []demoPattern {
	out := make([]demoPattern, len(templates))
	for i, t := range templates {
		out[i] = demoPattern{ID: t, Template: t, Count: len(templates) - i}
	}
	return out
}

func colorsOf(ps []demoPattern) map[string]int {
	out := map[string]int{}
	for _, p := range ps {
		out[p.Template] = p.Color
	}
	return out
}

func TestAssignColorsFirstSeenPicksUnusedThenLeastUsed(t *testing.T) {
	ps := colorPatterns("a", "b", "c")
	saved, changed := assignColors(nil, ps)
	if !changed || len(saved) != 3 {
		t.Fatalf("saved = %v, changed = %v", saved, changed)
	}
	if got := colorsOf(ps); got["a"] == got["b"] || got["b"] == got["c"] || got["a"] == got["c"] {
		t.Fatalf("new patterns share a color: %v", got)
	}

	// Ten patterns: the first eight get eight different colors, the rest
	// the least-used ones.
	ps = colorPatterns("p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9")
	saved, _ = assignColors(nil, ps)
	seen := map[int]bool{}
	for _, p := range ps[:8] {
		seen[p.Color] = true
	}
	if len(seen) != 8 {
		t.Fatalf("top 8 colors = %v", colorsOf(ps))
	}
	if ps[8].Color == ps[9].Color {
		t.Fatalf("p8 and p9 should take different least-used colors: %v", saved)
	}
}

func TestAssignColorsStableWhenRanksChange(t *testing.T) {
	first := colorPatterns("a", "b", "c", "d")
	saved, _ := assignColors(nil, first)
	want := colorsOf(first)

	// Next import: ranks reversed, one pattern gone, one new.
	second := colorPatterns("d", "e", "c", "a")
	saved2, changed := assignColors(saved, second)
	if !changed {
		t.Fatal("new pattern e should be saved")
	}
	got := colorsOf(second)
	for _, tpl := range []string{"a", "c", "d"} {
		if got[tpl] != want[tpl] {
			t.Fatalf("%s changed color: %d → %d", tpl, want[tpl], got[tpl])
		}
	}
	if saved2["b"] != want["b"] {
		t.Fatalf("absent pattern b lost its color: %v", saved2)
	}
	for _, tpl := range []string{"a", "c", "d"} {
		if got["e"] == got[tpl] {
			t.Fatalf("new pattern e took %s's color: %v", tpl, got)
		}
	}
	if _, changed := assignColors(saved2, second); changed {
		t.Fatal("same patterns again should change nothing")
	}
}

func TestAssignColorsReassignsLowerRankedCollisionOnce(t *testing.T) {
	saved := map[string]int{"a": 2, "b": 2, "c": 5}
	ps := colorPatterns("b", "a", "c")
	saved, changed := assignColors(saved, ps)
	got := colorsOf(ps)
	if !changed || got["b"] != 2 || got["a"] == 2 || got["a"] == 5 || got["c"] != 5 {
		t.Fatalf("colors = %v, changed = %v", got, changed)
	}
	if saved["a"] != got["a"] {
		t.Fatalf("reassignment not saved: %v", saved)
	}
	if _, changed := assignColors(saved, colorPatterns("a", "b", "c")); changed {
		t.Fatal("a fixed collision should stay fixed when ranks change")
	}

	// Hidden patterns are not on screen, so they do not collide.
	ps = colorPatterns("x", "y")
	ps[1].Filtered = true
	if _, changed := assignColors(map[string]int{"x": 1, "y": 1}, ps); changed {
		t.Fatal("hidden pattern should keep its color")
	}
}

func TestDemoPatternColorsPersistPerService(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	d := newDemoAPI(root, "", "")
	mux := http.NewServeMux()
	d.register(mux)
	ctx := context.Background()
	logOf := func(counts map[string]int) string {
		var b strings.Builder
		base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		i := 0
		for _, word := range []string{"health", "cache", "payment", "login"} {
			for j := 0; j < counts[word]; j++ {
				fmt.Fprintf(&b, "%s INFO %s step done in %d ms\n", base.Add(time.Duration(i)*time.Second).Format(time.RFC3339), word, j)
				i++
			}
		}
		return b.String()
	}
	patterns := func(ws string) map[string]demoPattern {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demo/patterns?ws="+ws, nil))
		var resp demoPatternsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("patterns: %d %s", rec.Code, rec.Body)
		}
		out := map[string]demoPattern{}
		for _, p := range resp.Patterns {
			out[strings.Fields(p.Template)[1]] = p
		}
		return out
	}

	first, err := d.importLog(ctx, "api-1.log", strings.NewReader(logOf(map[string]int{"health": 40, "cache": 30, "payment": 20})))
	if err != nil {
		t.Fatal(err)
	}
	one := patterns(first)
	time.Sleep(10 * time.Millisecond)
	second, err := d.importLog(ctx, "api-2.log", strings.NewReader(logOf(map[string]int{"health": 5, "cache": 50, "payment": 60, "login": 30})))
	if err != nil {
		t.Fatal(err)
	}
	two := patterns(second)
	for _, w := range []string{"health", "cache", "payment"} {
		if one[w].Color != two[w].Color {
			t.Fatalf("%s changed color across imports: %d → %d", w, one[w].Color, two[w].Color)
		}
		if two["login"].Color == two[w].Color {
			t.Fatalf("new pattern login took %s's color %d", w, two[w].Color)
		}
	}

	// Hide clicks and page loads at the same time keep both the decisions
	// and the colors.
	var wg sync.WaitGroup
	for _, w := range []string{"health", "cache", "payment"} {
		wg.Add(2)
		go func(id string) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{"patterns": []string{id}, "decision": "hide"})
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/demo/rules?ws="+second, strings.NewReader(string(body))))
			if rec.Code != http.StatusOK {
				t.Errorf("hide: %d %s", rec.Code, rec.Body)
			}
		}(two[w].ID)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/demo/patterns?ws="+second, nil))
		}()
	}
	wg.Wait()
	rules, _ := d.loadRules("api")
	if len(rules.Filtered) != 3 || len(rules.Colors) != 4 {
		t.Fatalf("rules after concurrent hides: filtered=%v colors=%v", rules.Filtered, rules.Colors)
	}
	for w, p := range patterns(second) {
		if p.Color != two[w].Color {
			t.Fatalf("%s changed color after hides: %d → %d", w, two[w].Color, p.Color)
		}
	}
}
