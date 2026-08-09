/** Pure command-list builder for the ⌘K palette. No runes — unit-testable. */
import {
  serviceUrl, ownerUrl, graphUrl, ownersUrl, readinessUrl, compareDiffUrl,
  fleetOverviewUrl, fleetServicesUrl, fleetUrl, fleetOwnersUrl, fleetSourcesUrl,
  fleetAttentionUrl, fleetChangesUrl,
} from './router';
import { ownerKey, ownerMatchesFilter } from './format';

export type CommandKind = 'view' | 'service' | 'owner' | 'action';

export interface Command {
  kind: CommandKind;
  label: string;
  hint?: string;       // right-aligned meta (version, service count)
  href?: string;       // router hash for view/service/owner
  action?: string;     // action id for action commands
  keywords?: string[]; // searchable synonyms; never rendered
}

export interface CommandGroup {
  label: string;
  items: Command[];
}

// On a Fleet-capable host the palette offers the PRODUCT destinations (never the legacy
// routes), so the command palette can't be a back door to a superseded UI (Part 1); a
// non-Fleet host keeps the legacy destinations, which are its only UI.
//
// The palette AGREES with the primary nav: the four primary destinations come first, in
// nav order, and the secondary workspaces (the dimensions the nav deliberately does not
// promote) follow -- so every workspace stays one keystroke away without the nav
// pretending they are all equally fundamental. Readiness resolves to the Needs-attention
// readiness category, the product's single definition of it.
const FLEET_VIEWS: Command[] = [
  { kind: 'view', label: 'Overview', href: fleetOverviewUrl() },
  { kind: 'view', label: 'Services', href: fleetServicesUrl() },
  { kind: 'view', label: 'Operational graph', href: fleetUrl() },
  // "Compare revisions" is the ACTION this workspace performs, not a second place to go:
  // as its own row it listed the identical href twice, which is the same "two buttons,
  // one screen" confusion the entity pages had. It stays fully searchable as a synonym.
  { kind: 'view', label: 'Change analysis', href: fleetChangesUrl(), keywords: ['compare', 'compare revisions', 'diff'] },
  { kind: 'view', label: 'Needs attention', href: fleetAttentionUrl() },
  { kind: 'view', label: 'Owners', href: fleetOwnersUrl() },
  { kind: 'view', label: 'Data sources', href: fleetSourcesUrl() },
  { kind: 'view', label: 'Readiness', href: fleetAttentionUrl({ category: 'readiness' }) },
];
const LEGACY_VIEWS: Command[] = [
  { kind: 'view', label: 'Services', href: '#/' },
  { kind: 'view', label: 'Graph', href: graphUrl() },
  { kind: 'view', label: 'Owners', href: ownersUrl() },
  { kind: 'view', label: 'Readiness', href: readinessUrl() },
  { kind: 'view', label: 'Compare', href: compareDiffUrl() },
];

const ACTIONS: Command[] = [
  { kind: 'action', label: 'Toggle theme', action: 'theme' },
  { kind: 'action', label: 'Refresh data', action: 'refresh' },
  { kind: 'action', label: 'Toggle auto-reload', action: 'autoreload' },
];

/**
 * Ordered, grouped command list for the palette. Empty query → Views + Actions
 * (a useful default). Non-empty query → every group filtered; empty groups drop.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function buildCommands(query: string, services: any[], fleet = false): CommandGroup[] {
  const q = query.trim().toLowerCase();
  const match = (s: string) => !q || s.toLowerCase().includes(q);
  const groups: CommandGroup[] = [];

  const views = (fleet ? FLEET_VIEWS : LEGACY_VIEWS)
    .filter((v) => match(v.label) || (v.keywords || []).some(match));
  if (views.length) groups.push({ label: 'Views', items: views });

  // The legacy service/owner search links to legacy detail routes, which are superseded
  // on a Fleet host (the visible EntitySearch / '/' discovers entities through the
  // product API there). Offer it only on a non-Fleet host, so the palette never links to
  // a superseded service/owner screen.
  if (q && !fleet) {
    const svc: Command[] = (services || [])
      .filter((s) => s.name.toLowerCase().includes(q) || ownerMatchesFilter(s.owner, q))
      .slice(0, 6)
      .map((s) => ({ kind: 'service', label: s.name, hint: s.version || '', href: serviceUrl(s.name) }));
    if (svc.length) groups.push({ label: 'Services', items: svc });

    const seen = new Set<string>();
    const owners: Command[] = [];
    for (const s of services || []) {
      const key = ownerKey(s.owner);
      if (!key || seen.has(key) || !key.toLowerCase().includes(q)) continue;
      seen.add(key);
      owners.push({ kind: 'owner', label: key, href: ownerUrl(key) });
      if (owners.length >= 4) break;
    }
    if (owners.length) groups.push({ label: 'Owners', items: owners });
  }

  const actions = ACTIONS.filter((a) => match(a.label));
  if (actions.length) groups.push({ label: 'Actions', items: actions });

  return groups;
}

/** Flatten groups to a single ordered list for keyboard index selection. */
export function flattenCommands(groups: CommandGroup[]): Command[] {
  return groups.flatMap((g) => g.items);
}
