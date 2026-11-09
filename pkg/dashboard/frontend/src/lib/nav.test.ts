import { describe, it, expect } from 'vitest';
import { navGroups, legacyNav, navCurrent, navCount, railCount, sourceHealth } from './nav';
import { buildCommands } from './commands';

const find = (key: string, caps: unknown = { fleet: true, delivery: true }) =>
  navGroups(caps).flatMap((g) => g.items).find((i) => i.key === key)!;

describe('navCurrent: the item a page belongs to agrees with its breadcrumb trail', () => {
  it.each([
    ['fleet-services', 'services'], ['fleet-owners', 'owners'], ['fleet-domains', 'domains'],
    ['fleet-offerings', 'offerings'], ['fleet-golden-paths', 'golden-paths'], ['fleet-sources', 'sources'],
    ['fleet-attention', 'attention'], ['fleet-overview', 'overview'], ['fleet', 'graph'], ['changes', 'changes'],
    ['delivery-inbox', 'delivery'],
  ])('%s is the page of %s', (view, key) => {
    const lit = navGroups({ fleet: true, delivery: true }).flatMap((g) => g.items).filter((i) => navCurrent(i, view, ''));
    expect(lit.map((i) => [i.key, navCurrent(i, view, '')])).toEqual([[key, 'page']]);
  });

  // Every entity page shares one view id, so the kind decides; the item is marked
  // "true", never "page": the entity page is beneath it, not it.
  it.each([
    ['service', 'services'], ['revision', 'services'], ['target', 'services'], ['owner', 'owners'],
    ['domain', 'domains'], ['offering', 'offerings'], ['golden-path', 'golden-paths'], ['source', 'sources'],
  ])('a %s entity page sits under %s', (kind, key) => {
    const lit = navGroups({ fleet: true }).flatMap((g) => g.items).filter((i) => navCurrent(i, 'fleet-entity', kind));
    expect(lit.map((i) => [i.key, navCurrent(i, 'fleet-entity', kind)])).toEqual([[key, 'true']]);
  });

  it('marks Delivery "true" on a Delivery tab other than the inbox', () => {
    expect(navCurrent(find('delivery'), 'delivery-history', '')).toBe('true');
    expect(navCurrent(find('delivery'), 'delivery-inbox', '')).toBe('page');
  });

  it('lights the legacy item of a legacy view', () => {
    expect(legacyNav().filter((i) => navCurrent(i, 'owner-detail', '')).map((i) => i.label)).toEqual(['Owners']);
  });
});

describe('navGroups', () => {
  it('Services links somewhere real at every probe state', () => {
    expect(find('services', null).href).toBe('#/services');
    expect(find('services', { fleet: true }).href).toBe('#/fleet/services');
  });

  it('adds Delivery only once the host confirms it', () => {
    const keys = (caps: unknown) => navGroups(caps).flatMap((g) => g.items).map((i) => i.key);
    expect(keys(null)).not.toContain('delivery');
    expect(keys({ fleet: true, delivery: false })).not.toContain('delivery');
    expect(keys({ fleet: true, delivery: true })).toContain('delivery');
  });

  // The palette lists the sidebar's destinations first, in the same order.
  it('agrees with the palette order', () => {
    const side = navGroups({ fleet: true }).flatMap((g) => g.items).map((i) => i.href);
    const views = buildCommands('', [], true).find((g) => g.label === 'Views')!.items.map((v) => v.href);
    expect(views.slice(0, side.length)).toEqual(side);
  });

  it('the palette keeps Delivery in its group, after Change analysis', () => {
    const views = buildCommands('', [], true, true).find((g) => g.label === 'Views')!.items;
    const i = views.findIndex((v) => v.label === 'Change analysis');
    expect(views[i + 1]).toMatchObject({ label: 'Delivery inbox', hint: 'Change and delivery' });
    expect(buildCommands('organization', [], true)[0].items.map((v) => v.label))
      .toEqual(['Services', 'Owners', 'Domains', 'Offerings', 'Golden paths']);
  });
});

describe('counts and health', () => {
  it('compacts a count over 999 for the 232px row, and caps the rail badge at 99+', () => {
    expect(navCount(999)).toBe('999');
    expect(navCount(1234)).toBe('1.2K');
    expect(navCount(15_000)).toBe('15K');
    expect(railCount(99)).toBe('99');
    expect(railCount(100)).toBe('99+');
  });

  it('is a disc when every source reports, a ring otherwise, and unknown with no sources', () => {
    expect(sourceHealth(null)).toBeNull();
    expect(sourceHealth({ total: 0 })).toBeNull();
    expect(sourceHealth({ total: 4, available: 4 })).toEqual({ total: 4, reporting: 4, partial: false });
    expect(sourceHealth({ total: 4, available: 3, unavailable: 1 })).toEqual({ total: 4, reporting: 3, partial: true });
    expect(sourceHealth({ total: 4, available: 3, stale: 1 })?.partial).toBe(true);
    expect(sourceHealth({ total: 4, available: 3, partial: 1 })?.partial).toBe(true);
  });
});
