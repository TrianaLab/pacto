/**
 * The inferred service page (509-S1): essentials first (identity, image, ports,
 * probes) with one mark per value, the calls with both empty states, and every path
 * behind one collapsed fold. No contract section appears.
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
const { planFn } = vi.hoisted(() => ({ planFn: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return { ...actual, api: { fleetDeclare: (...a: unknown[]) => planFn(...a) } };
});
// @ts-expect-error — Svelte component has no declaration file
import InferredServiceEntity from './InferredServiceEntity.svelte';

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });

const preview = (items: unknown[]) => ({ total: items.length, count: items.length, truncated: false, items });
const IMAGE_FROM = 'spec.template.spec.containers[name=cart].image';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture
function inferredDetail(patch: Record<string, any> = {}): Record<string, any> {
  return {
    meta: { schemaVersion: 'pacto.dev/fleet-product/v1', asOf: '2026-11-16T10:00:00Z', completeness: 'complete', sources: [] },
    entity: { kind: 'service', key: 'k8s:shop%2FDeployment%2Fcart', label: 'k8s:shop/Deployment/cart', declaration: 'inferred', scope: 'shop', secondary: 'Deployment' },
    actions: [],
    service: {
      workload: {
        source: 'k8s-workloads', namespace: 'shop', kind: 'Deployment', name: 'cart', observedAt: '2026-11-16T09:59:00Z',
        image: { value: 'ghcr.io/acme/cart@sha256:0123456789abcdef0123456789abcdef', from: IMAGE_FROM },
        ports: [{ port: 8080, name: 'http', protocol: 'TCP', from: 'spec.template.spec.containers[name=cart].ports[0]' }],
        probes: [{ kind: 'readiness', path: '/healthz', port: 'http', from: 'spec.template.spec.containers[name=cart].readinessProbe' }],
      },
      calls: preview([]), callsObserved: false,
      limitations: preview([{ limitation: { code: 'DECLARATION_UNVERIFIED', source: 'k8s', message: 'Pacto could not read Pacto targets in shop; a contract may describe this workload' } }]),
      revisions: preview([]), dependencies: preview([]),
      ...patch,
    },
  };
}

function render(detail: Record<string, unknown>, more: Record<string, unknown> = {}) {
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(InferredServiceEntity, { target, props: { detail, ...more } });
  flushSync();
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}

describe('InferredServiceEntity', () => {
  it('shows the identity, image, ports and probes first, one mark per value', () => {
    const t = render(inferredDetail());
    const text = t.textContent || '';
    expect(t.querySelector('[data-testid="inferred-identity"]')?.textContent).toContain('namespace shop, Deployment. Inferred from k8s-workloads');
    expect(text).toContain('ghcr.io/acme/cart@sha256:');
    expect(text).toContain('8080 http TCP');
    expect(text).toContain('readiness: /healthz port http');
    const marks = [...t.querySelectorAll('.prov-mark button')];
    expect(marks).toHaveLength(3);
    expect(marks[0].getAttribute('aria-label')).toBe('Inferred service: from k8s-workloads');
    expect(t.querySelector('.prov-mark .prov-tip')?.textContent).toBe(`Inferred from k8s-workloads: ${IMAGE_FROM}`);
  });

  it('keeps the paths behind one collapsed fold and draws no contract section', () => {
    const t = render(inferredDetail());
    const fold = t.querySelector('[data-testid="inferred-paths"]') as HTMLDetailsElement;
    expect(fold.open).toBe(false);
    expect(fold.textContent).toContain(IMAGE_FROM);
    const text = t.textContent || '';
    for (const contract of ['Revisions', 'Expected dependencies', 'Operational targets', 'Owner']) expect(text).not.toContain(contract);
  });

  it('renders the image and every path in breakable elements, the full image in its title', () => {
    const t = render(inferredDetail());
    const img = t.querySelector('[data-testid="inferred-image"]') as HTMLElement;
    expect(img.tagName).toBe('BDI');
    expect(img.classList.contains('ie-value')).toBe(true);
    expect(img.getAttribute('title')).toBe('ghcr.io/acme/cart@sha256:0123456789abcdef0123456789abcdef');
    const paths = [...t.querySelectorAll('.ie-path')];
    expect(paths.map((p) => p.tagName)).toEqual(['BDI', 'BDI', 'BDI']);
  });

  it('says calls are unknown without a traffic source', () => {
    const t = render(inferredDetail());
    expect(t.querySelector('[data-testid="inferred-calls"]')?.textContent).toContain('Calls unknown: Pacto reads no traffic source here');
  });

  it('says no calls were seen when a traffic source covers it', () => {
    const t = render(inferredDetail({ callsObserved: true }));
    const calls = t.querySelector('[data-testid="inferred-calls"]')?.textContent || '';
    expect(calls).toContain('No calls to declared services seen as of');
    expect(calls).not.toContain('Calls unknown');
  });

  it('lists the declared services it was seen calling', () => {
    const t = render(inferredDetail({ callsObserved: true, calls: preview([{ kind: 'service', key: 'payments', label: 'payments', href: '/fleet/services/payments' }]) }));
    const link = t.querySelector('[data-testid="inferred-calls"] a.entity-link') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('#/fleet/services/payments');
  });

  it('says what it did not read when no port or probe was read', () => {
    const t = render(inferredDetail({ workload: { source: 'k8s-workloads', namespace: 'shop', kind: 'CronJob', name: 'nightly' } }));
    const text = t.textContent || '';
    expect(text).toContain('No ports read');
    expect(text).toContain('No HTTP probe read on the main container');
    expect(text).not.toContain('declared on this workload');
    expect(t.querySelectorAll('.prov-mark')).toHaveLength(0);
  });

  it('says a dropped value was read, never that none is declared', () => {
    const t = render(inferredDetail({ workload: { source: 'k8s-workloads', namespace: 'shop', kind: 'Deployment', name: 'cart', dropped: ['image', 'ports[0]', 'probes[0]'] } }));
    const text = t.textContent || '';
    expect(text).toContain('The image was read but not shown (invalid value)');
    expect(text).toContain('Ports were read but not shown (invalid value)');
    expect(text).toContain('Probes were read but not shown (invalid value)');
  });

  it('never states no contract describes a workload whose declaration is unverified', () => {
    const unverified = inferredDetail();
    unverified.service.workload.declarationUnverified = true;
    const id = render(unverified).querySelector('[data-testid="inferred-identity"]')?.textContent || '';
    expect(id).toContain('Pacto could not read the Pacto targets in shop, so a contract may describe this workload.');
    expect(id).not.toContain('No contract describes');
    cleanup?.(); cleanup = null;
    expect(render(inferredDetail()).querySelector('[data-testid="inferred-identity"]')?.textContent).toContain('No contract describes this workload.');
  });

  it('counts calls to hidden services instead of saying none were seen', () => {
    const calls = render(inferredDetail({ callsObserved: true, hiddenDependencies: 2 })).querySelector('[data-testid="inferred-calls"]')?.textContent || '';
    expect(calls).toContain('2 calls to services you cannot see');
    expect(calls).not.toContain('No calls');
  });

  // 509-S3: how the cluster groups the workload and the repository its GitOps object names.
  const ARGO = { rule: 'argocd', namespace: 'shop', name: 'cart', from: 'annotation argocd.argoproj.io/tracking-id', key: 'argocd/shop/cart', label: 'Argo CD Application shop/cart' };
  const withWorkload = (patch: Record<string, unknown>) => {
    const d = inferredDetail();
    Object.assign(d.service.workload, patch);
    return d;
  };

  it('shows the group with one mark naming the rule and where it was read, and links its services', () => {
    const t = render(withWorkload({ group: ARGO }));
    const row = t.querySelector('[data-testid="inferred-group"]') as HTMLElement;
    expect(row.querySelector('bdi')?.textContent).toBe('Argo CD Application shop/cart');
    expect(row.querySelector('.prov-tip')?.textContent).toBe('Grouped by Argo CD Application: read from annotation argocd.argoproj.io/tracking-id');
    const link = row.querySelector('a') as HTMLAnchorElement;
    expect(link.textContent).toBe('Services in this group');
    expect(link.getAttribute('href')).toBe('#/fleet/services?declaration=inferred&group=argocd%2Fshop%2Fcart');
  });

  it('gives no group link to a workload grouped by itself, and says why an ambiguous one stays ungrouped', () => {
    const own = { rule: 'workload', namespace: 'shop', name: 'Deployment/cart', from: 'no grouping label or annotation', key: 'workload/shop/Deployment/cart', label: 'This workload only' };
    let t = render(withWorkload({ group: own }));
    let row = t.querySelector('[data-testid="inferred-group"]') as HTMLElement;
    expect(row.textContent).toContain('This workload only');
    expect(row.querySelector('a')).toBeNull();
    expect(row.querySelector('.prov-tip')?.textContent).toBe('Not grouped: no grouping label or annotation');
    expect(t.querySelector('[data-testid="inferred-paths"]')?.textContent).not.toContain('Group');
    cleanup?.(); cleanup = null;
    const why = 'label argocd.argoproj.io/instance names cart and annotation argocd.argoproj.io/tracking-id names checkout';
    t = render(withWorkload({ group: { ...own, label: 'Ungrouped', ambiguous: why } }));
    row = t.querySelector('[data-testid="inferred-group"]') as HTMLElement;
    expect(row.querySelector('bdi')?.textContent).toBe('Ungrouped');
    expect(row.querySelector('.t-meta')?.textContent).toBe(why);
    expect(row.querySelector('a')).toBeNull();
    // The tip never contradicts the claims the row names.
    expect(row.querySelector('.prov-tip')?.textContent).toBe(`Ungrouped: ${why}`);
    expect(t.querySelector('[data-testid="inferred-paths"]')?.textContent).not.toContain('no grouping label');
  });

  it('says a group Pacto read but left out was read, never that no label exists', () => {
    const left = { rule: 'workload', namespace: 'shop', name: 'Deployment/cart', from: 'a grouping value Pacto read but left out (invalid)', key: 'workload/shop/Deployment/cart', label: 'This workload only' };
    const row = render(withWorkload({ group: left, dropped: ['group'] })).querySelector('[data-testid="inferred-group"]') as HTMLElement;
    expect(row.querySelector('.t-meta')?.textContent).toBe('The group was read but not shown (invalid value)');
    expect(row.querySelector('.prov-tip')?.textContent).toBe('Not grouped: a grouping value Pacto read but left out (invalid)');
  });

  it('links an https repository, prints any other as text and keeps the path', () => {
    const t = render(withWorkload({ group: ARGO, repositories: [
      { url: 'https://github.com/acme/shop', path: 'deploy/cart', from: 'Application argocd/cart spec.source.repoURL', namespace: 'argocd', kind: 'Application' },
      { url: 'github.com:acme/infra', from: 'Application argocd/cart spec.sources[1].repoURL', namespace: 'argocd', kind: 'Application' },
    ] }));
    const row = t.querySelector('[data-testid="inferred-repository"]') as HTMLElement;
    const links = [...row.querySelectorAll('a')];
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute('href')).toBe('https://github.com/acme/shop');
    expect(links[0].getAttribute('rel')).toBe('noopener noreferrer');
    expect(links[0].getAttribute('target')).toBe('_blank');
    expect(row.textContent).toContain('github.com:acme/infra');
    expect(row.textContent).toContain('path deploy/cart');
    expect([...row.querySelectorAll('bdi')].every((b) => b.getAttribute('title'))).toBe(true);
    expect(row.querySelector('.prov-tip')?.textContent).toBe('Read from Application argocd/cart spec.source.repoURL, Application argocd/cart spec.sources[1].repoURL');
    expect(row.textContent).not.toContain('Repository unknown');
    const fold = t.querySelector('[data-testid="inferred-paths"]')?.textContent || '';
    expect(fold).toContain('annotation argocd.argoproj.io/tracking-id');
    expect(fold).toContain('Application argocd/cart spec.sources[1].repoURL');
  });

  it('says why the repository is unknown, muted, and draws no repository row for a Helm release', () => {
    const row = render(withWorkload({ group: ARGO, repositoryUnknown: 'Applications unreadable (forbidden)' })).querySelector('[data-testid="inferred-repository"]') as HTMLElement;
    expect(row.querySelector('.t-meta')?.textContent).toBe('Repository unknown: Applications unreadable (forbidden)');
    expect(row.querySelector('.prov-mark')).toBeNull();
    cleanup?.(); cleanup = null;
    const helm = { rule: 'helm', namespace: 'shop', name: 'exporter', from: 'annotations meta.helm.sh/release-name and release-namespace', key: 'helm/shop/exporter', label: 'Helm release shop/exporter' };
    const t = render(withWorkload({ group: helm }));
    expect(t.querySelector('[data-testid="inferred-group"]')).not.toBeNull();
    expect(t.querySelector('[data-testid="inferred-repository"]')).toBeNull();
  });

  describe('Declare this service', () => {
    const declare = { nextAction: { verb: 'declare', cli: 'pacto declare cart --namespace shop' } };
    const ANSWER = { plan: { version: '0.1.0', deploy: 'none', ranges: [], signer: { class: 'agent', source: 'none' }, intentDigest: 'sha256:i', contract: { text: '' }, record: { text: '' } },
      signing: { mode: 'none', reason: 'r' }, write: { available: false, reason: 'w' }, command: 'pacto declare cart' };

    it('draws the fold only with the capability and a declare action, after the identity', () => {
      planFn.mockReset();
      expect(render(inferredDetail(declare)).querySelector('[data-testid="declare-panel"]')).toBeNull();
      cleanup?.();
      expect(render(inferredDetail(), { canDeclare: true }).querySelector('[data-testid="declare-panel"]')).toBeNull();
      cleanup?.();
      expect(render(inferredDetail({ nextAction: { verb: 'edit-contract', cli: 'pacto ci' } }), { canDeclare: true }).querySelector('[data-testid="declare-panel"]')).toBeNull();
      cleanup?.();
      const t = render(inferredDetail(declare), { canDeclare: true });
      const fold = t.querySelector('[data-testid="declare-panel"]') as HTMLDetailsElement;
      expect(fold.querySelector('summary')?.textContent).toContain('Declare this service');
      expect(t.querySelector('[data-testid="inferred-identity"]')?.nextElementSibling).toBe(fold);
      expect(t.querySelectorAll('[data-testid="declare-panel"]')).toHaveLength(1);
      expect(t.querySelector('[data-testid="inferred-next-action"]')).toBeNull();
    });

    it('says the declare command where the host plans no declaration', () => {
      planFn.mockReset();
      const t = render(inferredDetail(declare));
      expect(t.querySelector('[data-testid="inferred-next-action"]')?.textContent).toBe('Fix: declare the workload with pacto declare cart --namespace shop');
      expect(planFn).not.toHaveBeenCalled();
      cleanup?.();
      expect(render(inferredDetail({ nextAction: { verb: 'edit-contract', cli: 'pacto ci' } })).querySelector('[data-testid="inferred-next-action"]')).toBeNull();
    });

    it('starts closed and asks for no plan until opened', async () => {
      planFn.mockReset(); planFn.mockResolvedValue(ANSWER);
      const t = render(inferredDetail(declare), { canDeclare: true });
      const fold = t.querySelector('[data-testid="declare-panel"]') as HTMLDetailsElement;
      expect(fold.open).toBe(false);
      expect(planFn).not.toHaveBeenCalled();
      fold.open = true;
      fold.dispatchEvent(new Event('toggle'));
      flushSync();
      await vi.waitFor(() => expect(planFn).toHaveBeenCalledTimes(1));
      expect(planFn.mock.calls[0][0]).toMatchObject({ serviceKey: 'k8s:shop%2FDeployment%2Fcart' });
    });

    it('opens with declare=1 and carries the signed intent to the panel', async () => {
      planFn.mockReset(); planFn.mockResolvedValue(ANSWER);
      const t = render(inferredDetail(declare), { canDeclare: true, declareQuery: { open: true, signed: 'sha256:i' } });
      expect((t.querySelector('[data-testid="declare-panel"]') as HTMLDetailsElement).open).toBe(true);
      await vi.waitFor(() => expect(planFn).toHaveBeenCalledTimes(1));
      expect(planFn.mock.calls[0][0]).toMatchObject({ intentDigest: 'sha256:i' });
    });
  });
});

describe('the inferred service page: what a catalog reports', () => {
  const fact = (value: string) => ({ value, source: 'backstage-catalog', where: 'component:default/cart', asOf: '2026-11-14T12:00:00Z' });

  it('shows an inferred owner and domain as muted text, one mark each', () => {
    const t = render(inferredDetail({ ownership: { declared: false, provenance: 'inferred', inferred: fact('team:cart') }, inferredBusinessDomain: fact('shop') }));
    expect(t.querySelector('[data-testid="owner-inferred"]')?.textContent).toBe('inferred: cart');
    expect(t.querySelector('[data-testid="domain-inferred"]')?.textContent).toBe('inferred: shop');
    expect(t.querySelectorAll('[data-testid="inferred-catalog"] button[aria-label^="About the inferred"]')).toHaveLength(2);
  });

  it('adds nothing when no source reports one', () => {
    const t = render(inferredDetail({ ownership: { declared: false } }));
    expect(t.querySelector('[data-testid="inferred-catalog"]')).toBeNull();
  });
});
