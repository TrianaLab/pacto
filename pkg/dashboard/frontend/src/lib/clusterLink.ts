// A member's outbound channel to this hub (514-S4) in words, for the Clusters block
// on Data sources: a muted run after the status word, with the issuer, the mapping
// and the dates in the chip's HelpTip only.
import type { ClusterSummary } from './api.ts';

const MODEL_WORDS: Record<string, string> = { 'shared-issuer': 'shared issuer', 'group-mapping': 'group mapping', 'issuer-not-accepted': 'issuer not accepted' };

// when renders an RFC 3339 time as "2026-11-20 08:15", as the join rows do.
const when = (t: string) => t.slice(0, 16).replace('T', ' ');

/** linkWords: "live · shared issuer", "down" or "no live channel" for an approved member
 *  (approved) with no link; '' for a cluster with neither. A dropped channel shows no model:
 *  nothing is reviewed through it until it is back. */
export function linkWords(s: ClusterSummary, approved: boolean): string {
  const l = s.link;
  if (!l?.channel) return approved ? 'no live channel' : '';
  if (l.channel !== 'live') return l.channel;
  const models = [...new Set((l.issuers ?? []).map((m) => MODEL_WORDS[m.model ?? ''] ?? m.model).filter(Boolean))];
  return ['live', ...models].join(' · ');
}

/** linkTip: the plain sentences behind linkWords, appended to the chip's HelpTip text. */
export function linkTip(s: ClusterSummary, approved: boolean): string {
  const l = s.link;
  if (!l?.channel) return approved ? ' This member has no live channel to this hub, so signed-in people are not reviewed there.' : '';
  const facts = (l.issuers ?? []).map((m) => {
    if (m.model === 'shared-issuer') return ` ${m.issuer}: its tokens are accepted.`;
    if (m.model === 'group-mapping') return ` ${m.issuer}: reads use the group mapping${m.mapping ? ` ${m.mapping}` : ''}${m.mappedBy ? ` approved by ${m.mappedBy}` : ''}${m.mappedAt ? ` on ${m.mappedAt.slice(0, 10)}` : ''}.`;
    return ` ${m.issuer}: not accepted and no group mapping is approved, so its people are not reviewed here.`;
  }).join('');
  if (l.channel === 'live') return ` Live channel${l.since ? ` since ${when(l.since)}` : ''}.${facts}`;
  return ` The channel dropped${l.since ? ` at ${when(l.since)}` : ''}; reviews here wait until the member dials again.${facts ? ` When it was live:${facts}` : ''}`;
}
