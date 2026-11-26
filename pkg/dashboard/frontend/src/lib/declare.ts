/**
 * The words and links of the Declare panel (513-S6): one row per range a person may
 * accept, the drafts list line pacto declare prints, the signing login's URL and
 * what moved between two plans. Pure, so the panel only draws.
 */

import { fleetEntityUrl, goldenPathDeclareUrl, serviceAcceptSignedUrl, serviceDeclareUrl } from './router.ts';
import { pagedHash } from './paging.ts';

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

type Completeness = {
  clusterAsOf?: string; coverage?: { source: string; state: string; reason?: string }[] | null;
  hidden?: { services?: number; callers?: number } | null; skipped?: { tag: string }[] | null;
};

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** completenessLine says how complete the cluster read behind the proposals is: when it
 *  was taken, the sources cut short and what the person's own credentials could not
 *  read, so a partial read never reads as complete. '' when the plan says nothing. */
export function completenessLine(p: Completeness | null | undefined): string {
  if (!p) return '';
  const partial = (p.coverage ?? []).filter((c) => c.state !== 'complete');
  const parts: string[] = [];
  if (p.clusterAsOf || p.coverage?.length) {
    const state = !p.coverage?.length ? 'completeness unknown'
      : partial.length ? `partial (${partial.map((c) => (c.reason ? `${c.source}: ${c.reason}` : c.source)).join('; ')})` : 'complete';
    parts.push(`Cluster read${p.clusterAsOf ? ` as of ${p.clusterAsOf}` : ''}: ${state}`);
  }
  if (p.hidden?.services) parts.push(`${plural(p.hidden.services, 'called service you may not read is', 'called services you may not read are')} not listed`);
  if (p.hidden?.callers) parts.push(`${plural(p.hidden.callers, 'caller you may not read is', 'callers you may not read are')} not counted`);
  if (p.skipped?.length) parts.push(`${plural(p.skipped.length, 'published version was', 'published versions were')} not taken as the baseline (${p.skipped.map((k) => k.tag).join(', ')})`);
  return parts.join('; ');
}

/** signUrl starts the signing login with the plan's one-time ticket and comes back to
 *  this service's panel with the intent it was started for. The accepted ranges ride
 *  on the ticket, never on the URL. */
export function signUrl(ticket: string, serviceKey: string, intent: string): string {
  return `/auth/sign?ticket=${encodeURIComponent(ticket)}&return=${encodeURIComponent('/' + signReturn(serviceKey, intent))}`;
}

/** signReturn is the page a signing login comes back to: this service's (or external
 *  system's, or inferred golden path's) panel with the intent it was started for. */
export function signReturn(serviceKey: string, intent: string): string {
  if (isTemplateKey(serviceKey)) return goldenPathDeclareUrl(serviceKey, { signed: intent });
  const id = claimId(serviceKey);
  return id ? pagedHash(fleetEntityUrl('external', id), { declare: '1', signed: intent }) : serviceDeclareUrl(serviceKey, { signed: intent });
}

/** claimId is the external system a claim's subject (external:<identity>) names, or ''
 *  for an inferred service. */
export function claimId(serviceKey: string): string {
  return serviceKey.startsWith('external:') ? serviceKey.slice('external:'.length) : '';
}

/** isTemplateKey says whether a declare subject is an inferred golden path,
 *  template:<namespace>/<name>: it has no service page, only the Golden paths list. */
export function isTemplateKey(serviceKey: string): boolean {
  return serviceKey.startsWith('template:');
}

// The contract schema's owner.team and service.name patterns.
const TEAM = /^[a-zA-Z0-9._/-]+$/;
const NAME = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;

/** claimFieldError says which field of a claim is wrong, its owner (required),
 *  name or offering (both optional), and what is wrong with it; field '' and
 *  message '' when all are fine. */
export function claimFieldError(owner: string, name: string, offering = ''): { field: '' | 'owner' | 'name' | 'offering'; message: string } {
  if (!owner) return { field: 'owner', message: 'Name the team that owns it' };
  if (owner.length > 253 || !TEAM.test(owner)) return { field: 'owner', message: 'A team uses letters, digits, dots, dashes, underscores and slashes' };
  if (name && (name.length > 63 || !NAME.test(name))) return { field: 'name', message: 'A service name uses lower-case letters, digits and dashes, at most 63' };
  // The offering is a slug like service.offering; the server refuses any other with OfferingInvalid.
  if (offering && (offering.length > 63 || !NAME.test(offering))) return { field: 'offering', message: 'An offering uses lower-case letters, digits and dashes, at most 63' };
  return { field: '', message: '' };
}

const RELEASE = /^\d+\.\d+\.\d+$/;

/** offeringFieldError says which field of an offering acceptance is wrong: its version
 *  (a release MAJOR.MINOR.PATCH, required) or its owner (checked only when given: the
 *  server asks for one on a first acceptance). field '' and message '' when both are fine. */
export function offeringFieldError(version: string, owner: string): { field: '' | 'version' | 'owner'; message: string } {
  if (!version) return { field: 'version', message: 'Set the version the declared offering is published at' };
  if (version.length > 64 || !RELEASE.test(version)) return { field: 'version', message: 'A version is MAJOR.MINOR.PATCH, such as 1.0.0' };
  if (owner && (owner.length > 253 || !TEAM.test(owner))) return { field: 'owner', message: 'A team uses letters, digits, dots, dashes, underscores and slashes' };
  return { field: '', message: '' };
}

/** openPaths are the paths an OfferingSchemaOpen refusal names, one per open path of
 *  the offering's parameters, so the panel lists them; [] for any other refusal. */
export function openPaths(r: { code?: string; message?: string } | null | undefined): string[] {
  if (r?.code !== 'OfferingSchemaOpen') return [];
  // ponytail: reads the planner's own message ("open at a, b: close each path ..."); a
  // structured paths field on the refusal if another surface ever needs them.
  const m = /open at (.+?): close each path/.exec(r.message ?? '');
  return m ? m[1].split(', ') : [];
}

/** acceptSignUrl starts the signing login for an accept plan and comes back to the
 *  service page with the intent it was started for. */
export function acceptSignUrl(ticket: string, serviceKey: string, intent: string): string {
  const ret = '/' + (isTemplateKey(serviceKey) ? goldenPathDeclareUrl(serviceKey, { signed: intent }) : serviceAcceptSignedUrl(serviceKey, intent));
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
