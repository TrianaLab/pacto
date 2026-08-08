/** Breadcrumb trails derived from view state. Pure — unit-testable. */
import {
  graphUrl, fleetOverviewUrl, fleetServicesUrl, fleetOwnersUrl, fleetSourcesUrl, hashForHref,
} from './router';

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

// A minimal structural view of a product entity ref (never the whole DTO type).
interface RefLike { kind?: string; key?: string; label?: string; href?: string }

// crumb builds a Crumb from a canonical product ref, linking to its authoritative
// backend href. Parent identity is ALWAYS taken from a canonical ref, never inferred
// from a display string (requirement H).
function refCrumb(ref: RefLike | null | undefined, fallbackLabel: string): Crumb {
  const label = ref?.label || ref?.key || fallbackLabel;
  return ref?.href ? { label, href: hashForHref(ref.href) } : { label };
}

/**
 * fleetEntityBreadcrumbs builds the entity-relationship breadcrumb trail for a rich
 * entity page from the DTO's canonical refs (requirement H): e.g. a revision's parent
 * service comes from detail.revision.service, never from parsing its label. `detail`
 * is a NarrowedEntityDetail; only the fields used here are read.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- reads a narrow subset of the DTO
export function fleetEntityBreadcrumbs(detail: any): Crumb[] {
  const fleet: Crumb = { label: 'Fleet', href: fleetOverviewUrl() };
  const e: RefLike | undefined = detail?.entity;
  if (!e || !e.kind) return [fleet];
  const services: Crumb = { label: 'Services', href: fleetServicesUrl() };
  switch (e.kind) {
    case 'service':
      return [fleet, services, { label: e.label || e.key || 'service' }];
    case 'revision': {
      const leaf = detail.revision?.version ? `Revision ${detail.revision.version}` : (e.label || e.key || 'Revision');
      return [fleet, services, refCrumb(detail.revision?.service, 'service'), { label: leaf }];
    }
    case 'target':
      return [fleet, services, refCrumb(detail.target?.service, 'service'), { label: `Deployment ${e.label || e.key || ''}`.trim() }];
    case 'owner':
      return [fleet, { label: 'Owners', href: fleetOwnersUrl() }, { label: e.label || e.key || 'owner' }];
    case 'source':
      return [fleet, { label: 'Sources', href: fleetSourcesUrl() }, { label: e.label || e.key || 'source' }];
    default:
      return [fleet, { label: e.label || e.key || 'entity' }];
  }
}
