/**
 * Pure, user-facing labels for the two identity dimensions and entity kinds.
 *
 * The dashboard replaces internal ontology with product language and, per
 * requirement H, distinguishes revision-match certainty from content
 * retrievability visibly. These are the ONE source of those labels/tones so a
 * component never hard-codes "exact" strings inconsistently.
 */

import { formatCount } from './format.ts';
import { formatDateTime } from './dateFormat.ts';

export type Tone = 'ok' | 'warn' | 'err' | 'info' | 'neutral';

/**
 * kindLabel is the user-facing singular name for an entity kind.
 *
 * The internal identifiers (EntityKind 'target', TargetKey, /fleet/targets/...) are
 * UNCHANGED; only the words a first-time user reads are. Two of them are deliberate:
 *   - 'target' reads "Operational target" -- a concrete place a revision runs. It is
 *     NOT "Deployment": Pacto observes where a revision runs, it never deploys.
 *   - 'source' reads "Data source" -- the ingestion seam a snapshot was built from.
 *     It is NOT a collector (a collector observes a real environment and emits
 *     evidence; OCI, local and cache are sources but observe nothing).
 */
export function kindLabel(kind: string | undefined): string {
  switch (kind) {
    case 'service': return 'Service';
    case 'revision': return 'Revision';
    case 'target': return 'Operational target';
    case 'owner': return 'Owner';
    case 'source': return 'Data source';
    case 'domain': return 'Domain';
    case 'offering': return 'Platform offering';
    case 'external': return 'External system';
    default: return kind ? kind[0].toUpperCase() + kind.slice(1) : 'Entity';
  }
}

/** The classes of an external system, '' first for every class (the filter's All). */
export const EXTERNAL_CLASSES = ['', 'vendor-api', 'managed-cloud', 'legacy', 'unclassified', 'other-egress'] as const;

/** externalClassLabel is the words for an external system's class. */
export function externalClassLabel(c: string | undefined): string {
  switch (c) {
    case 'vendor-api': return 'Vendor API';
    case 'managed-cloud': return 'Managed cloud';
    case 'legacy': return 'Legacy';
    case 'other-egress': return 'Other egress';
    default: return 'Unclassified';
  }
}

/** externalClassNoun names one external system of a class inside a sentence. */
export function externalClassNoun(c: string | undefined): string {
  switch (c) {
    case 'vendor-api': return 'vendor API';
    case 'managed-cloud': return 'managed cloud service';
    case 'legacy': return 'legacy system';
    default: return 'external system';
  }
}

/**
 * externalJoinWords is how the consumers of an external system no catalog entry
 * classifies join it, from its joinedBy: a short label for a list cell and a
 * sentence for its page. Only a normalized-name join is "name only".
 */
export function externalJoinWords(joinedBy: string | undefined): { short: string; sentence: string } {
  switch (joinedBy) {
    case 'identity': return { short: 'declared identity', sentence: 'Joined by the identity its consumers declare; no catalog entry classifies it.' };
    case 'host': return { short: 'registrable domain', sentence: 'Joined by its registrable domain; no catalog entry classifies it.' };
    default: return { short: 'name only', sentence: 'Joined by name only: consumers that spell it differently do not join.' };
  }
}

/** kindLabelPlural is the user-facing plural of kindLabel (section headings, lists). */
export function kindLabelPlural(kind: string | undefined): string {
  return `${kindLabel(kind)}s`;
}

// ── revision-match certainty (a target's LinkState) ──────────────────────────
export function linkStateLabel(s: string | undefined): string {
  switch (s) {
    case 'exact': return 'Exact revision match';
    case 'inferred': return 'Inferred revision match';
    case 'ambiguous': return 'Ambiguous revision match';
    case 'unresolved': return 'Unresolved revision';
    default: return s || 'Unknown';
  }
}
export function linkStateTone(s: string | undefined): Tone {
  switch (s) {
    case 'exact': return 'ok';
    case 'inferred': return 'info';
    case 'ambiguous': return 'warn';
    case 'unresolved': return 'neutral';
    default: return 'neutral';
  }
}

// ── content retrievability (RevisionIdentity.retrievable + identityClass) ─────
// This is a SEPARATE dimension from revision-match certainty: an exact revision
// match can point at content that is not resolver-retrievable.
export function retrievabilityLabel(identityClass: string | undefined, retrievable: boolean | undefined): string {
  if (retrievable) return 'Retrievable content';
  switch (identityClass) {
    case 'mutable': return 'Mutable reference (not retrievable)';
    case 'no-ref': return 'No canonical reference';
    case 'local': return 'Local reference (not retrievable)';
    case 'malformed': return 'Malformed identity';
    case 'digest-mismatch': return 'Digest/reference mismatch';
    default: return 'Content not retrievable';
  }
}
export function retrievabilityTone(identityClass: string | undefined, retrievable: boolean | undefined): Tone {
  if (retrievable) return 'ok';
  // A digest/ref disagreement or malformed identity is a genuine inconsistency
  // (error); merely-not-retrievable-but-consistent content is a neutral limitation.
  return identityClass === 'digest-mismatch' || identityClass === 'malformed' ? 'err' : 'neutral';
}

// ── snapshot knowledge level ─────────────────────────────────────────────────
export function knowledgeLabel(level: string | undefined): string {
  switch (level) {
    case 'complete': return 'Complete knowledge';
    case 'empty': return 'Nothing known yet';
    case 'partial': return 'Partial knowledge';
    case 'stale': return 'Stale knowledge';
    case 'unavailable': return 'Source unavailable';
    case 'unknown': return 'Knowledge state unknown';
    default: return 'Knowledge state unknown';
  }
}
export function knowledgeTone(level: string | undefined): Tone {
  switch (level) {
    case 'complete': return 'ok';
    case 'empty': return 'neutral';
    // Partial knowledge is the unknown family: the neutral hatch, never a status tone.
    case 'partial': return 'neutral';
    case 'stale': return 'warn';
    case 'unavailable': return 'err';
    default: return 'neutral';
  }
}

// ownerProvenanceText is the tooltip sentence for where a service's owner comes from
// (ServiceRecord.ownerProvenance, carried as ownership.provenance). A declared or absent
// provenance says nothing: the plain owner value is the declaration.
export function ownerProvenanceText(p: string | undefined): string {
  switch (p) {
    case 'declared-via-target': return 'Declared by the contract a running target reports; no revision Pacto read declares an owner';
    case 'attested': return 'Read from the repository at the commit a verified CI identity signed; routes reviews, never clears a gate';
    case 'inferred': return 'Inferred; Pacto never routes approvals or clears a gate on it';
    default: return '';
  }
}

// ── relationship difference: see differenceLabel/differenceTone in lib/graphState.ts ──

// provenanceLabel names where a relationship's knowledge came from. The wire enum
// (pkg/fleet/neighborhood.go) has THREE values: a merged edge carries the combined
// "declared+observed", which had no case here and so printed as the raw wire token.
export function provenanceLabel(p: string | undefined): string {
  switch (p) {
    case 'declared': return 'Expected';
    case 'observed': return 'Observed';
    case 'declared+observed': return 'Expected and observed';
    case 'inferred': return 'Inferred';
    default: return p || '';
  }
}

// In the ordinary cases a relationship's provenance and its reconciliation are two names
// for one fact: an edge that is "Expected, not observed" was declared BY DEFINITION, and
// a "Matched" edge is both declared and observed. Printing both put a second word beside
// a badge that already said it. This is the provenance each difference already implies,
// so a caller can drop the redundant chip and keep it where it still carries news --
// insufficient evidence, or a pairing we did not anticipate.
const IMPLIED_PROVENANCE: Record<string, string> = {
  matched: 'declared+observed',
  'expected-not-observed': 'declared',
  'observed-not-expected': 'observed',
  inferred: 'inferred',
};
export function provenanceIsImplied(difference: string | undefined, provenance: string | undefined): boolean {
  return !!provenance && IMPLIED_PROVENANCE[difference || provenance] === provenance;
}

// ── attention categories ─────────────────────────────────────────────────────
// The backend enum (pkg/fleet/product.go) is the wire truth; these are the words a
// first-time user reads. Readiness is one of them ON PURPOSE: readiness is a
// dimension of triage (declared contract preparedness), not a separate workspace,
// so this list is also the Overview's set of entry points into the triage view.
export const ATTENTION_CATEGORIES = [
  'non-compliant', 'invalid', 'health', 'provisioning', 'unknown', 'stale', 'unresolved', 'out-of-range', 'lifecycle', 'undeclared', 'golden-path', 'readiness', 'knowledge', 'unowned',
] as const;

export function attentionCategoryLabel(c: string | undefined): string {
  switch (c) {
    case 'non-compliant': return 'Not compliant';
    case 'unknown': return 'Compliance unknown';
    case 'stale': return 'Stale evidence';
    case 'invalid': return 'Invalid contract';
    case 'health': return 'Health check failing';
    case 'provisioning': return 'Resource provisioning';
    case 'readiness': return 'Readiness gate';
    case 'unresolved': return 'Unresolved revision';
    case 'out-of-range': return 'Version out of range';
    case 'lifecycle': return 'Deprecated provider';
    case 'undeclared': return 'Undeclared dependency';
    case 'golden-path': return 'Golden path running';
    case 'knowledge': return 'Knowledge gap';
    case 'unowned': return 'No declared owner';
    default: return c || 'Other';
  }
}

/** The cause each attention code names (pkg/fleet/health.go and knowledge.go). */
const ATTENTION_CAUSES: Record<string, string> = {
  COLLECTION_FAILED: 'The collector failed to read the evidence',
  HEALTH_FAILING: 'The health check is failing',
  EVIDENCE_STALE: "The evidence is older than the source's freshness window",
  OBSERVATION_UNSUPPORTED: 'This cannot be observed in this environment',
  EVIDENCE_INSUFFICIENT: 'The evidence cannot confirm or contradict the contract',
  EVIDENCE_MISSING: 'No evidence was collected',
  CONTRACT_UNREADABLE: 'No contract of this service could be read',
  INTERFACE_DOCS_UNREAD: 'The interface documents the contract names could not be read',
  SUMMARY_UNKNOWN: 'Sources report different summaries or none could be read',
  DOMAIN_UNKNOWN: 'Sources report different business domains or none could be read',
  PLACEMENT_UNKNOWN: 'Pacto cannot see every place this service runs',
  NO_SUMMARY: 'The contract declares no summary',
  NO_DOMAIN: 'The contract declares no business domain',
  NO_INTERFACE_DOCS: 'An interface declares no document',
  NOT_DEPLOYED: 'No source reports an operational target of this service',
  UNVERIFIED_CAPABILITY: 'An extension capability no evaluator checks',
  INFERRED_PROVIDER: 'A dependency range points at an inferred contract, so Pacto never checks it',
};
/** attentionCause words an attention item's code, or returns the code itself. */
export function attentionCause(code: string | undefined): string {
  return (code && ATTENTION_CAUSES[code]) || code || '';
}

/** The words of a next action verb (#472's AttentionItem.nextAction). */
const NEXT_ACTION_VERBS: Record<string, string> = {
  'edit-contract': 'edit the contract',
  'plan-rollback': 'plan a rollback',
  declare: 'declare a contract',
};
/** nextActionWords says a next action as words only: it is advice, never a control. */
export function nextActionWords(a: { verb?: string; tool?: string; environment?: string } | null | undefined): string {
  if (!a?.verb) return '';
  const verb = NEXT_ACTION_VERBS[a.verb] ?? a.verb;
  return `Fix: ${verb}${a.environment ? ` in ${a.environment}` : ''}${a.tool ? ` with ${a.tool}` : ''}`;
}

// ── knowledge gaps ───────────────────────────────────────────────────────────
/** The words for each `gap` filter value (pkg/fleet/knowledge.go). */
export const GAP_WORDS: Record<string, string> = {
  'no-summary': 'No summary',
  'no-domain': 'No domain',
  'no-interface-docs': 'No interface document',
  'no-readiness': 'No readiness assessment',
  'not-deployed': 'Runs nowhere',
  'unverified-capability': 'Unverified extension capability',
  unknown: 'Pacto cannot tell',
};

// ── business domain and target environment ──────────────────────────────────
/** The businessDomain filter values for the services with no domain and for those
 *  whose domain Pacto cannot tell. Neither is a domain slug. */
export const NO_DOMAIN = '(none)';
export const UNKNOWN_DOMAIN = '(unknown)';

/** businessDomainLabel names a service's business domain, the No domain group, or the
 *  Domain unknown group for UNKNOWN_DOMAIN. Unknown is never folded into No domain. */
export function businessDomainLabel(v: string | undefined): string {
  if (v === UNKNOWN_DOMAIN) return 'Domain unknown';
  return v && v !== NO_DOMAIN ? v : 'No domain';
}

/** The environment a target runs in, as the target detail reports it. */
export interface TargetEnvironmentLike { name?: string; declared?: string; check?: { status?: string } }

/** targetEnvironmentLabel is the environment name, or Unknown when no source says. */
export function targetEnvironmentLabel(env: TargetEnvironmentLike | null | undefined): string {
  return env?.name || 'Unknown';
}

/** targetEnvironmentTone is ok only when the running revision declares the environment
 *  and its values pass. An environment nobody can confirm stays neutral. */
export function targetEnvironmentTone(env: TargetEnvironmentLike | null | undefined): Tone {
  if (env?.declared === 'not-declared') return 'warn';
  if (env?.declared !== 'declared') return 'neutral';
  switch (env.check?.status) {
    case 'pass': return 'ok';
    case 'fail': return 'err';
    default: return 'neutral';
  }
}

// ── attention severity ───────────────────────────────────────────────────────
// Severity is a THIRD vocabulary, orthogonal to compliance and to source health:
// it grades how much a triage item matters, not what state an entity is in. Routed
// through the compliance badge it had no case and no tone, so the highest-severity
// row on the triage screen printed a grey lowercase "error" — the one signal the
// screen exists to convey, rendered as the most ignorable thing on it.
// A finding severity carries one extra value the attention enum does not: "unknown"
// (pkg/fleet/preview.go), which is a severity we could not grade — not a fourth grade.
export function severityLabel(s: string | undefined): string {
  switch (s) {
    case 'error': return 'Error';
    case 'warning': return 'Warning';
    case 'info': return 'Info';
    case 'unknown': return 'Unknown';
    default: return s || 'Unknown';
  }
}
export function severityTone(s: string | undefined): Tone {
  switch (s) {
    case 'error': return 'err';
    case 'warning': return 'warn';
    case 'info': return 'info';
    default: return 'neutral';
  }
}

/** Entry points worth a tile or a chip on the overview: drops the uncategorised
 *  attention one (the lead tile's own number).
 *  Errors, any grade this build cannot read and failing health checks lead; the
 *  other warning and info entries follow. */
export function splitEntryPoints<T extends { view?: string; category?: string; severity?: string }>(eps: T[] = []): { lead: T[]; secondary: T[] } {
  const shown = eps.filter((ep) => !(ep.view === 'attention' && !ep.category));
  const milder = (ep: T) => ep.severity === 'warning' || ep.severity === 'info';
  // A failing health check is an observed problem, so it leads whatever its grade:
  // under "Not broken, worth a look" it would read as not broken.
  const leads = (ep: T) => !milder(ep) || ep.category === 'health';
  return { lead: shown.filter(leads), secondary: shown.filter((ep) => !leads(ep)) };
}

// ── source health ────────────────────────────────────────────────────────────
export function sourceHealthLabel(status: string | undefined): string {
  switch (status) {
    case 'available': return 'Available';
    case 'partial': return 'Partial';
    case 'stale': return 'Stale';
    case 'unavailable': return 'Unavailable';
    // A filter value, never a status: partial, stale or unavailable.
    case 'incomplete': return 'Incomplete';
    default: return status || 'Unknown';
  }
}
export function sourceHealthTone(status: string | undefined): Tone {
  switch (status) {
    case 'available': return 'ok';
    case 'stale': return 'warn';
    case 'unavailable': return 'err';
    // Partial, like unknown, is the neutral family, never a status tone.
    default: return 'neutral';
  }
}
/** The complete-population source tally (Fleet.SourceCounts). */
export interface SourceCounts {
  total?: number;
  available?: number;
  partial?: number;
  stale?: number;
  unavailable?: number;
}
/** One non-empty bucket of the fleet-wide source tally. `status` is the backend
 *  health filter value, or '' for the unclassified remainder — which has no filter
 *  value precisely because the product does not recognize what those sources are. */
export interface SourceTallyPart { status: string; count: number; text: string }
/**
 * sourceHealthTallyParts breaks the COMPLETE source population into its non-empty
 * health buckets — never the meta's source list, which is capped and, once capped,
 * deliberately biased towards the least healthy.
 *
 * Least-healthy first, so the number that changes what a reader does is the first
 * one they read. A status the product does not recognize is NOT folded into a
 * bucket; it is named as unclassified, because the alternative is a tally that adds
 * up perfectly and is wrong.
 */
export function sourceHealthTallyParts(c: SourceCounts | null | undefined): SourceTallyPart[] {
  const parts: SourceTallyPart[] = [];
  for (const [status, n] of [
    ['unavailable', c?.unavailable ?? 0],
    ['stale', c?.stale ?? 0],
    ['partial', c?.partial ?? 0],
    ['available', c?.available ?? 0],
  ] as Array<[string, number]>) {
    if (n > 0) parts.push({ status, count: n, text: `${n} ${status}` });
  }
  const classified = parts.reduce((n, p) => n + p.count, 0);
  const total = c?.total ?? 0;
  if (total > classified) {
    parts.push({ status: '', count: total - classified, text: `${total - classified} unclassified` });
  }
  return parts;
}
/**
 * sourceHealthSentence says what a source's health MEANS for the records around it,
 * in a sentence rather than a badge.
 *
 * A source page carries two health facts that are routinely different: this source's
 * own status, and the snapshot's completeness. The snapshot caveat is a banner, so
 * this one has to be more than a second badge repeating the header — a reader
 * comparing two badges cannot tell which is about what. A sentence naming the
 * consequence can only be read one way.
 */
export function sourceHealthSentence(status: string | undefined): string {
  switch (status) {
    case 'available':
      return 'This data source answered in full, so everything it holds is in the snapshot.';
    case 'partial':
      return 'This data source answered only in part, so some of its records are missing from the snapshot and may still exist in the fleet.';
    case 'stale':
      return 'This data source last answered too long ago to be treated as current, so what it contributed to the snapshot may already have changed.';
    case 'unavailable':
      return 'This data source did not answer, so nothing it holds reached the snapshot.';
    default:
      return 'This data source reported a health state Pacto does not recognize, so how much of it reached the snapshot is unknown.';
  }
}

/**
 * collectorLabel says, in plain words, who saw the evidence behind a finding.
 * The operator stamps each evidence ref with the producer of the matched
 * observation; an unknown producer is shown verbatim rather than guessed at.
 */
export function collectorLabel(collector: string | undefined): string {
  switch (collector) {
    case 'k8s-observer': return 'Kubernetes';
    case 'k8s-observer+ebpf': return 'Kubernetes + kernel';
    case 'ebpf': return 'kernel';
    // The host agent and pacto run decide from the kernel's view alone.
    case 'pacto-agent+ebpf': return 'kernel (host agent)';
    case 'pacto-run+ebpf': return 'kernel (pacto run)';
    default: return collector ?? '';
  }
}

/** seenBy joins the distinct collector labels of a finding's evidence refs. */
export function seenBy(refs: { collector?: string }[] | null | undefined): string {
  return [...new Set((refs ?? []).map((r) => collectorLabel(r.collector)).filter(Boolean))].join(', ');
}

/**
 * compatibility: unknown (pactoVersion 2.5) declares no range, so every surface
 * names it this one way and never as a range a consumer "accepts".
 */
export const RANGE_NOT_DECLARED = 'Range not declared';

/** acceptsLabel is what a consumer's declared compatibility says, or '' for none. */
export function acceptsLabel(compatibility: string | undefined | null): string {
  if (!compatibility) return '';
  return compatibility === 'unknown' ? RANGE_NOT_DECLARED : `Accepts ${compatibility}`;
}

/** serviceKeyName is the name inside a canonical service key, as fleet.ParseServiceKey
 *  reads it: the part after the one unescaped "/" (the domain separator), with "%2F" and
 *  "%25" decoded. "k8s:prod%2FDeployment%2Fexperiments" reads "k8s:prod/Deployment/experiments". */
export function serviceKeyName(key: string): string {
  const name = key.includes('/') ? key.slice(key.indexOf('/') + 1) : key;
  return name.replaceAll('%2F', '/').replaceAll('%25', '%');
}

type ReplacementRef = { from: string; target?: string; at?: string };

/** replacesTip is the tooltip of a service's Replaces line: how the newest replacement
 *  was linked, then up to ten more by key, target and time, so "+N more" never hides
 *  which workloads were taken over. total counts every replacement. */
export function replacesTip(replaces: ReplacementRef[], total: number): string {
  const [first, ...rest] = replaces;
  if (!first) return '';
  const head = `Linked by the Pacto target ${first.target || 'of this service'}, which runs as the workload. The Pacto was created ${formatDateTime(first.at)}.`;
  if (!rest.length) return head;
  const shown = rest.slice(0, 10).map((r) => `${serviceKeyName(r.from)} (Pacto target ${r.target || 'of this service'}, ${formatDateTime(r.at)})`);
  const unlisted = total - 1 - shown.length;
  return `${head} Also replaces ${shown.join(', ')}${unlisted > 0 ? ` and ${formatCount(unlisted)} more, listed by pacto fleet get` : ''}.`;
}
