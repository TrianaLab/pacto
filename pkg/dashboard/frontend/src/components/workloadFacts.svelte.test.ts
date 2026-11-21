/**
 * The workload facts block (moved from the inferred service page so a declared
 * service's provenance fold shows the workload it replaced the same way): one mark per
 * value, and a value Pacto read but left out says so instead of "none".
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import WorkloadFacts from './WorkloadFacts.svelte';

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });

function render(workload: Record<string, unknown>) {
  const target = document.createElement('div'); document.body.appendChild(target);
  const c = mount(WorkloadFacts, { target, props: { workload } });
  flushSync();
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}

describe('WorkloadFacts', () => {
  it('shows the image, ports and probes with one mark each', () => {
    const t = render({
      source: 'k8s-workloads', namespace: 'billing', kind: 'Deployment', name: 'invoices',
      image: { value: 'ghcr.io/acme/invoices:0.1.0', from: 'spec.template.spec.containers[name=invoices].image' },
      ports: [{ port: 8080, name: 'http', protocol: 'TCP', from: 'spec.template.spec.containers[name=invoices].ports[0]' }],
      probes: [{ kind: 'readiness', path: '/healthz', from: 'spec.template.spec.containers[name=invoices].readinessProbe' }],
    });
    expect(t.querySelector('[data-testid="workload-facts"]')).toBeTruthy();
    expect(t.querySelector('[data-testid="inferred-image"]')?.getAttribute('title')).toBe('ghcr.io/acme/invoices:0.1.0');
    expect(t.textContent).toContain('8080 http TCP');
    expect(t.textContent).toContain('readiness: /healthz');
    expect(t.querySelectorAll('.prov-mark button')).toHaveLength(3);
  });

  it('says what it left out and what it never read', () => {
    const t = render({ source: 'k8s-workloads', namespace: 'billing', kind: 'Deployment', name: 'invoices', dropped: ['image'] });
    expect(t.textContent).toContain('The image was read but not shown (invalid value)');
    expect(t.textContent).toContain('No ports read');
    expect(t.textContent).toContain('No HTTP probe read on the main container');
  });
});
