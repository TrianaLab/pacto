import { describe, it, expect } from 'vitest';
import { nodeDrawerData } from './nodeDrawer';

const graphData = {
  nodes: [
    { id: 'checkout', serviceName: 'checkout', status: 'Compliant', version: '1.0.0',
      edges: [{ targetId: 'payments', targetName: 'payments', type: 'dependency', required: true }] },
    { id: 'payments', serviceName: 'payments', status: 'Warning', version: '2.0.0', edges: [] },
  ],
};
const services = [
  { name: 'checkout', version: '1.0.0', owner: { team: 'team/checkout' }, sources: ['k8s'], blastRadius: 0 },
  { name: 'payments', version: '2.0.0', owner: { team: 'team/payments' }, sources: ['oci'], blastRadius: 1 },
];

describe('nodeDrawerData', () => {
  it('returns null for unknown or empty selection', () => {
    expect(nodeDrawerData('', services, graphData)).toBeNull();
    expect(nodeDrawerData('nope', services, graphData)).toBeNull();
  });

  it('collects a node dependencies from its edges', () => {
    const d = nodeDrawerData('checkout', services, graphData);
    expect(d?.dependencies).toEqual([{ name: 'payments', type: 'dependency', required: true }]);
    expect(d?.dependents).toEqual([]);
    expect(d?.owner).toEqual({ team: 'team/checkout' });
  });

  it('collects dependents by scanning edges that target the node', () => {
    const d = nodeDrawerData('payments', services, graphData);
    expect(d?.dependents).toEqual(['checkout']);
    expect(d?.blastRadius).toBe(1);
    expect(d?.status).toBe('Warning');
  });
});
