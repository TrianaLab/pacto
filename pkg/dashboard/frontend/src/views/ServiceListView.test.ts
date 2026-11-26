/**
 * The services list's empty state reads /api/sources: an egress row (a provider that
 * reads calls leaving the cluster) is no data source, so it never counts as one.
 */
import { describe, it, expect } from 'vitest';
import { mount, unmount } from 'svelte';

// @ts-expect-error — Svelte component has no declaration file
import ServiceListView from './ServiceListView.svelte';

function render(sourcesInfo: unknown[]) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ServiceListView, { target, props: { services: [], sourcesInfo } });
  return { target, done: () => { unmount(c); document.body.removeChild(target); } };
}

describe('ServiceListView — egress rows', () => {
  it('an egress row alone keeps the setup screen', () => {
    const { target, done } = render([{ type: 'kernel-egress', enabled: true, egress: true, coverage: 'partial', reason: 'node agent on 3/4 nodes' }]);
    expect(target.textContent).toContain('No data sources are available');
    expect(target.textContent).not.toContain('node agent on 3/4 nodes');
    done();
  });

  it('an egress row is never listed as a disabled source', () => {
    const { target, done } = render([
      { type: 'oci', enabled: false, reason: 'authentication with the source failed' },
      { type: 'cilium-fqdn', enabled: false, egress: true, coverage: 'unavailable', reason: 'no CiliumNetworkPolicy CRD' },
    ]);
    const reasons = Array.from(target.querySelectorAll('.source-reason')).map((n) => n.textContent);
    expect(reasons).toHaveLength(1);
    expect(reasons[0]).toContain('oci: authentication with the source failed');
    done();
  });

  it('a managed-resources row is no data source, enabled or not', () => {
    const { target, done } = render([
      { type: 'managed-resources', enabled: true, resources: true, reason: 'lists the catalog kinds of rds.aws.upbound.io' },
      { type: 'cloud-inventory', enabled: false, resources: true, reason: 'set controller.cloudInventory' },
    ]);
    expect(target.textContent).toContain('No data sources are available');
    expect(target.textContent).not.toContain('rds.aws.upbound.io');
    expect(target.querySelectorAll('.source-reason')).toHaveLength(0);
    done();
  });
});
