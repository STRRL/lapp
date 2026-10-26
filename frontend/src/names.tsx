import React from "react";

// The one way a pattern name is shown: on one line, cut to n characters, and
// apart from every other pattern in the workspace. Differences in spaces and
// punctuation don't count; names are compared word by word.
//   - When a name shares more than SHARED_START characters with another, it
//     keeps a short start that says what kind of log it is, then skips to
//     where the names differ: "duration: <*> ms plan: …pgrst_call.pgrst_scalar…".
//     Compact places (legend, chart tooltip) drop the start: "…pgrst_call…".
//   - If two labels still read the same, the words where the names differ are
//     put in front until they don't ("READ …tenantA …alpha").
// fields maps each <*> in text to its field index in the full name, for Template.
export type PatternLabel = { text: string; fields: number[] };
export type LabelOf = (p: { id: string; name: string }, n: number, compact?: boolean) => PatternLabel;

// Character budgets per place. The list and Start here are wide: their names
// run to the column edge and CSS cuts them (text-overflow), so NAME_ROW only
// bounds very long templates.
export const NAME_ROW = 300;
export const NAME_NOTICE = 40;
export const NAME_LEGEND = 32;
export const NAME_TIP = 38;

const SHARED_START = 24;

type Token = { t: string; start: number; end: number };

function tokens(s: string): Token[] {
  return [...s.matchAll(/<\*>|[\p{L}\p{N}_]+/gu)].map((m) => ({ t: m[0], start: m.index!, end: m.index! + m[0].length }));
}

// Cuts s to n characters with a trailing "…", never inside a <*> field.
function cutName(s: string, n: number) {
  if (s.length <= n) return s;
  const field = s.lastIndexOf("<*>", n - 1);
  const end = field >= 0 && field + 3 > n ? field : n;
  return `${s.slice(0, end)}…`;
}

const fieldsBefore = (name: string, i: number) => name.slice(0, i).split("<*>").length - 1;

// The label shown from position from of name, after the head ranges.
function labelFrom(name: string, from: number, n: number, heads: [number, number][]): PatternLabel {
  const fields: number[] = [];
  let text = "";
  let shown = 0;
  for (const [a, b] of heads) {
    const h = name.slice(a, b).trim();
    text += `${a > shown ? "…" : ""}${h} `;
    h.split("<*>").slice(1).forEach((_, k) => fields.push(fieldsBefore(name, a) + k));
    shown = b;
  }
  const rest = from === 0 ? cutName(name, n) : `…${cutName(name.slice(from), Math.max(8, n - 1 - text.length))}`;
  rest.split("<*>").slice(1).forEach((_, k) => fields.push(fieldsBefore(name, from) + k));
  return { text: text + rest, fields };
}

// Groups indexes whose labels read the same but whose names differ.
function collisions(names: string[], labels: PatternLabel[]) {
  const by = new Map<string, number[]>();
  labels.forEach((l, i) => by.set(l.text, [...(by.get(l.text) ?? []), i]));
  return [...by.values()].filter((g) => new Set(g.map((i) => names[i])).size > 1);
}

export function patternLabels(names: string[], n: number, compact = false): PatternLabel[] {
  const flat = names.map((s) => s.replace(/\s+/g, " ").trim());
  const toks = flat.map(tokens);
  // Start of the first word where name i differs from name j.
  const differ = (i: number, j: number, after = (_k: number) => true) => {
    const a = toks[i];
    const b = toks[j];
    for (let k = 0; k < a.length; k++) if ((k >= b.length || a[k].t !== b[k].t) && after(k)) return k;
    return a.length;
  };
  // Where word k starts, taking in punctuation just before it ('"public"').
  const at = (i: number, k: number) => {
    if (k >= toks[i].length) return flat[i].length;
    const start = toks[i][k].start;
    const word = flat[i].lastIndexOf(" ", start - 1) + 1;
    return start - word <= 3 && (k === 0 || word >= toks[i][k - 1].end) ? word : start;
  };
  // Where the word ending at end really ends, taking in punctuation after it ("plan:").
  const wordEnd = (i: number, end: number) => {
    const space = flat[i].indexOf(" ", end);
    return space >= 0 && space - end <= 3 ? space : end;
  };

  const from = flat.map(() => 0);
  const heads: [number, number][][] = flat.map(() => []);
  const cut = flat.map((s) => cutName(s, n));
  flat.forEach((name, i) => {
    let shared = 0;
    flat.forEach((other, j) => {
      if (other !== name) shared = Math.max(shared, at(i, differ(i, j)));
    });
    if (shared >= name.length) return;
    const collides = cut.some((c, j) => j !== i && c === cut[i] && flat[j] !== name);
    if (shared <= SHARED_START && !collides) return;
    from[i] = shared;
    if (compact || shared <= SHARED_START) return;
    // Keep the whole words that fit in SHARED_START as the start.
    const keep = toks[i].filter((t) => t.end <= SHARED_START && t.end < shared);
    if (keep.length > 0) heads[i] = [[0, Math.min(wordEnd(i, keep[keep.length - 1].end), shared)]];
  });
  const labels = flat.map((name, i) => labelFrom(name, from[i], n, heads[i]));

  // Same label from different names: put the earliest word not shown yet
  // where they differ in front, until every label reads differently.
  const shown = (i: number, pos: number) => pos >= from[i] || heads[i].some(([a, b]) => pos >= a && pos < b);
  for (let round = 0; round < 6; round++) {
    const groups = collisions(flat, labels);
    if (groups.length === 0) break;
    let changed = false;
    for (const group of groups) {
      for (const i of group) {
        let k = toks[i].length;
        for (const j of group) {
          if (flat[j] !== flat[i]) k = Math.min(k, differ(i, j, (x) => !shown(i, toks[i][x].start)));
        }
        if (k >= toks[i].length || toks[i][k].start >= from[i]) continue;
        heads[i] = [...heads[i], [toks[i][k].start, toks[i][k].end] as [number, number]].sort((x, y) => x[0] - y[0]);
        labels[i] = labelFrom(flat[i], from[i], n, heads[i]);
        changed = true;
      }
    }
    if (!changed) break;
  }
  return labels;
}

// labelOf for one workspace's patterns: labels are computed once per place.
export function patternNamer(patterns: { id: string; name: string }[]): LabelOf {
  const cache = new Map<string, Map<string, PatternLabel>>();
  return (p, n, compact = false) => {
    const key = `${n}:${compact}`;
    let labels = cache.get(key);
    if (!labels) {
      const list = patternLabels(patterns.map((x) => x.name), n, compact);
      labels = new Map(patterns.map((x, i) => [x.id, list[i]]));
      cache.set(key, labels);
    }
    return labels.get(p.id) ?? patternLabels([p.name], n, compact)[0];
  };
}

// Shows text with each skipped part ("…") in a muted color.
export function Skips({ text }: { text: string }) {
  const parts = text.split("…");
  // One element, so flex parents (the legend) lay it out as one piece of text.
  return (
    <span>
      {parts.map((part, i) => (
        <React.Fragment key={i}>
          {i > 0 && <span className="skip">…</span>}
          {part}
        </React.Fragment>
      ))}
    </span>
  );
}
