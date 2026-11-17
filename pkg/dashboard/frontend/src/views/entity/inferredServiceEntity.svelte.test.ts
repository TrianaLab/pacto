/**
 * The inferred service page (509-S1): essentials first (identity, image, ports,
 * probes) with one mark per value, the calls with both empty states, and every path
 * behind one collapsed fold. No contract section appears.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
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

function render(detail: Record<string, unknown>) {
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(InferredServiceEntity, { target, props: { detail } });
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

  it('says what it did not see when no port or probe is declared', () => {
    const t = render(inferredDetail({ workload: { source: 'k8s-workloads', namespace: 'shop', kind: 'CronJob', name: 'nightly' } }));
    const text = t.textContent || '';
    expect(text).toContain('No ports are declared on this workload');
    expect(text).toContain('No HTTP probes are declared on this workload');
    expect(t.querySelectorAll('.prov-mark')).toHaveLength(0);
  });
});
