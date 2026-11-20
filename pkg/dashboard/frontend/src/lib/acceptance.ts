// Words for range coverage and announced breaks, as pacto impact prints them.

type Coverage = { edges: number; declared: number; accepted: number; unknown?: { from: string; to: string; proposed?: string }[] | null; unknownTotal?: number };
type BreakState = { state: string; by?: string; record?: string; expires?: string; route?: string; reason?: string };

/** The Ranges: line: `Ranges: 41 declared and 6 accepted of 58 dependency edges (81%)`, saying when consumers hidden by the viewer's access are left out. */
export function rangesLine(r: Coverage, hidden = 0): string {
  const pct = r.edges ? Math.floor(((r.declared + r.accepted) * 100) / r.edges) : 0;
  const line = r.edges ? `Ranges: ${r.declared} declared and ${r.accepted} accepted of ${r.edges} dependency edges (${pct}%)` : 'Ranges: no dependency edges';
  return hidden > 0 ? `${line}; ${hidden} ${hidden === 1 ? 'consumer' : 'consumers'} hidden by your access ${hidden === 1 ? 'is' : 'are'} not counted` : line;
}

/** The unknown ranges, one per line, then how many more there are. */
export function unknownRanges(r: Coverage): string[] {
  const lines = (r.unknown ?? []).map((u) => `unknown: ${u.from} -> ${u.to}${u.proposed ? ` (proposed ${u.proposed})` : ''}`);
  const more = (r.unknownTotal ?? 0) - lines.length;
  return more > 0 ? [...lines, `and ${more} more`] : lines;
}

/** s as a sentence: capitalized, with a full stop. */
const sentence = (s: string) => `${s[0].toUpperCase()}${s.slice(1)}${s.endsWith('.') ? '' : '.'}`;

/** The tip of a consumer's break mark: its state, who signed, the record, the expiry and the route. */
export function breakText(b: BreakState): string {
  const words: Record<string, string> = {
    accepted: 'Break accepted: it never blocks.',
    announced: 'Break announced: nothing accepts it yet.',
    expired: 'Break expired or records no expiry: it keeps its verdict.',
  };
  return [words[b.state] ?? `Break ${b.state}.`, b.by && `Signed by ${b.by}.`, b.record && `Record: ${b.record}.`,
    b.expires ? `Expires ${b.expires}.` : 'Expiry unknown.', b.route && sentence(b.route), b.reason].filter(Boolean).join(' ');
}

type Advisory = { interface: string; source: string; changes?: { path: string; reason: string }[] | null; consumers?: { service: string }[] | null };

/** The tip of a possible break (inferred): the interface, its source, the first findings, the consumers and the declare hint. */
export function advisoryText(a: Advisory): string {
  const changes = a.changes ?? [], consumers = (a.consumers ?? []).map((c) => c.service);
  const more = changes.length > 3 ? ` and ${changes.length - 3} more` : '';
  return [`Possible break on inferred interface ${a.interface} (${a.source}).`,
    changes.length > 0 && `Findings: ${changes.slice(0, 3).map((c) => c.reason).join('; ')}${more}.`,
    consumers.length > 0 ? `Consumers (inferred, compatibility unknown): ${consumers.join(', ')}.` : 'No consumer is known.',
    'It never blocks or clears: declare the interface to classify the change.'].filter(Boolean).join(' ');
}
