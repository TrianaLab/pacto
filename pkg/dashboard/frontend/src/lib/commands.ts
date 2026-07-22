/** Pure command-list builder for the ⌘K palette. No runes — unit-testable. */
import { serviceUrl, ownerUrl, graphUrl, ownersUrl, readinessUrl, compareDiffUrl } from './router';
import { ownerKey, ownerMatchesFilter } from './format';

export type CommandKind = 'view' | 'service' | 'owner' | 'action';

export interface Command {
  kind: CommandKind;
  label: string;
  hint?: string;    // right-aligned meta (version, service count)
  href?: string;    // router hash for view/service/owner
  action?: string;  // action id for action commands
}

export interface CommandGroup {
  label: string;
  items: Command[];
}

const VIEWS: Command[] = [
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
export function buildCommands(query: string, services: any[]): CommandGroup[] {
  const q = query.trim().toLowerCase();
  const match = (s: string) => !q || s.toLowerCase().includes(q);
  const groups: CommandGroup[] = [];

  const views = VIEWS.filter((v) => match(v.label));
  if (views.length) groups.push({ label: 'Views', items: views });

  if (q) {
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
