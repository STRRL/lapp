import React, { useEffect, useRef, useState } from "react";
import { Check, Download, Info, LoaderCircle, OctagonAlert, Sparkles, X } from "lucide-react";

// Shapes shared with demo.tsx (kept structural to avoid a circular import).
export type InvPattern = {
  id: string;
  name: string;
  named: boolean;
  template: string;
  count: number;
  level: string;
  samples: string[];
  spikeAt?: number;
  newAt?: number;
  buckets: number[];
};

export type InvData = {
  workspace: { id: string; title: string; service: string; lineCount: number };
  fileNames: string[];
  from?: string;
  to?: string;
  patterns: InvPattern[];
};

type Investigation = {
  id: string;
  agent: string;
  question: string;
  pattern?: string;
  state: "running" | "succeeded" | "failed" | "stopped";
  phase: "starting" | "reading" | "writing";
  answer?: string;
  errorCode?: "not_found" | "auth" | "failed";
  error?: string;
  refs: string[];
  unverified?: string[];
  startedAt: string;
  finishedAt?: string;
};

const AGENT_LABEL: Record<string, string> = { claude: "Claude Code", codex: "Codex" };

async function getJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init);
  const body = await res.json();
  if (!res.ok) throw new Error(body?.error ?? res.statusText);
  return body as T;
}

// ---------- answer parsing ----------

export type Parsed = {
  title: string;
  cause: string;
  timeline: string[];
  evidence: string[];
  next: string[];
};

// Splits the agent's Markdown into the sections the prompt asks for.
// Returns null when the expected headings are missing.
export function parseAnswer(md: string): Parsed | null {
  const out: Parsed = { title: "", cause: "", timeline: [], evidence: [], next: [] };
  let section = "";
  const cause: string[] = [];
  for (const raw of md.split("\n")) {
    const line = raw.trimEnd();
    const h1 = line.match(/^#\s+(.*)/);
    const h2 = line.match(/^##\s+(.*)/);
    if (h1 && !h2) {
      out.title = h1[1].trim();
      section = "";
      continue;
    }
    if (h2) {
      const name = h2[1].toLowerCase();
      section = name.startsWith("likely") ? "cause" : name.startsWith("timeline") ? "timeline" : name.startsWith("evidence") ? "evidence" : name.startsWith("what to check") ? "next" : "";
      continue;
    }
    const item = line.match(/^\s*(?:[-*]|\d+[.)])\s+(.*)/);
    if (section === "cause" && line.trim()) cause.push(line.trim());
    else if (item && section === "timeline") out.timeline.push(item[1]);
    else if (item && section === "evidence") out.evidence.push(item[1]);
    else if (item && section === "next") out.next.push(item[1]);
  }
  out.cause = cause.join(" ");
  if (!out.cause && out.evidence.length === 0) return null;
  return out;
}

// Citations: [P3], [P3 · Dec 4 06:00–07:00], or several ids in one bracket
// ([P1 · P2 · Dec 4 06:00–07:00]).
const CITE = /\[(P\d+(?:\s*[·,]\s*P\d+)*)(?:\s*·\s*([^\]]+))?\]/g;
// "all slots" / "whole window" style ranges read as "whole log" to users.
const rangeText = (r?: string) => (!r ? "" : /^(all|every|whole|entire)\b/i.test(r.trim()) ? "whole log" : r.trim());
const citeIds = (m: RegExpMatchArray) => [...m[1].matchAll(/P(\d+)/g)].map((x) => Number(x[1]));

// ---------- panel ----------

export function InvestigatePanel({
  data,
  start,
  colorOf,
  labelOf,
  filtered,
  onClose,
  onCite,
  onHoverPattern,
  toast,
  wsId,
  onJob
}: {
  onJob?: (id: string) => void;
  data: InvData;
  start: { pattern?: InvPattern; question: string; reason?: string; spike?: boolean; jobId?: string };
  colorOf: (p: InvPattern) => string;
  labelOf: (p: InvPattern, n: number) => { text: string };
  filtered: Set<string>;
  onClose: () => void;
  onCite: (p: InvPattern) => void;
  onHoverPattern: (id: string | null) => void;
  toast: (t: string) => void;
  wsId: string;
}) {
  const [question, setQuestion] = useState(start.question);
  const [agents, setAgents] = useState<Record<string, boolean>>({ claude: true, codex: true });
  const [agent, setAgent] = useState("claude");
  const [runs, setRuns] = useState<Investigation[]>([]);
  const [followUp, setFollowUp] = useState("");
  const [now, setNow] = useState(Date.now());
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    getJSON<Record<string, boolean>>("/api/demo/agents")
      .then((a) => {
        setAgents(a);
        if (!a.claude && a.codex) setAgent("codex");
      })
      .catch(() => undefined);
  }, []);

  // Reopen an existing investigation (?investigate={id}).
  useEffect(() => {
    if (!start.jobId) return;
    getJSON<Investigation>(`/api/demo/investigate?id=${start.jobId}`)
      .then((inv) => {
        setRuns([inv]);
        setQuestion(inv.question);
      })
      .catch(() => undefined);
  }, [start.jobId]);

  const current = runs[runs.length - 1];
  const running = current?.state === "running";

  // Poll the running investigation; tick the elapsed clock.
  useEffect(() => {
    if (!running) return;
    const tick = window.setInterval(async () => {
      setNow(Date.now());
      try {
        const inv = await getJSON<Investigation>(`/api/demo/investigate?id=${current.id}`);
        setRuns((rs) => rs.map((r) => (r.id === inv.id ? inv : r)));
      } catch {
        /* keep polling */
      }
    }, 1000);
    return () => window.clearInterval(tick);
  }, [running, current?.id]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [runs.length]);

  const run = async (q: string, previous?: string) => {
    const qs = [...filtered].map((id) => `filtered=${encodeURIComponent(id)}`).join("&");
    try {
      const res = await getJSON<{ id: string }>(`/api/demo/investigate?${qs}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ workspace: wsId, agent, question: q, pattern: start.pattern?.id ?? "", followUp: previous ?? "" })
      });
      const inv = await getJSON<Investigation>(`/api/demo/investigate?id=${res.id}`);
      setRuns((rs) => (previous ? [...rs, inv] : [inv]));
      if (!previous) onJob?.(inv.id);
      setNow(Date.now());
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e));
    }
  };
  const stop = async () => {
    if (!current) return;
    const inv = await getJSON<Investigation>(`/api/demo/investigate/stop?id=${current.id}`, { method: "POST" });
    setRuns((rs) => rs.map((r) => (r.id === inv.id ? inv : r)));
  };

  const first = runs[0];
  const answered = first && first.state === "succeeded";
  const title = start.pattern ? `Investigate · ${labelOf(start.pattern, 40).text}` : "Investigate";

  return (
    <aside className="drawer inv" aria-label="Investigate">
      <header>
        <b className="dtitle">{title}</b>
        <button className="btn ghost sm" aria-label="Close" onClick={onClose}>
          <X className="ico" />
        </button>
      </header>
      {start.pattern && start.reason && (
        <span className={`badge ${start.spike ? "b-spike" : "b-new"} inv-ctx`}>
          <b className="ell">{labelOf(start.pattern, 36).text}</b> · {start.reason}
        </span>
      )}

      {!answered && (
        <AskBox
          question={question}
          setQuestion={setQuestion}
          agents={agents}
          agent={agent}
          setAgent={setAgent}
          running={running}
          onRun={() => run(question)}
          onStop={stop}
        />
      )}

      {runs.map((r, i) => (
        <RunView
          key={r.id}
          inv={r}
          now={now}
          data={data}
          colorOf={colorOf}
          labelOf={labelOf}
          filtered={filtered}
          onCite={onCite}
          onHoverPattern={onHoverPattern}
          showQuestion={i > 0 || r.state === "succeeded"}
        />
      ))}

      {answered && (
        <>
          <div className="inv-foot">
            <button className="btn sm" onClick={() => exportSummary(data, runs, filtered, toast)}>
              <Download className="ico" /> Export summary
            </button>
            <span className="sub tnum">{meta(first)}</span>
          </div>
          <form
            className="inv-follow"
            onSubmit={(e) => {
              e.preventDefault();
              if (!followUp.trim() || running) return;
              run(followUp.trim(), current.id);
              setFollowUp("");
            }}
          >
            <input placeholder="Ask a follow-up" value={followUp} onChange={(e) => setFollowUp(e.target.value)} disabled={running} />
            <button className="btn sm" type="submit" disabled={running || !followUp.trim()}>
              Send
            </button>
          </form>
        </>
      )}
      <div ref={bottomRef} />
    </aside>
  );
}

function meta(inv: Investigation) {
  const end = inv.finishedAt ? new Date(inv.finishedAt).getTime() : Date.now();
  const secs = Math.max(0, Math.round((end - new Date(inv.startedAt).getTime()) / 1000));
  const cited = new Set([...(inv.answer ?? "").matchAll(CITE)].flatMap(citeIds)).size;
  return `${AGENT_LABEL[inv.agent]} · ${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, "0")} · cited ${cited} ${cited === 1 ? "pattern" : "patterns"}`;
}

function AskBox({
  question,
  setQuestion,
  agents,
  agent,
  setAgent,
  running,
  onRun,
  onStop
}: {
  question: string;
  setQuestion: (q: string) => void;
  agents: Record<string, boolean>;
  agent: string;
  setAgent: (a: string) => void;
  running: boolean;
  onRun: () => void;
  onStop: () => void;
}) {
  return (
    <div>
      <div className="askbox">
        <textarea
          rows={Math.min(6, Math.max(2, question.split("\n").length))}
          placeholder="What do you want to know? e.g. Why did this spike at 06:00?"
          value={question}
          disabled={running}
          onChange={(e) => setQuestion(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && question.trim() && !running) onRun();
          }}
        />
        <div className="askbar">
          <span className="sub">Agent</span>
          <div className="seg">
            {(["claude", "codex"] as const).map((a) => (
              <button
                key={a}
                className={agent === a ? "on" : ""}
                disabled={!agents[a] || running}
                title={agents[a] ? undefined : `Couldn't find ${a} on this machine. Install it, sign in, then try again.`}
                onClick={() => setAgent(a)}
              >
                {AGENT_LABEL[a]}
              </button>
            ))}
          </div>
          <div style={{ flex: 1 }} />
          {running ? (
            <button className="btn sm" onClick={onStop}>
              Stop
            </button>
          ) : (
            <button className="btn sm primary" disabled={!question.trim()} onClick={onRun} title="⌘↵">
              <Sparkles className="ico" /> Investigate
            </button>
          )}
        </div>
      </div>
      <p className="hint">
        Your agent reads the filtered patterns and sample lines on this machine. Like any Claude Code or Codex session, what it reads is sent to that agent's provider.
      </p>
    </div>
  );
}

const PHASES = [
  { key: "starting", label: (a: string) => `Starting ${AGENT_LABEL[a]}` },
  { key: "reading", label: () => "Reading patterns" },
  { key: "writing", label: () => "Writing the answer" }
];

function RunView({
  inv,
  now,
  data,
  colorOf,
  labelOf,
  filtered,
  onCite,
  onHoverPattern,
  showQuestion
}: {
  inv: Investigation;
  now: number;
  data: InvData;
  colorOf: (p: InvPattern) => string;
  labelOf: (p: InvPattern, n: number) => { text: string };
  filtered: Set<string>;
  onCite: (p: InvPattern) => void;
  onHoverPattern: (id: string | null) => void;
  showQuestion: boolean;
}) {
  const elapsed = Math.max(0, Math.round((now - new Date(inv.startedAt).getTime()) / 1000));
  const clock = `${Math.floor(elapsed / 60)}:${String(elapsed % 60).padStart(2, "0")}`;
  const byId = new Map(data.patterns.map((p) => [p.id, p]));

  // Renders text with [P3 · Dec 4 06:00–07:00] citations as chips.
  const rich = (text: string) => {
    const nodes: React.ReactNode[] = [];
    let last = 0;
    for (const m of text.matchAll(CITE)) {
      nodes.push(flagNumbers(text.slice(last, m.index), inv.unverified ?? [], `t${last}`));
      for (const n of citeIds(m)) {
        const p = byId.get(inv.refs[n - 1]);
        const key = `${m.index}-${n}`;
        if (!p) {
          nodes.push(
            <span key={key} className="cite-missing" title="Pattern not found">
              P{n}
            </span>
          );
          continue;
        }
        const isFiltered = filtered.has(p.id);
        nodes.push(
          <button
            key={key}
            className="cite"
            title={isFiltered ? "Filtered as noise. Still used as evidence." : undefined}
            onClick={() => onCite(p)}
            onMouseEnter={() => onHoverPattern(p.id)}
            onMouseLeave={() => onHoverPattern(null)}
          >
            <i style={{ background: colorOf(p) }} />
            {labelOf(p, 28).text}
            {m[2] ? ` · ${rangeText(m[2])}` : ""}
          </button>
        );
      }
      last = (m.index ?? 0) + m[0].length;
    }
    nodes.push(flagNumbers(text.slice(last), inv.unverified ?? [], `t${last}`));
    return nodes;
  };

  return (
    <section className="inv-run">
      {showQuestion && (
        <div className="inv-q">
          <Sparkles className="ico" /> {inv.question}
        </div>
      )}
      {inv.state === "running" && (
        <div className="inv-progress">
          {PHASES.map((ph, i) => {
            const idx = PHASES.findIndex((x) => x.key === inv.phase);
            const st = i < idx ? "done" : i === idx ? "run" : "todo";
            return (
              <div key={ph.key} className={`step ${st}`}>
                <span className="sic">
                  {st === "done" && <Check className="ico" />}
                  {st === "run" && <LoaderCircle className="ico spin" />}
                </span>
                <b>
                  {ph.label(inv.agent)}
                  {st === "run" ? "…" : ""}
                </b>
              </div>
            );
          })}
          <span className="sub tnum">{clock}</span>
          <div className="skel">
            <span className="sk" />
            <span className="sk" style={{ width: "90%" }} />
            <span className="sk" style={{ width: "60%" }} />
          </div>
        </div>
      )}
      {(inv.state === "failed" || inv.state === "stopped") && (
        <div className="inv-err">
          <OctagonAlert className="ico" />
          <span>{errorText(inv)}</span>
        </div>
      )}
      {inv.state === "succeeded" && inv.answer && <Answer answer={inv.answer} rich={rich} />}
    </section>
  );
}

function errorText(inv: Investigation) {
  const bin = inv.agent === "codex" ? "codex" : "claude";
  if (inv.state === "stopped") return "Stopped.";
  if (inv.errorCode === "not_found") return `Couldn't find ${bin} on this machine. Install it, sign in, then try again.`;
  if (inv.errorCode === "auth") return `${AGENT_LABEL[inv.agent]} needs you to sign in. Run \`${bin}\` in a terminal once, then try again.`;
  return `The investigation stopped: ${inv.error ?? "unknown error"}. Try again, or switch agent.`;
}

// Numbers the server could not find in the page data get a dashed underline.
function flagNumbers(text: string, unverified: string[], key: string): React.ReactNode {
  if (unverified.length === 0) return inline(text);
  const esc = unverified.map((n) => n.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|");
  const re = new RegExp(`(?<![\\d.])(${esc})(?![\\d.])`, "g");
  const out: React.ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(re)) {
    out.push(<React.Fragment key={`${key}-${last}`}>{inline(text.slice(last, m.index))}</React.Fragment>);
    out.push(
      <span key={`${key}-n${m.index}`} className="unverified" title="Not found in the data on this page. Check it before relying on it.">
        {m[0]}
      </span>
    );
    last = (m.index ?? 0) + m[0].length;
  }
  out.push(<React.Fragment key={`${key}-end`}>{inline(text.slice(last))}</React.Fragment>);
  return out;
}

// Minimal inline Markdown: **bold** and `code`.
function inline(text: string): React.ReactNode {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`)/g);
  return parts.map((p, i) =>
    p.startsWith("**") && p.endsWith("**") ? (
      <b key={i}>{p.slice(2, -2)}</b>
    ) : p.startsWith("`") && p.endsWith("`") ? (
      <code key={i}>{p.slice(1, -1)}</code>
    ) : (
      <React.Fragment key={i}>{p}</React.Fragment>
    )
  );
}

function Answer({ answer, rich }: { answer: string; rich: (t: string) => React.ReactNode[] }) {
  const parsed = parseAnswer(answer);
  return (
    <div className="inv-answer">
      <div className="inv-guess">
        <Info className="ico" /> Best guess from the evidence. Check before acting on it.
      </div>
      {parsed ? (
        <>
          <h3>Likely cause</h3>
          <p className="inv-cause">{rich(parsed.cause)}</p>
          {parsed.evidence.length > 0 && (
            <>
              <h3>Evidence</h3>
              <ol>
                {parsed.evidence.map((e, i) => (
                  <li key={i}>{rich(e)}</li>
                ))}
              </ol>
            </>
          )}
          {parsed.next.length > 0 && (
            <>
              <h3>What to check next</h3>
              <ul>
                {parsed.next.map((e, i) => (
                  <li key={i}>{rich(e)}</li>
                ))}
              </ul>
            </>
          )}
        </>
      ) : (
        <pre className="inv-raw">{answer}</pre>
      )}
    </div>
  );
}

// ---------- summary export ----------

function exportSummary(data: InvData, runs: Investigation[], filtered: Set<string>, toast: (t: string) => void) {
  const inv = runs.find((r) => r.state === "succeeded" && r.answer);
  if (!inv?.answer) return;
  const parsed = parseAnswer(inv.answer);
  const byId = new Map(data.patterns.map((p) => [p.id, p]));
  const nameOf = (p: InvPattern) => (p.named ? p.name : p.template);
  const cited = new Map<string, string>(); // pattern id -> first cited time range
  const plain = (text: string) =>
    text.replace(CITE, (_m, ids: string, range?: string) => {
      const names = [...ids.matchAll(/P(\d+)/g)]
        .map((x) => byId.get(inv.refs[Number(x[1]) - 1]))
        .filter((p): p is InvPattern => !!p)
        .map((p) => {
          if (!cited.has(p.id)) cited.set(p.id, rangeText(range));
          return `\`${nameOf(p)}\``;
        });
      if (names.length === 0) return "";
      return `(${names.join(", ")}${range ? `, ${rangeText(range)}` : ""})`;
    });
  const total = data.workspace.lineCount;
  const filteredPatterns = data.patterns.filter((p) => filtered.has(p.id));
  const filteredLines = filteredPatterns.reduce((s, p) => s + p.count, 0);
  const pct = total ? `${(Math.round((filteredLines / total) * 1000) / 10).toFixed(1)}%` : "0.0%";
  const fmt = (iso?: string) => (iso ? new Date(iso).toISOString().slice(0, 16).replace("T", " ") + " UTC" : "—");

  const cause = parsed ? plain(parsed.cause) : plain(inv.answer);
  const timeline = parsed?.timeline.map((t) => `- ${plain(t)}`).join("\n") ?? "";
  const next = parsed?.next.map((t) => `- [ ] ${plain(t)}`).join("\n") ?? "";
  parsed?.evidence.forEach((e) => plain(e));
  const rows = [...cited.entries()]
    .map(([id, range]) => {
      const p = byId.get(id)!;
      const why = [p.level === "error" || p.level === "fatal" ? "error" : "", p.spikeAt !== undefined ? "spiked" : "", p.newAt !== undefined ? "new" : "", filtered.has(id) ? "filtered as noise" : ""]
        .filter(Boolean)
        .join(", ");
      return `| \`${p.template.replace(/\|/g, "\\|")}\` | ${p.count} | ${range || "—"} | ${why || "—"} |`;
    })
    .join("\n");
  const firstCited = byId.get([...cited.keys()][0] ?? "");
  const samples = firstCited ? firstCited.samples.slice(0, 5).join("\n") : "";
  const title = parsed?.title || inv.question;

  const md = `# ${title}

**Service:** ${data.workspace.service} · **Logs:** ${data.fileNames[0] ?? data.workspace.title} · **Time range:** ${fmt(data.from)} – ${fmt(data.to)}
**Lines:** ${total} total · ${pct} filtered as noise · ${total - filteredLines} looked at

## What happened
${cause}

## Timeline
${timeline || "_No timeline in the answer._"}

## Evidence
| Pattern | Lines | When | Why it matters |
|---|---|---|---|
${rows || "| — | — | — | — |"}

Sample lines:
\`\`\`
${samples}
\`\`\`

## Likely cause
${cause} _(a hypothesis, not confirmed)_

## Next steps
${next || "- [ ] —"}

## Filtered as noise
${filteredPatterns.length} patterns, ${filteredLines} lines: ${filteredPatterns.map((p) => `\`${nameOf(p)}\``).join(", ") || "none"}.

---
_Generated by lapp on ${new Date().toISOString().slice(0, 10)} with ${AGENT_LABEL[inv.agent]}. Evidence above links to patterns in the lapp workspace for ${data.workspace.service}._
`;
  const d = new Date();
  const stamp = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}-${String(d.getHours()).padStart(2, "0")}${String(d.getMinutes()).padStart(2, "0")}`;
  const file = `lapp-${data.workspace.service}-${stamp}.md`;
  const url = URL.createObjectURL(new Blob([md], { type: "text/markdown" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = file;
  a.click();
  URL.revokeObjectURL(url);
  toast(`Summary saved to Downloads/${file}`);
}
