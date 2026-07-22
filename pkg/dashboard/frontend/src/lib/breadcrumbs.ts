/** Breadcrumb trails derived from view state. Pure — unit-testable. */
import { graphUrl } from './router';

export interface Crumb {
  label: string;
  href?: string;
}

/** Fleet › focused-node, or Fleet › By owner, or just Fleet. */
export function graphBreadcrumbs(f: { group: string; focus: string }): Crumb[] {
  if (f.focus) return [{ label: 'Fleet', href: graphUrl() }, { label: f.focus }];
  if (f.group === 'owner') return [{ label: 'Fleet', href: graphUrl() }, { label: 'By owner' }];
  return [{ label: 'Fleet' }];
}
