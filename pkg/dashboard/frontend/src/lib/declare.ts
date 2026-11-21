/**
 * The words and links of the Declare panel (513-S6): one row per range a person may
 * accept, the drafts list line pacto declare prints, the signing login's URL and
 * what moved between two plans. Pure, so the panel only draws.
 */

import { serviceDeclareUrl } from './router.ts';

type Range = { dependency: string; compatibility: string; proposed?: string; observed?: string; accepted?: boolean };
type Signer = { subject?: string; issuer?: string; class?: string };
type Draft = { tag: string; state: string; asker?: Signer; onBehalfOf?: string; requested?: string[]; signed?: boolean; reason?: string };
type Plan = { version?: string; deploy?: string; owner?: string; ranges?: Range[]; signer?: { subject?: string }; contract?: { text?: string }; record?: { text?: string } };
type Ref = { kind?: string; key?: string; declaration?: string } | null | undefined;

/** A range row: checked means the declaration carries the proposal, unchecked keeps unknown. */
export type RangeRow = { dependency: string; label: string; value: string; proposed: string; checked: boolean; acceptable: boolean; mark: string };

/** rangeRows is one row per dependency of plan, checked for each one in accept. */
export function rangeRows(plan: Plan | null | undefined, accept: readonly string[]): RangeRow[] {
  return (plan?.ranges ?? []).map((r) => {
    const proposed = r.proposed ?? '';
    const checked = !!proposed && accept.includes(r.dependency);
    const seen = r.observed ? `, seen at ${r.observed}` : '';
    const mark = !proposed
      ? `Nothing proposes a range for ${r.dependency}${seen}, so it stays ${r.compatibility || 'unknown'}`
      : checked
        ? `Accepted: you declare ${proposed}, proposed by the cluster read${seen}`
        : `Proposed ${proposed} by the cluster read${seen}; unchecked keeps unknown`;
    return {
      dependency: r.dependency, label: `${r.dependency} range`, proposed, checked, acceptable: !!proposed, mark,
      value: checked ? proposed : proposed ? `keep unknown (proposed ${proposed})` : r.compatibility || 'unknown',
    };
  });
}

/** draftAsker says who wrote a draft: a signed one names its signer, an unsigned one
 *  only asserts the person it was written for. */
export function draftAsker(d: Draft): string {
  const by = d.signed && d.asker?.subject ? d.asker.subject : d.asker?.class === 'human' ? 'a person' : 'an agent';
  return d.onBehalfOf ? `${by} for ${d.onBehalfOf}` : by;
}

/** draftLine is a draft as pacto declare's plan lists it. */
export function draftLine(d: Draft): string {
  if (d.state === 'unreadable') return `${d.tag} is unreadable${d.reason ? `: ${d.reason}` : ''}`;
  const asks = d.requested?.length ? `, requests ${d.requested.join(', ')}` : '';
  return `${d.tag} by ${draftAsker(d)}${asks}`;
}

/** draftMark is the tip of a draft proposer's mark. */
export function draftMark(d: Draft): string {
  if (d.signed) return `Signed by ${d.asker?.subject || 'an unnamed signer'}${d.asker?.issuer ? ` (${d.asker.issuer})` : ''}`;
  return `Asserted by the proposer, unsigned${d.onBehalfOf ? `: nobody proved ${d.onBehalfOf} asked for it` : ''}`;
}

/** signUrl starts the signing login with the plan's one-time ticket and comes back to
 *  this service's panel with the intent it was started for. The accepted ranges ride
 *  on the ticket, never on the URL. */
export function signUrl(ticket: string, serviceKey: string, intent: string): string {
  const ret = '/' + serviceDeclareUrl(serviceKey, { signed: intent });
  return `/auth/sign?ticket=${encodeURIComponent(ticket)}&return=${encodeURIComponent(ret)}`;
}

/** declareTarget is the inferred service key a declare action opens the panel of,
 *  or '' when the action is something else or names no inferred service. */
export function declareTarget(action: { verb?: string } | null | undefined, subject: Ref): string {
  return action?.verb === 'declare' && subject?.kind === 'service' && subject.declaration === 'inferred' ? subject.key ?? '' : '';
}

/** moved names what differs between the plan a person read and a fresh one. */
export function moved(before: Plan | null | undefined, after: Plan | null | undefined): string {
  if (!before || !after) return 'the plan';
  const ranges = (p: Plan) => JSON.stringify((p.ranges ?? []).map((r) => [r.dependency, r.compatibility, r.proposed ?? '']));
  const out: string[] = [];
  if (before.version !== after.version) out.push(`the version (${before.version || 'none'} to ${after.version || 'none'})`);
  if (before.deploy !== after.deploy) out.push('the deploy artifact');
  if (before.owner !== after.owner) out.push('the owner');
  if (before.signer?.subject !== after.signer?.subject) out.push('the signer');
  if (ranges(before) !== ranges(after)) out.push('the ranges');
  if (before.contract?.text !== after.contract?.text || before.record?.text !== after.record?.text) out.push('the files');
  return out.length ? out.join(', ') : 'what the cluster read';
}
