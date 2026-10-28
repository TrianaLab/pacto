/** Owner identity, matching and detail: how a service's owner is keyed, labelled and merged. */

// ── Owner helpers ──

/** The owner's name as a reader sees it: the team if there is one, else the DRI. */
export function ownerDisplay(owner: unknown): string {
  if (!owner || typeof owner !== 'object') return '';
  const o = owner as Record<string, unknown>;
  if (o.team) return String(o.team);
  if (o.dri) return String(o.dri);
  return '';
}

/**
 * Canonical owner IDENTITY: `team:NAME` or `dri:NAME`, mirroring
 * `contract.OwnerKey` on the server so a link built here and a link built there
 * are the same link.
 *
 * The namespace is what makes it an identity rather than a label. A team called
 * `alice` and a person called `alice` render identically and are not the same
 * owner: merging them would put two estates on one page, two backlogs in one
 * count, and — worst — would make a service whose revisions name each of them
 * look consistently owned.
 *
 * Only the FIRST colon delimits, so an owner name may contain any punctuation
 * (`a:b:c`, `external/sendgrid`) and still round-trip. An owner declared with
 * contacts alone has no key: it is ownership that cannot be navigated to, not
 * ownership that is absent — see `unidentifiedOwnership` in the fleet aggregate.
 */
export function ownerKey(owner: unknown): string {
  if (!owner || typeof owner !== 'object') return '';
  const o = owner as Record<string, unknown>;
  if (o.team) return `team:${String(o.team)}`;
  if (o.dri) return `dri:${String(o.dri)}`;
  return '';
}

/**
 * The bucket for services with no canonical owner identity: no team and no DRI.
 * Not a canonical key — `parseOwnerKey` rejects it — so nothing routes to it by
 * accident.
 */
export const UNOWNED_KEY = '(unowned)';

/** One half of a parsed canonical owner key. */
export interface ParsedOwnerKey { kind: 'team' | 'dri'; value: string }

/**
 * Split a canonical owner key back into its namespace and name, or null if the
 * string is not one. Fail closed: a bare owner name is ambiguous — it could be
 * either namespace — and guessing would silently route a reader to one of two
 * different owners.
 */
export function parseOwnerKey(key: string): ParsedOwnerKey | null {
  const at = key.indexOf(':');
  if (at <= 0 || at === key.length - 1) return null;
  const kind = key.slice(0, at);
  if (kind !== 'team' && kind !== 'dri') return null;
  return { kind, value: key.slice(at + 1) };
}

/** The name inside a canonical owner key — what a reader reads. */
export function ownerKeyLabel(key: string): string {
  return parseOwnerKey(key)?.value ?? key;
}

/** How a canonical owner key's namespace is written for a reader: Team or DRI. */
export function ownerKeyKind(key: string): string {
  const p = parseOwnerKey(key);
  if (!p) return '';
  return p.kind === 'dri' ? 'DRI' : 'Team';
}

/** Check whether an owner object matches a search query (case-insensitive). */
export function ownerMatchesFilter(owner: unknown, query: string): boolean {
  if (!owner || typeof owner !== 'object') return false;
  const q = query.toLowerCase();
  const o = owner as Record<string, unknown>;
  if (String(o.team || '').toLowerCase().includes(q)) return true;
  if (String(o.dri || '').toLowerCase().includes(q)) return true;
  const contacts = o.contacts as Array<Record<string, unknown>> | undefined;
  if (contacts) {
    for (const c of contacts) {
      if (String(c.value || '').toLowerCase().includes(q)) return true;
    }
  }
  return false;
}

/** Check if owner is a structured object. Always returns true if owner is present. */
export function ownerIsStructured(owner: unknown): boolean {
  return owner != null && typeof owner === 'object';
}

// ── Owner detail extraction ──

export interface OwnerContact {
  type: string;
  value: string;
  purpose?: string;
}

export interface OwnerDetail {
  key: string;
  team: string;
  dri: string;
  contacts: OwnerContact[];
  isStructured: boolean;
  driConflict: boolean;
  allDris: string[];
}

/**
 * Extract a consistent OwnerDetail from the services sharing an owner key.
 * Merges contacts from all services (deduped by type+value).
 * Flags DRI inconsistency when services disagree.
 */
export function extractOwnerDetail(ownerKeyStr: string, services: Array<Record<string, unknown>>): OwnerDetail {
  const detail: OwnerDetail = { key: ownerKeyStr, team: '', dri: '', contacts: [], isStructured: false, driConflict: false, allDris: [] };

  const contactSet = new Set<string>();
  const mergedContacts: OwnerContact[] = [];
  const dris = new Set<string>();

  for (const svc of services) {
    const o = svc.owner;
    if (!o || typeof o !== 'object') continue;
    detail.isStructured = true;
    const obj = o as Record<string, unknown>;
    if (!detail.team && obj.team) detail.team = String(obj.team);
    const dri = String(obj.dri || '');
    if (dri) dris.add(dri);

    const contacts = obj.contacts as OwnerContact[] | undefined;
    if (contacts) {
      for (const c of contacts) {
        const key = `${c.type}\0${c.value}`;
        if (!contactSet.has(key)) {
          contactSet.add(key);
          mergedContacts.push(c);
        }
      }
    }
  }

  if (dris.size > 0) {
    detail.allDris = Array.from(dris).sort();
    detail.dri = detail.allDris[0];
    detail.driConflict = dris.size > 1;
  }
  detail.contacts = mergedContacts;

  return detail;
}

// ── Owner aggregation ──

export interface OwnerAggregation {
  /** Canonical identity — what groups, links and drill-downs use. */
  key: string;
  /** The owner's name as authored — what a reader reads. Never the key. */
  label: string;
  /** `Team` or `DRI`: the namespace that makes two same-named owners tellable apart. */
  kind: string;
  services: number;
  compliant: number;
  warning: number;
  nonCompliant: number;
  reference: number;
  unknown: number;
  invalid: number;
  notEvaluated: number;
  // Secondary conclusive/verification metrics (mirror the fleet tile).
  runtimeEvaluated: number; // compliant + warning + nonCompliant + unknown
  conclusive: number; // compliant + warning + nonCompliant
  totalBlast: number;
  /** The widest blast radius among the owner's services. Blast radii overlap, so their sum counts no real set. */
  maxBlast: number;
  compliancePercent: number;
  // Per-owner readiness composition (buckets sum to `services`).
  ready: number;
  partial: number;
  notReady: number;
  notConfigured: number;
}
