/**
 * The primary navigation (SPEC 20, option B2): one model for the sidebar, its icon rail,
 * the phone drawer and the command palette, so the four never disagree on a name, an
 * order or which page a route belongs to. Pure, no runes.
 */
import {
  ownersUrl, readinessUrl, fleetUrl, fleetOverviewUrl, fleetChangesUrl, compareDiffUrl, deliveryInboxUrl,
  fleetSourcesUrl, fleetAttentionUrl, fleetOwnersUrl, fleetDomainsUrl, fleetOfferingsUrl, fleetGoldenPathsUrl,
} from './router';
import { formatCount } from './format';

export interface NavItem {
  key: string;
  label: string;
  href: string;
  /** route.view values whose page this item is. */
  views: string[];
  /** Entity kinds whose pages belong under this item (every entity page shares one view). */
  kinds?: string[];
  /** A path from NAV_GLYPH, drawn at 20px. */
  glyph: string;
}

export interface NavGroup {
  /** Empty for the ungrouped head (Overview, Needs attention). */
  label: string;
  items: NavItem[];
}

// The kind glyphs of lib/figure.ts plus four pages that are not kinds. Overview is a
// house so it never reads as Domains' four squares.
export const NAV_GLYPH: Record<string, string> = {
  overview: 'M3 11 L12 4 L21 11 M5.5 9.5 V20 H18.5 V9.5 M10 20 V14 H14 V20',
  attention: 'M12 4 L21 20 H3 Z M12 10 V14 M12 17 V17.5',
  service: 'M4 7 L12 3 L20 7 L20 17 L12 21 L4 17 Z M4 7 L12 11 L20 7 M12 11 V21',
  owner: 'M12 11 A3.5 3.5 0 1 0 12 4 A3.5 3.5 0 1 0 12 11 Z M5 20 C5 15.5 19 15.5 19 20',
  domain: 'M4 4 H11 V11 H4 Z M13 4 H20 V11 H13 Z M4 13 H11 V20 H4 Z M13 13 H20 V20 H13 Z',
  offering: 'M3 9 L12 4 L21 9 L12 14 Z M6 11 V17 L12 20 L18 17 V11',
  'golden-path': 'M4 20 C8 20 8 12 12 12 C16 12 16 4 20 4 M17 4 H20 V7',
  change: 'M4 12 H20 M14 6 L20 12 L14 18',
  environment: 'M3 18 H21 M5 18 V10 L12 6 L19 10 V18 M10 18 V13 H14 V18',
  graph: 'M8.5 6 A2.5 2.5 0 1 1 3.5 6 A2.5 2.5 0 1 1 8.5 6 Z M20.5 8 A2.5 2.5 0 1 1 15.5 8 A2.5 2.5 0 1 1 20.5 8 Z M12.5 18 A2.5 2.5 0 1 1 7.5 18 A2.5 2.5 0 1 1 12.5 18 Z M8.4 6.2 L15.5 7.8 M6.7 8.4 L9.3 15.6 M16.6 10.1 L11.6 16',
  source: 'M12 20 V12 M12 12 A3 3 0 1 0 12 6 A3 3 0 1 0 12 12 Z M6 4 A9 9 0 0 0 6 14 M18 4 A9 9 0 0 1 18 14',
};

const DELIVERY_VIEWS = ['delivery-inbox', 'delivery-environments', 'delivery-autonomy', 'delivery-change', 'delivery-history', 'delivery-publish'];

/**
 * navGroups is the sidebar of a Fleet host, or of a host whose capabilities are not known
 * yet (the common case, so it never flashes the legacy bar first). Delivery is shown only
 * once the host CONFIRMS it, so a cluster without it never flashes a dead item.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function navGroups(capabilities: any): NavGroup[] {
  return [
    {
      label: '',
      items: [
        { key: 'overview', label: 'Overview', href: fleetOverviewUrl(), views: ['fleet-overview'], glyph: 'overview' },
        { key: 'attention', label: 'Needs attention', href: fleetAttentionUrl(), views: ['fleet-attention'], glyph: 'attention' },
      ],
    },
    {
      label: 'Organization',
      items: [
        // '#/services' works at every probe state: canonicalized to /fleet/services on a
        // Fleet host. '#/' would canonicalize to the Overview and do nothing visible.
        { key: 'services', label: 'Services', href: capabilities?.fleet ? '#/fleet/services' : '#/services', views: ['list', 'detail', 'fleet-services'], kinds: ['service', 'revision', 'target'], glyph: 'service' },
        { key: 'owners', label: 'Owners', href: fleetOwnersUrl(), views: ['fleet-owners'], kinds: ['owner'], glyph: 'owner' },
        { key: 'domains', label: 'Domains', href: fleetDomainsUrl(), views: ['fleet-domains'], kinds: ['domain'], glyph: 'domain' },
        { key: 'offerings', label: 'Offerings', href: fleetOfferingsUrl(), views: ['fleet-offerings'], kinds: ['offering'], glyph: 'offering' },
        // A golden path's entity page is a service page, lit here like its breadcrumb.
        { key: 'golden-paths', label: 'Golden paths', href: fleetGoldenPathsUrl(), views: ['fleet-golden-paths'], kinds: ['golden-path'], glyph: 'golden-path' },
      ],
    },
    {
      label: 'Change and delivery',
      items: [
        { key: 'changes', label: 'Change analysis', href: fleetChangesUrl(), views: ['changes'], glyph: 'change' },
        ...(capabilities?.delivery === true
          ? [{ key: 'delivery', label: 'Delivery', href: deliveryInboxUrl(), views: DELIVERY_VIEWS, glyph: 'environment' }]
          : []),
      ],
    },
    {
      label: 'System',
      items: [
        { key: 'graph', label: 'Operational graph', href: fleetUrl(), views: ['fleet', 'graph'], glyph: 'graph' },
        { key: 'sources', label: 'Data sources', href: fleetSourcesUrl(), views: ['fleet-sources'], kinds: ['source'], glyph: 'source' },
      ],
    },
  ];
}

/** The legacy host (the offline `pacto doc` export) keeps its own top bar items. */
export function legacyNav(): NavItem[] {
  return [
    { key: 'services', label: 'Services', href: '#/services', views: ['list', 'detail'], glyph: 'service' },
    { key: 'owners', label: 'Owners', href: ownersUrl(), views: ['owners', 'owner-detail'], glyph: 'owner' },
    { key: 'readiness', label: 'Readiness', href: readinessUrl(), views: ['readiness'], glyph: 'attention' },
    { key: 'compare', label: 'Compare', href: compareDiffUrl(), views: ['diff'], glyph: 'change' },
  ];
}

/**
 * navCurrent is the aria-current an item takes: "page" on the page it opens, "true" on a
 * page beneath it (an entity page, a Delivery tab other than the inbox), so the item is
 * marked in both sidebar states without claiming to be the page itself.
 */
export function navCurrent(item: NavItem, view: string, kind: string): 'page' | 'true' | undefined {
  if (view === 'fleet-entity') return (item.kinds || []).includes(kind) ? 'true' : undefined;
  if (!item.views.includes(view)) return undefined;
  return item.key === 'delivery' && view !== 'delivery-inbox' ? 'true' : 'page';
}

const COMPACT = new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 });

/** A count in the 232px sidebar: 1.2K over 999, so a row never grows past its column. */
export function navCount(n: number): string {
  return n > 999 ? COMPACT.format(n) : formatCount(n);
}

/** The rail badge has room for two digits. */
export function railCount(n: number): string {
  return n > 99 ? '99+' : formatCount(n);
}

export interface SourceCounts { total?: number; available?: number; partial?: number; stale?: number; unavailable?: number }

/**
 * sourceHealth is the Data sources dot: a disc when every source reports, a ring when
 * some do not (the shape carries the state, never only the colour). null = not known.
 */
export function sourceHealth(c: SourceCounts | null | undefined): { total: number; reporting: number; partial: boolean } | null {
  const total = c?.total ?? 0;
  if (!total) return null;
  const reporting = total - (c?.unavailable ?? 0);
  return { total, reporting, partial: reporting < total || (c?.stale ?? 0) > 0 || (c?.partial ?? 0) > 0 };
}
