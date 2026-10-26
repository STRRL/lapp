import React, { useEffect, useState } from "react";
import { X } from "lucide-react";

type Preview = {
  model: string;
  keySet: boolean;
  patterns: { template: string; samples: string[] }[];
  sampleLines: number;
  tokens: number;
  requests: number;
};

// Bits worth a second look before they leave the machine.
const SENSITIVE: { kind: string; re: RegExp }[] = [
  { kind: "Looks like an email", re: /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/g },
  { kind: "Looks like an IP address", re: /\b(?:\d{1,3}\.){3}\d{1,3}\b|\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b/gi },
  { kind: "Looks like a token or key", re: /\b(?:eyJ[\w-]{10,}\.[\w-]{10,}\.[\w-]{5,}|(?:sk|pk|ghp|gho|xox[abp])[-_][A-Za-z0-9_-]{12,}|[A-Za-z0-9+/_-]{32,}={0,2})\b/g }
];

type Hit = { start: number; end: number; kind: string };

function findHits(line: string): Hit[] {
  const hits: Hit[] = [];
  for (const { kind, re } of SENSITIVE) {
    for (const m of line.matchAll(re)) {
      const start = m.index ?? 0;
      const end = start + m[0].length;
      if (!hits.some((h) => start < h.end && end > h.start)) hits.push({ start, end, kind });
    }
  }
  return hits.sort((a, b) => a.start - b.start);
}

function Highlighted({ line }: { line: string }) {
  const hits = findHits(line);
  const out: React.ReactNode[] = [];
  let last = 0;
  hits.forEach((h, i) => {
    out.push(line.slice(last, h.start));
    out.push(
      <mark key={i} className="sens" title={h.kind}>
        {line.slice(h.start, h.end)}
      </mark>
    );
    last = h.end;
  });
  out.push(line.slice(last));
  return <>{out}</>;
}

export function PreviewDialog({
  wsId,
  service,
  onClose,
  onSent
}: {
  wsId: string;
  service: string;
  onClose: () => void;
  onSent: () => void;
}) {
  const [data, setData] = useState<Preview | null>(null);
  const [error, setError] = useState("");
  const [remember, setRemember] = useState(false);
  const [sending, setSending] = useState(false);
  useEffect(() => {
    fetch(`/api/demo/llm-preview?ws=${encodeURIComponent(wsId)}`)
      .then(async (r) => (r.ok ? r.json() : Promise.reject(new Error((await r.json()).error))))
      .then(setData)
      .catch((e) => setError(String(e.message ?? e)));
  }, [wsId]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const flagged = data ? data.patterns.flatMap((p) => [p.template, ...p.samples]).reduce((n, l) => n + findHits(l).length, 0) : 0;

  const send = async () => {
    setSending(true);
    setError("");
    const res = await fetch(`/api/demo/name?ws=${encodeURIComponent(wsId)}${remember ? "&remember=1" : ""}`, { method: "POST" });
    if (!res.ok) {
      setError((await res.json()).error ?? res.statusText);
      setSending(false);
      return;
    }
    onSent();
  };

  return (
    <div className="modal-scrim" onClick={onClose}>
      <div className="modal" role="dialog" aria-label="What lapp will send to OpenRouter" onClick={(e) => e.stopPropagation()}>
        <header>
          <b className="dtitle">What lapp will send to OpenRouter</b>
          <button className="btn ghost sm" aria-label="Close" onClick={onClose}>
            <X className="ico" />
          </button>
        </header>
        <p className="lead-plain">To name patterns, lapp sends these sample lines. Nothing else is sent.</p>
        {data && (
          <div className="modal-stats tnum">
            {data.patterns.length} patterns · {data.sampleLines} sample lines · about {data.tokens.toLocaleString("en-US")} tokens · model {data.model}
          </div>
        )}
        {flagged > 0 && (
          <div className="modal-warn">{flagged} highlighted bits look like emails, IP addresses or tokens. Check them before sending.</div>
        )}
        <div className="modal-body">
          {!data && !error && <p className="sub">Loading…</p>}
          {data?.patterns.map((p, i) => (
            <section key={i} className="pv-block">
              <div className="pv-tpl mono">
                <Highlighted line={p.template} />
              </div>
              <pre className="pv-lines mono">
                {p.samples.map((l, j) => (
                  <div key={j}>
                    <Highlighted line={l} />
                  </div>
                ))}
              </pre>
            </section>
          ))}
        </div>
        {error && <p className="err">{error}</p>}
        <footer>
          <label className="chk">
            <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
            <span className="cb">✓</span>
            Don't ask again for {service}
          </label>
          <div style={{ flex: 1 }} />
          {data && !data.keySet && <span className="sub">Set OPENROUTER_API_KEY and restart lapp to name patterns.</span>}
          <button className="btn" onClick={onClose}>
            Not now
          </button>
          <button
            className="btn primary"
            disabled={!data || sending || !data.keySet}
            title={data && !data.keySet ? "Set OPENROUTER_API_KEY and restart lapp to name patterns." : undefined}
            onClick={send}
          >
            Send and name patterns
          </button>
        </footer>
      </div>
    </div>
  );
}
