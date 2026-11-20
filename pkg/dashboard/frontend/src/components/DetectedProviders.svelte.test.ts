/**
 * The Detected providers block on Data sources (509-S2): what Pacto found, how it knows
 * and what it uses it for, with the reasons of a degraded row collapsed, five rows
 * before "Show all", the clusters Pacto could not read named as "not read" and four
 * honest states (error, loading, no Kubernetes source, every Kubernetes source unread).
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';

const { providersFn } = vi.hoisted(() => ({ providersFn: vi.fn() }));
vi.mock('../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api.ts')>();
  return { ...actual, api: { fleetProviders: (...a: unknown[]) => providersFn(...a) } };
});

// @ts-expect-error — Svelte component has no declaration file
import DetectedProviders from './DetectedProviders.svelte';

type Src = { id: string; kind: string; status: string; error?: { message: string } };
const k8s = (id: string, status = 'available', error?: string): Src => ({ id, kind: 'kubernetes', status, ...(error ? { error: { message: error } } : {}) });
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const row = (id: string, over: Record<string, any> = {}) => ({
  id, source: 'k8s', name: id, evidence: `evidence of ${id}`, usedFor: ['inferred-services'], status: 'available', ...over,
});
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
const resp = (providers: any[], sources: Src[] = [k8s('k8s')]) => ({
  meta: { schemaVersion: 'pacto.dev/fleet-product/v1', completeness: 'complete', sources, sourceCounts: { total: sources.length } },
  total: providers.length, providers,
  kubernetesSources: sources.filter((s) => s.kind.startsWith('kubernetes')).length,
  notRead: sources.filter((s) => s.kind.startsWith('kubernetes') && s.status === 'unavailable'),
});

let mounted: { target: HTMLElement; component: unknown } | null = null;
function render(props: { refreshTick: number } = { refreshTick: 0 }) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(DetectedProviders, { target, props });
  mounted = { target, component };
  return target;
}
const block = (t: HTMLElement) => t.querySelector('[data-testid="detected-providers"]') as HTMLElement;
const rows = (t: HTMLElement) => [...t.querySelectorAll('[data-testid="provider-row"]')] as HTMLElement[];
const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();

beforeEach(() => { providersFn.mockReset(); });
afterEach(() => {
  if (mounted) { unmount(mounted.component as never); mounted.target.remove(); mounted = null; }
});

describe('DetectedProviders', () => {
  it('renders one row per provider with its name, evidence and use', async () => {
    providersFn.mockResolvedValue(resp([
      row('pacto-crd', { name: 'Pacto CRD', evidence: 'served (pacto.trianalab.io/v1alpha1)', usedFor: ['declared-targets'] }),
      row('kubernetes-workloads', { name: 'Kubernetes workloads', evidence: '3 Deployments' }),
    ]));
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
    expect(block(t).querySelector('h2')?.textContent).toBe('Detected providers');
    expect(text(rows(t)[0])).toContain('Pacto CRD');
    expect(text(rows(t)[0])).toContain('served (pacto.trianalab.io/v1alpha1)');
    // Tokens render with spaces, never with the wire's hyphen.
    expect(text(rows(t)[0])).toContain('declared targets');
    expect(text(rows(t)[1])).toContain('inferred services');
    expect(t.querySelector('[data-testid="providers-show-all"]')).toBeNull();
  });

  it('collapses the reasons of a partial row under "Why partial" and of a stale one under "Why stale"', async () => {
    providersFn.mockResolvedValue(resp([
      row('a', { status: 'partial', limitations: [{ code: 'X', message: 'Deployments unreadable' }] }),
      row('b', { status: 'stale', limitations: [{ code: 'Y', message: 'last read an hour ago' }] }),
    ]));
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
    const [a, b] = rows(t);
    const da = a.querySelector('details') as HTMLDetailsElement;
    expect(da.open).toBe(false);
    expect(text(da.querySelector('summary'))).toBe('Why partial');
    expect(text(da)).toContain('Deployments unreadable');
    expect(text(b.querySelector('details summary'))).toBe('Why stale');
    // The status word is text in the row, beside a dot the screen reader skips.
    expect(text(a)).toContain('Partial');
    expect(a.querySelector('.dp-dot')?.getAttribute('aria-hidden')).toBe('true');
  });

  it('keeps an available row free of a disclosure', async () => {
    providersFn.mockResolvedValue(resp([row('a')]));
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
    expect(rows(t)[0].querySelector('details')).toBeNull();
  });

  it('shows five rows, then "Show all N"', async () => {
    providersFn.mockResolvedValue(resp(Array.from({ length: 7 }, (_, i) => row(`p${i}`))));
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(5));
    const more = t.querySelector('[data-testid="providers-show-all"]') as HTMLButtonElement;
    expect(text(more)).toBe('Show all 7 providers');
    more.click(); flushSync();
    expect(rows(t)).toHaveLength(7);
    expect(text(t.querySelector('[data-testid="providers-show-all"]'))).toBe('Show fewer');
  });

  it('labels the cluster only when the rows come from two or more clusters', async () => {
    providersFn.mockResolvedValue(resp([row('a', { cluster: 'prod' }), row('b', { cluster: 'prod' })]));
    let t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
    expect(t.querySelector('.dp-cluster')).toBeNull();
    unmount(mounted!.component as never); mounted!.target.remove(); mounted = null;

    providersFn.mockResolvedValue(resp([row('a', { cluster: 'prod' }), row('b', { cluster: 'staging' })]));
    t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
    expect([...t.querySelectorAll('.dp-cluster')].map(text)).toEqual(['prod', 'staging']);
  });

  it('names an unavailable Kubernetes source as not read, with a link to it', async () => {
    providersFn.mockResolvedValue(resp([row('a')], [k8s('k8s'), k8s('edge', 'unavailable', 'connection refused')]));
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
    const lines = [...t.querySelectorAll('[data-testid="provider-not-read"]')];
    expect(lines).toHaveLength(1);
    expect(text(lines[0])).toContain('edge');
    expect(text(lines[0])).toContain('not read');
    expect(text(lines[0])).toContain('connection refused');
    expect(lines[0].querySelector('a')?.getAttribute('href')).toBe('#/fleet/sources/edge');
  });

  it('names at most three unread sources, then links the rest', async () => {
    const down = Array.from({ length: 5 }, (_, i) => k8s(`edge-${i}`, 'unavailable', 'timeout'));
    providersFn.mockResolvedValue(resp([row('a')], [k8s('k8s'), ...down]));
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
    expect(t.querySelectorAll('[data-testid="provider-not-read"]')).toHaveLength(3);
    const more = t.querySelector('[data-testid="providers-not-read-more"]') as HTMLAnchorElement;
    expect(text(more)).toBe('+2 more not read');
    expect(more.getAttribute('href')).toBe('#/fleet/sources?sourceHealth=unavailable');
  });

  it('says it could not load and offers a retry, never the empty sentence', async () => {
    providersFn.mockRejectedValue(new Error('boom'));
    const t = render();
    await vi.waitFor(() => expect(text(block(t))).toContain('Could not load the detected providers'));
    expect(text(block(t))).not.toContain('No providers detected');
    providersFn.mockResolvedValue(resp([row('a')]));
    (block(t).querySelector('button') as HTMLButtonElement).click();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
  });

  it('draws a skeleton and no sentence while loading', async () => {
    providersFn.mockReturnValue(new Promise(() => {}));
    const t = render();
    flushSync();
    expect(block(t).querySelector('.skeleton')).toBeTruthy();
    expect(text(block(t))).toBe('Detected providers');
  });

  it('says no providers were detected only when no Kubernetes source is read', async () => {
    providersFn.mockResolvedValue(resp([], [{ id: 'local', kind: 'local', status: 'available' }]));
    const t = render();
    await vi.waitFor(() => expect(text(block(t))).toContain('No providers detected. This view reads no Kubernetes cluster.'));
  });

  it('counts the workload source as a Kubernetes source', async () => {
    providersFn.mockResolvedValue(resp([], [{ id: 'w', kind: 'kubernetes-workloads', status: 'unavailable', error: { message: 'x' } }]));
    const t = render();
    await vi.waitFor(() => expect(t.querySelectorAll('[data-testid="provider-not-read"]')).toHaveLength(1));
    expect(text(block(t))).not.toContain('No providers detected');
  });

  it('shows only the not read lines when every Kubernetes source is unread and no row exists', async () => {
    providersFn.mockResolvedValue(resp([], [k8s('a', 'unavailable', 'x'), k8s('b', 'unavailable', 'y')]));
    const t = render();
    await vi.waitFor(() => expect(t.querySelectorAll('[data-testid="provider-not-read"]')).toHaveLength(2));
    expect(text(block(t))).not.toContain('No providers detected');
    expect(rows(t)).toHaveLength(0);
  });

  it('keeps the rows and says a refresh failed when a poll fails over data on hand', async () => {
    providersFn.mockResolvedValue(resp([row('a')]));
    const props = $state({ refreshTick: 0 });
    const t = render(props);
    await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
    providersFn.mockRejectedValue(new Error('blip'));
    props.refreshTick = 1;
    await vi.waitFor(() => expect(t.querySelector('[data-testid="stale-refresh"]')).toBeTruthy());
    expect(rows(t)).toHaveLength(1);
    expect(text(block(t))).not.toContain('Could not load the detected providers');
  });

  it('renders the rows the API returned even when no Kubernetes source is configured', async () => {
    providersFn.mockResolvedValue({ ...resp([row('registry')], [{ id: 'oci', kind: 'oci', status: 'available' }]) });
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
    expect(text(block(t))).not.toContain('No providers detected');
  });

  it('collapses the details of a row a source reported under "More details", unmuted and unmarked', async () => {
    const registry = row('registry:ghcr.io', { source: 'oci', name: 'Registry ghcr.io', evidence: 'no Referrers, no _catalog', usedFor: ['write-once-tags', 'index-listed-by-tag'],
      details: ['mode: referrers=write-once-tags listing=tags', 'cosign signatures: not checked'] });
    providersFn.mockResolvedValue({ ...resp([registry], [{ id: 'oci', kind: 'oci', status: 'available' }]) });
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
    const d = rows(t)[0].querySelectorAll('details');
    expect(d).toHaveLength(1);
    expect(d[0].open).toBe(false);
    expect(text(d[0].querySelector('summary'))).toBe('More details');
    expect(text(d[0])).toContain('mode: referrers=write-once-tags listing=tags');
    expect(text(rows(t)[0])).toContain('write once tags, index listed by tag');
    expect(rows(t)[0].classList.contains('dp-inferred')).toBe(false);
    expect(rows(t)[0].querySelector('.prov-mark')).toBeNull();
  });

  it('counts unread sources past the bound from notReadTotal', async () => {
    const down = Array.from({ length: 3 }, (_, i) => k8s(`edge-${i}`, 'unavailable', 'timeout'));
    providersFn.mockResolvedValue({ ...resp([row('a')], [k8s('k8s'), ...down]), notReadTotal: 60 });
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
    expect(text(t.querySelector('[data-testid="providers-not-read-more"]'))).toBe('+57 more not read');
    expect(t.querySelector('.dp-unread')?.classList.contains('t-meta')).toBe(false);
  });

  it('says when the server cut the list', async () => {
    providersFn.mockResolvedValue({ ...resp(Array.from({ length: 7 }, (_, i) => row(`p${i}`))), total: 260, truncated: true });
    const t = render();
    await vi.waitFor(() => expect(rows(t)).toHaveLength(5));
    expect(text(t.querySelector('[data-testid="providers-truncated"]'))).toBe('Showing 7 of 260 detected providers, reported before inferred, least healthy first.');
  });
  describe('inferred rows (510-S5)', () => {
    const AS_OF = new Date(Date.now() - 4 * 60_000).toISOString();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
    const inferred = (id: string, over: Record<string, any> = {}) => row(id, {
      source: 'cluster-detection', provenance: 'inferred', asOf: AS_OF, name: 'Registry',
      evidence: 'ghcr.io/acme in 9 of 11 namespaces, 87% of workloads', usedFor: ['delivery'],
      hint: 'oci://ghcr.io/acme/<service>:<version>', details: ['ghcr.io/acme 87%', 'docker.io/library 13%'], ...over,
    });
    beforeEach(() => { location.hash = '#/fleet/sources'; });
    afterEach(() => { location.hash = ''; });

    it('marks inferred rows with a tooltip naming source, age and completeness', async () => {
      providersFn.mockResolvedValue(resp([inferred('registry', { status: 'partial' }), row('pacto-crd')]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
      const [r, plain] = rows(t);
      expect(r.querySelector('.prov-mark button')?.getAttribute('aria-label')).toBe('Inferred: from cluster-detection');
      expect(text(r.querySelector('.prov-tip'))).toBe('Inferred from cluster-detection, as of 4 minutes ago, partial. Nothing was turned on. ghcr.io/acme in 9 of 11 namespaces, 87% of workloads');
      expect(plain.querySelector('.prov-mark')).toBeNull();
    });

    it('inferred tip carries the evidence', async () => {
      const evidence = 'shop/ghcr-pull named in the pod specs of 4 running workloads that pulled from ghcr.io; Pacto did not read the Secret';
      providersFn.mockResolvedValue(resp([inferred('pull-secret', { name: 'Pull Secret', evidence }), inferred('bare', { evidence: '' })]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
      expect(text(rows(t)[0].querySelector('.prov-tip'))).toContain(`Nothing was turned on. ${evidence}`);
      expect(text(rows(t)[1].querySelector('.prov-tip'))).toMatch(/Nothing was turned on\.$/);
    });

    it('declared row gets the declared mark', async () => {
      const installation = row('installation', { source: 'cluster-detection', provenance: 'declared', asOf: AS_OF, name: 'Installation',
        evidence: 'registry ghcr.io/acme; tags only under the listed prefixes, referrers by digest elsewhere', details: ['push prefixes ghcr.io/acme/pacto-targets'] });
      providersFn.mockResolvedValue(resp([installation, inferred('pull-secret', { name: 'Pull Secret' })]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
      const r = rows(t)[0];
      expect(r.querySelector('.prov-mark button')?.getAttribute('aria-label')).toBe("Declared: by the operator's configuration");
      expect(text(r.querySelector('.prov-tip'))).toBe("Declared by the operator's configuration, as of 4 minutes ago, complete.");
      expect(r.classList.contains('dp-inferred')).toBe(false);
      expect(text(r.querySelector('details summary'))).toBe('More details');
      // The Inferred toggle hides the proposal and keeps the declared row.
      (t.querySelector('[data-testid="providers-inferred-toggle"] input') as HTMLInputElement).click();
      window.dispatchEvent(new HashChangeEvent('hashchange')); flushSync();
      expect(rows(t).map((x) => text(x.querySelector('.dp-name bdi')))).toEqual(['Installation']);
    });

    it('names an unavailable inferred row as unknown and an available one as complete', async () => {
      providersFn.mockResolvedValue(resp([inferred('a', { status: 'unavailable' }), inferred('b')]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
      expect(text(rows(t)[0].querySelector('.prov-tip'))).toContain(', unknown.');
      expect(text(rows(t)[1].querySelector('.prov-tip'))).toContain(', complete.');
    });

    it('mutes inferred rows', async () => {
      providersFn.mockResolvedValue(resp([inferred('registry'), row('pacto-crd')]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
      expect(rows(t)[0].classList.contains('dp-inferred')).toBe(true);
      expect(rows(t)[1].classList.contains('dp-inferred')).toBe(false);
    });

    it('toggle hides inferred rows and writes inferred=0', async () => {
      providersFn.mockResolvedValue(resp([inferred('registry'), row('pacto-crd')]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
      const box = t.querySelector('[data-testid="providers-inferred-toggle"] input') as HTMLInputElement;
      expect(box.checked).toBe(true);
      box.click();
      expect(location.hash).toBe('#/fleet/sources?inferred=0');
      window.dispatchEvent(new HashChangeEvent('hashchange')); flushSync();
      expect(rows(t)).toHaveLength(1);
      expect(text(rows(t)[0])).toContain('pacto-crd');
      expect(text(t.querySelector('[data-testid="providers-inferred-hidden"]'))).toBe('1 inferred provider hidden.');
      (t.querySelector('[data-testid="providers-inferred-toggle"] input') as HTMLInputElement).click();
      expect(location.hash).toBe('#/fleet/sources');
    });

    it('inferred=0 in location.hash hides inferred rows on mount', async () => {
      location.hash = '#/fleet/sources?inferred=0';
      window.dispatchEvent(new HashChangeEvent('hashchange'));
      providersFn.mockResolvedValue(resp([inferred('registry'), row('pacto-crd')]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
      expect((t.querySelector('[data-testid="providers-inferred-toggle"] input') as HTMLInputElement).checked).toBe(false);
    });

    it('says inferred rows are hidden, never that nothing was detected, when no Kubernetes source is read', async () => {
      location.hash = '#/fleet/sources?inferred=0';
      window.dispatchEvent(new HashChangeEvent('hashchange'));
      providersFn.mockResolvedValue(resp([inferred('registry')], []));
      const t = render();
      await vi.waitFor(() => expect(t.querySelector('[data-testid="providers-inferred-hidden"]')).not.toBeNull());
      expect(text(t.querySelector('[data-testid="providers-inferred-hidden"]'))).toBe('1 inferred provider hidden.');
      expect(text(block(t))).not.toContain('No providers detected');
      expect(rows(t)).toHaveLength(0);
    });

    it('offers no toggle when no row is inferred', async () => {
      providersFn.mockResolvedValue(resp([row('pacto-crd')]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
      expect(t.querySelector('[data-testid="providers-inferred-toggle"]')).toBeNull();
    });

    it('details stay collapsed until opened', async () => {
      providersFn.mockResolvedValue(resp([inferred('registry', { status: 'partial', limitations: [{ code: 'DETECTION_REGISTRY_PARTIAL', message: 'no clear majority' }] })]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
      const d = rows(t)[0].querySelectorAll('details');
      // One disclosure per row: the reasons, the details and the hint together.
      expect(d).toHaveLength(1);
      expect(d[0].open).toBe(false);
      expect(text(d[0].querySelector('summary'))).toBe('More details');
      expect(text(d[0])).toContain('no clear majority');
      expect(text(d[0])).toContain('docker.io/library 13%');
    });

    it('hint copies the exact command', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined);
      Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
      providersFn.mockResolvedValue(resp([inferred('registry')]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
      (rows(t)[0].querySelector('.copyable-btn') as HTMLButtonElement).click();
      expect(writeText).toHaveBeenCalledWith('oci://ghcr.io/acme/<service>:<version>');
    });

    it('a redacted row shows no hint', async () => {
      providersFn.mockResolvedValue(resp([inferred('registry', { evidence: 'Registry found in a namespace you cannot read', hint: undefined, details: undefined })]));
      const t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
      expect(rows(t)[0].querySelector('.copyable')).toBeNull();
      expect(rows(t)[0].querySelector('details')).toBeNull();
      expect(text(rows(t)[0])).toContain('Registry found in a namespace you cannot read');
    });

    it('adds no block', async () => {
      providersFn.mockResolvedValue(resp([row('pacto-crd')]));
      let t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(1));
      const before = [t.querySelectorAll('section').length, t.querySelectorAll('h2, h3').length];
      unmount(mounted!.component as never); mounted!.target.remove(); mounted = null;
      providersFn.mockResolvedValue(resp([inferred('registry'), row('pacto-crd')]));
      t = render();
      await vi.waitFor(() => expect(rows(t)).toHaveLength(2));
      expect([t.querySelectorAll('section').length, t.querySelectorAll('h2, h3').length]).toEqual(before);
    });
  });
});
