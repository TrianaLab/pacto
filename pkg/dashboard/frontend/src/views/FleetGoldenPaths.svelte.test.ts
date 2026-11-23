/**
 * Component tests for the Golden paths list: one row per service whose contract
 * declares service.template and that runs nowhere, with its owner, status, signature,
 * source and the services created from it.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { entitiesFn, declareFn } = vi.hoisted(() => ({ entitiesFn: vi.fn(), declareFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetEntities: (...a: unknown[]) => entitiesFn(...a), fleetDeclare: (...a: unknown[]) => declareFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetGoldenPathsView from './FleetGoldenPathsView.svelte';

const meta = { schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'x', asOf: '2026-11-07T10:00:00Z', completeness: 'complete', sources: [] };
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const listResp = (entities: any[], m: any = meta): any => ({ meta: m, total: entities.length, count: entities.length, offset: 0, limit: 25, truncated: false, entities });
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const pathRef = (name: string, extra: any = {}) => ({
  kind: 'service', key: `ghcr.io/acme/${name}`, label: name, href: `/fleet/services/${encodeURIComponent(`ghcr.io/acme/${name}`)}`,
  template: true, status: 'NotEvaluated', domain: 'ghcr.io/acme', secondary: 'ghcr.io/acme', ...extra,
});

function mountView(props: Record<string, unknown> = {}) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetGoldenPathsView, { target, props: { refreshTick: 0, ...props } });
  return { target, done: () => { unmount(component); document.body.removeChild(target); } };
}

beforeEach(() => { entitiesFn.mockReset(); declareFn.mockReset(); declareFn.mockReturnValue(new Promise(() => {})); location.hash = ''; });

describe('FleetGoldenPathsView', () => {
  it('asks for golden paths only and lists each with its summary, owner, signature, source and created services', async () => {
    entitiesFn.mockResolvedValue(listResp([
      pathRef('http-service', { summary: 'An HTTP API on the platform runtime', owner: 'platform', signature: 'verified', created: 2, nonCompliant: 1 }),
      pathRef('worker', { signature: 'unsigned', created: 1 }),
      pathRef('cron', { signature: 'not-checked' }),
    ]));
    const { target, done } = mountView({ text: 'http' });
    await vi.waitFor(() => expect(target.querySelectorAll('[data-testid="golden-path-list"] tbody tr').length).toBe(3));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['service'], template: 'only', text: 'http', limit: 25, sort: 'created' }));
    // The origin figure asks about the whole fleet, whatever the search.
    expect(entitiesFn).toHaveBeenCalledWith({ kinds: ['service'], template: 'exclude', limit: 1 });
    expect(target.querySelector('h1')?.textContent).toBe('Golden paths');
    const rows = target.querySelectorAll('[data-testid="golden-path-list"] tbody tr');
    expect(rows[0].textContent).toContain('http-service');
    // The label takes the ellipsis, with its whole name in its title: an ellipsis on a
    // box around the inline-flex link hid the whole link in a narrow name column.
    const name = rows[0].querySelector('th .gp-name') as HTMLElement;
    expect(name.closest('.cell-clip')).toBeNull();
    expect(name.querySelector('.ei-label')?.getAttribute('title')).toBe('http-service');
    expect(rows[0].textContent).toContain('An HTTP API on the platform runtime');
    expect(rows[0].textContent).toContain('platform');
    expect(rows[0].textContent).toContain('ghcr.io/acme');
    expect(rows[0].textContent).toContain('Verified');
    // A verified signature was checked against a publisher rule, pin or policy.
    expect(rows[0].querySelector('[data-testid="path-signature"]')?.getAttribute('title')).toContain('publisher rule, pin or policy');
    expect(rows[1].textContent).toContain('No owner declared');
    expect(rows[1].textContent).toContain('Unsigned');
    // No source that read it checks signatures: never shown as unknown.
    expect(rows[2].textContent).toContain('Not checked');
    const created = rows[0].querySelector('a[data-testid="path-created"]');
    expect(created?.textContent).toContain('2');
    expect(created?.getAttribute('href')).toBe('#/fleet/services?createdFrom=ghcr.io%2Facme%2Fhttp-service');
    expect(rows[0].querySelector('a.entity-link')?.getAttribute('href')).toBe('#/fleet/services/ghcr.io%2Facme%2Fhttp-service');
    expect(rows[0].querySelector('a[data-testid="path-noncompliant"]')?.getAttribute('href')).toBe('#/fleet/services?status=NonCompliant&createdFrom=ghcr.io%2Facme%2Fhttp-service');
    expect(rows[1].querySelector('[data-testid="path-noncompliant"]')).toBeNull();
    done();
  });

  it('opens the creation explainer on request', async () => {
    entitiesFn.mockResolvedValue(listResp([pathRef('http-service', { created: 1 })]));
    const { target, done } = mountView();
    const toggle = await vi.waitFor(() => { const b = target.querySelector<HTMLButtonElement>('[data-testid="creation-toggle"]'); expect(b).toBeTruthy(); return b!; });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    toggle.click();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="creation-explainer"]')).toBeTruthy());
    expect(toggle.textContent).toContain('Hide');
    done();
  });

  it('says what a golden path is when there is none', async () => {
    entitiesFn.mockResolvedValue(listResp([]));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(target.textContent).toContain('No golden paths yet: a golden path is a contract that declares service.template'));
    expect(target.querySelector('[data-testid="golden-paths-empty"] a')?.getAttribute('href')).toContain('pacto.run');
    done();
  });

  it('does not claim there is none when knowledge is incomplete', async () => {
    entitiesFn.mockResolvedValue(listResp([], { ...meta, completeness: 'partial' }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(target.textContent).not.toContain('No golden paths yet');
    done();
  });

  it('orders by health on request', async () => {
    entitiesFn.mockResolvedValue(listResp([pathRef('http-service')]));
    const { done } = mountView({ sort: 'health' });
    await vi.waitFor(() => expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ sort: 'health', template: 'only' })));
    done();
  });
});

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const tpl = (name: string, extra: any = {}) => ({
  name, entity: `template:default/${name}`, from: 'Catalog (catalog.acme.io)', source: 'catalog', provenance: 'inferred', acceptable: true,
  observedAt: '2026-11-23T09:00:00Z', url: `https://catalog.acme.io/create/templates/default/${name}`, summary: `${name} summary`, owner: 'group:default/platform', ...extra,
});
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const withInferred = (entities: any[], items: any[], extra: any = {}) => ({ ...listResp(entities), inferredTemplates: { completeness: 'complete', asOf: '2026-11-23T09:00:00Z', total: items.length, items, ...extra } });
const rowsOf = (t: HTMLElement) => [...t.querySelectorAll<HTMLElement>('[data-testid="golden-path-list"] tbody tr')];

describe('FleetGoldenPathsView inferred golden paths', () => {
  it('lists inferred rows after the declared ones, muted, with one mark and no signature or status badge', async () => {
    entitiesFn.mockResolvedValue(withInferred([pathRef('http-service', { signature: 'verified' })], [tpl('springboot-service'), tpl('worker', { url: undefined })]));
    const { target, done } = mountView({ canAccept: true });
    await vi.waitFor(() => expect(rowsOf(target).length).toBe(3));
    const rows = rowsOf(target);
    expect(rows[0].classList.contains('gp-inferred')).toBe(false);
    expect(rows[1].classList.contains('gp-inferred')).toBe(true);
    expect(rows[1].querySelectorAll('.prov-mark').length).toBe(1);
    expect(rows[1].querySelector('.prov-mark button')?.getAttribute('aria-label')).toBe('Inferred');
    expect(rows[1].textContent).toContain('Not declared');
    expect(rows[1].querySelector('[data-testid="path-signature"]')).toBeNull();
    expect(rows[1].querySelector('.badge')).toBeNull();
    expect(rows[1].textContent).toContain('springboot-service summary');
    expect(rows[1].textContent).toContain('Catalog (catalog.acme.io)');
    expect(rows[1].querySelectorAll('[aria-label="not applicable"]').length).toBe(2);
    const links = [...rows[1].querySelectorAll('a')];
    expect(links.length).toBe(2);
    for (const a of links) {
      expect(a.getAttribute('href')).toBe('https://catalog.acme.io/create/templates/default/springboot-service');
      expect(a.getAttribute('target')).toBe('_blank');
      expect(a.getAttribute('rel')).toBe('noopener noreferrer');
    }
    // No link without a url: the source gives no address a browser opens.
    expect(rows[2].querySelector('a')).toBeNull();
    expect(rows[2].textContent).toContain('Open it in your Backstage');
    // The page count stays the declared total; the inferred count is in the filter.
    expect(target.querySelector('h1')?.parentElement?.parentElement?.textContent).toContain('1 golden path');
    expect(target.querySelector('[data-testid="golden-path-declaration"]')?.textContent).toContain('Inferred 2');
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ template: 'only', declaration: undefined }));
    done();
  });

  it('Accept opens the Declare panel with the template subject', async () => {
    entitiesFn.mockResolvedValue(withInferred([], [tpl('springboot-service')]));
    const { target, done } = mountView({ canAccept: true });
    const b = await vi.waitFor(() => { const x = target.querySelector<HTMLButtonElement>('[data-testid="inferred-accept"]'); expect(x).toBeTruthy(); return x!; });
    expect(b.textContent).toBe('Accept as golden path');
    b.click();
    await vi.waitFor(() => expect(declareFn).toHaveBeenCalledWith(expect.objectContaining({ serviceKey: 'template:default/springboot-service' })));
    expect(target.querySelector('[data-testid="golden-path-accept"] h2')?.textContent).toContain('springboot-service');
    // With only inferred rows the table shows them, never the empty state.
    expect(target.querySelector('[data-testid="golden-paths-empty"]')).toBeNull();
    (target.querySelector('[data-testid="golden-path-accept"] button') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(target.querySelector('[data-testid="golden-path-accept"]')).toBeNull());
    done();
  });

  it('offers no Accept for an unverified source, a taken name or without the right, and says why', async () => {
    entitiesFn.mockResolvedValue(withInferred([], [tpl('a', { acceptable: false }), tpl('b', { nameTaken: true }), tpl('c')]));
    const { target, done } = mountView({ canAccept: false });
    await vi.waitFor(() => expect(rowsOf(target).length).toBe(3));
    expect(target.querySelector('[data-testid="inferred-accept"]')).toBeNull();
    const why = [...target.querySelectorAll('[data-testid="inferred-no-accept"]')].map((e) => e.textContent);
    expect(why).toEqual(['Set dashboard.backstage.url to accept templates from this catalog', 'The name is held by a service that is no golden path', 'Accepting needs update pactos in the installation namespace']);
    done();
  });

  it('gives a declared path holding a catalog template name its Open in Backstage link and one mark', async () => {
    const declared = [tpl('http-service', { declaredAs: 'ghcr.io/acme/http-service' })];
    entitiesFn.mockResolvedValue(withInferred([pathRef('http-service'), pathRef('worker')], [], { declared, total: 0 }));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(rowsOf(target).length).toBe(2));
    const [first, second] = rowsOf(target);
    const a = first.querySelector<HTMLAnchorElement>('[data-testid="path-open-catalog"]')!;
    expect(a.getAttribute('href')).toBe('https://catalog.acme.io/create/templates/default/http-service');
    expect(a.getAttribute('rel')).toBe('noopener noreferrer');
    expect(first.querySelectorAll('.prov-mark').length).toBe(1);
    expect(first.querySelector('.prov-mark button')?.getAttribute('aria-label')).toBe('Inferred link');
    expect(second.querySelector('[data-testid="path-open-catalog"]')).toBeNull();
    done();
  });

  it('asks for inferred ones only with declaration=inferred and shows the active filter', async () => {
    entitiesFn.mockResolvedValue(withInferred([], [tpl('springboot-service')]));
    const { target, done } = mountView({ declaration: 'inferred' });
    await vi.waitFor(() => expect(rowsOf(target).length).toBe(1));
    expect(entitiesFn).toHaveBeenCalledWith(expect.objectContaining({ template: 'only', declaration: 'inferred' }));
    expect(rowsOf(target)[0].classList.contains('gp-inferred')).toBe(true);
    const pressed = target.querySelector('[data-testid="golden-path-declaration"] [aria-pressed="true"]');
    expect(pressed?.textContent).toContain('Inferred');
    expect(target.querySelector('.filter-chips')?.textContent).toContain('Inferred');
    (target.querySelector('[data-testid="golden-path-declaration"] button') as HTMLButtonElement).click();
    expect(location.hash).toBe('#/fleet/golden-paths');
    done();
  });

  it('opens the panel on load from declare and passes signed to it', async () => {
    entitiesFn.mockResolvedValue(withInferred([], [tpl('springboot-service')]));
    const { target, done } = mountView({ declare: 'template:default/springboot-service', signed: 'sha256:abc', canAccept: true });
    await vi.waitFor(() => expect(declareFn).toHaveBeenCalledWith(expect.objectContaining({ serviceKey: 'template:default/springboot-service', intentDigest: 'sha256:abc' })));
    expect(target.querySelector('[data-testid="golden-path-accept"]')).toBeTruthy();
    done();
  });

  it('says a partial catalog may be incomplete, every reason in the DOM', async () => {
    const limitations = [{ code: 'X', message: 'first reason' }, { code: 'Y', message: 'second reason' }, { code: 'Z', message: 'third reason' }];
    entitiesFn.mockResolvedValue(withInferred([], [tpl('a')], { completeness: 'partial', limitations }));
    const { target, done } = mountView();
    const line = await vi.waitFor(() => { const x = target.querySelector('[data-testid="inferred-partial"]'); expect(x).toBeTruthy(); return x!; });
    expect(line.querySelector('p')?.textContent).toBe('Inferred golden paths may be incomplete: first reason');
    expect(line.querySelector('details summary')?.textContent?.trim()).toBe('2 more reasons');
    expect(line.querySelector('details')?.textContent).toContain('second reason');
    expect(line.querySelector('details')?.textContent).toContain('third reason');
    expect(line.querySelector('[title]')).toBeNull();
    done();
  });

  it('shows five inferred rows, then reveals the rest', async () => {
    entitiesFn.mockResolvedValue(withInferred([], Array.from({ length: 8 }, (_, i) => tpl(`path-${i}`))));
    const { target, done } = mountView();
    await vi.waitFor(() => expect(rowsOf(target).length).toBe(5));
    const more = target.querySelector<HTMLButtonElement>('[data-testid="inferred-more"]')!;
    expect(more.textContent).toBe('Show 3 more inferred');
    expect(more.getAttribute('aria-expanded')).toBe('false');
    more.click();
    await vi.waitFor(() => expect(rowsOf(target).length).toBe(8));
    expect(more.getAttribute('aria-expanded')).toBe('true');
    done();
  });
});
