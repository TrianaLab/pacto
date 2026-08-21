/** Breadcrumb trails derived from view state. Pure — unit-testable. */
import {
  graphUrl, fleetOverviewUrl, fleetServicesUrl, fleetOwnersUrl, fleetSourcesUrl, hashForHref,
} from './router';

export interface Crumb {
  label: string;
  href?: string;
}

/** Graph › focused-node, or Graph › By owner, or just Graph. */
export function graphBreadcrumbs(f: { group: string; focus: string }): Crumb[] {
  if (f.focus) return [{ label: 'Graph', href: graphUrl() }, { label: f.focus }];
  if (f.group === 'owner') return [{ label: 'Graph', href: graphUrl() }, { label: 'By owner' }];
  return [{ label: 'Graph' }];
}

// A minimal structural view of a product entity ref (never the whole DTO type).
interface RefLike { kind?: string; key?: string; label?: string; href?: string }

// crumb builds a Crumb from a canonical product ref, linking to its authoritative
// backend href. Parent identity is ALWAYS taken from a canonical ref, never inferred
// from a display string.
function refCrumb(ref: RefLike | null | undefined, fallbackLabel: string): Crumb {
  const label = ref?.label || ref?.key || fallbackLabel;
  return ref?.href ? { label, href: hashForHref(ref.href) } : { label };
}

/**
 * fleetEntityBreadcrumbs builds the entity-relationship breadcrumb trail for a rich
 * entity page from the DTO's canonical refs: e.g. a revision's parent
 * service comes from detail.revision.service, never from parsing its label. `detail`
 * is a NarrowedEntityDetail; only the fields used here are read.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- reads a narrow subset of the DTO
export function fleetEntityBreadcrumbs(detail: any): Crumb[] {
  // The trail roots at Overview, the product's home. "Fleet" is an internal word for
  // the snapshot model and never appears in the trail a first-time user reads.
  const root: Crumb = { label: 'Overview', href: fleetOverviewUrl() };
  const e: RefLike | undefined = detail?.entity;
  if (!e || !e.kind) return [root];
  const services: Crumb = { label: 'Services', href: fleetServicesUrl() };
  switch (e.kind) {
    case 'service':
      return [root, services, { label: e.label || e.key || 'service' }];
    case 'revision': {
      const leaf = detail.revision?.version ? `Revision ${detail.revision.version}` : (e.label || e.key || 'Revision');
      return [root, services, refCrumb(detail.revision?.service, 'service'), { label: leaf }];
    }
    case 'target':
      return [root, services, refCrumb(detail.target?.service, 'service'), { label: e.label || e.key || 'Operational target' }];
    case 'owner':
      return [root, { label: 'Owners', href: fleetOwnersUrl() }, { label: e.label || e.key || 'owner' }];
    case 'source':
      return [root, { label: 'Data sources', href: fleetSourcesUrl() }, { label: e.label || e.key || 'source' }];
    default:
      return [root, { label: e.label || e.key || 'entity' }];
  }
}
