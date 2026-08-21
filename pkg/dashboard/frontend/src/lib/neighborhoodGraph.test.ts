/**
 * Tests for the pure ProductNeighborhood -> GraphData adapter: it
 * produces a graph node for EVERY returned node, keeps mixed kinds distinct, maps
 * dependency vs runs to distinct edge types, tints only a service-projection
 * observed-not-expected difference as drift, and NEVER invents a node or an edge to a
 * node the backend did not return.
 */
import { describe, it, expect } from 'vitest';
import { neighborhoodToGraph, cyEdgeId, edgeState, topoSignature, presentationSignature } from './neighborhoodGraph.ts';
import type { GraphData } from './graph.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const ref = (kind: string, key: string, label?: string, status?: string): any => ({ kind, key, label: label ?? key, status });

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const mixed: any = {
  nodes: [
    { ref: ref('target', 'prod/k8s/web', 'web'), status: 'Compliant', focus: true },
    { ref: ref('revision', 'domain-a/web@sha256:1', 'web@1'), status: 'Compliant' },
    { ref: ref('service', 'domain-a/api', 'api'), status: 'Unknown' },
    { ref: ref('owner', 'team-a', 'team-a') }, // an unusual kind falls back to service
  ],
  edges: [
    { from: ref('target', 'prod/k8s/web'), to: ref('revision', 'domain-a/web@sha256:1'), relation: 'runs' },
    { from: ref('target', 'prod/k8s/web'), to: ref('service', 'domain-a/api'), relation: 'dependency', serviceCorroboration: 'matched' },
    { from: ref('service', 'domain-a/api'), to: ref('service', 'ghost'), relation: 'dependency' }, // ghost is not a returned node
  ],
};

describe('neighborhoodToGraph', () => {
  it('produces a graph node for every returned node, keyed by canonical key', () => {
    const gd = neighborhoodToGraph(mixed);
    expect(gd.nodes).toHaveLength(4);
    expect(gd.nodes.map((n) => n.id).sort()).toEqual(
      ['domain-a/api', 'domain-a/web@sha256:1', 'prod/k8s/web', 'team-a'],
    );
    expect(gd.nodes.find((n) => n.id === 'prod/k8s/web')!.serviceName).toBe('web'); // label used for display
  });

  it('keeps mixed node kinds distinct (service/revision/target; unknown kinds -> service)', () => {
    const gd = neighborhoodToGraph(mixed);
    const kindOf = (id: string) => gd.nodes.find((n) => n.id === id)!.kind;
    expect(kindOf('prod/k8s/web')).toBe('target');
    expect(kindOf('domain-a/web@sha256:1')).toBe('revision');
    expect(kindOf('domain-a/api')).toBe('service');
    expect(kindOf('team-a')).toBe('service'); // owner falls back to a service-shaped node
  });

  it('maps dependency and runs to distinct edge types and attaches edges to the source', () => {
    const gd = neighborhoodToGraph(mixed);
    const target = gd.nodes.find((n) => n.id === 'prod/k8s/web')!;
    const types = (target.edges || []).map((e) => `${e.targetId}:${e.type}`).sort();
    expect(types).toEqual(['domain-a/api:dependency', 'domain-a/web@sha256:1:runs']);
  });

  it('drops an edge to a node the backend did not return (never invents a node/edge)', () => {
    const gd = neighborhoodToGraph(mixed);
    const api = gd.nodes.find((n) => n.id === 'domain-a/api')!;
    expect(api.edges).toHaveLength(0); // the api->ghost edge is dropped (ghost not returned)
    expect(gd.nodes.find((n) => n.id === 'ghost')).toBeUndefined();
  });

  it('tints only a service-projection observed-not-expected difference as drift', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const nb: any = {
      nodes: [{ ref: ref('service', 'a') }, { ref: ref('service', 'b') }, { ref: ref('service', 'c') }],
      edges: [
        { from: ref('service', 'a'), to: ref('service', 'b'), relation: 'dependency', difference: 'observed-not-expected' },
        { from: ref('service', 'a'), to: ref('service', 'c'), relation: 'dependency', difference: 'expected-not-observed' },
      ],
    };
    const gd = neighborhoodToGraph(nb);
    const a = gd.nodes.find((n) => n.id === 'a')!;
    expect(a.edges!.find((e) => e.targetId === 'b')!.driftStatus).toBe('drift');
    expect(a.edges!.find((e) => e.targetId === 'c')!.driftStatus).toBeUndefined();
  });

  it('is empty and safe for a null/empty neighborhood', () => {
    expect(neighborhoodToGraph(null).nodes).toHaveLength(0);
    expect(neighborhoodToGraph({}).nodes).toHaveLength(0);
    expect(neighborhoodToGraph({ nodes: [], edges: [] }).nodes).toHaveLength(0);
  });

  it('cyEdgeId reproduces the Cytoscape edge id format the engine assigns', () => {
    expect(cyEdgeId('prod/k8s/web', 'domain-a/api', 'dependency')).toBe('prod/k8s/web→domain-a/api:dependency');
    expect(cyEdgeId('prod/k8s/web', 'domain-a/web@sha256:1', 'runs')).toBe('prod/k8s/web→domain-a/web@sha256:1:runs');
  });

  it('carries a single visual edge state from difference OR service corroboration (Part 6)', () => {
    const gd = neighborhoodToGraph(mixed);
    const target = gd.nodes.find((n) => n.id === 'prod/k8s/web')!;
    // The target->api edge has serviceCorroboration matched -> visual state matched.
    expect(target.edges!.find((e) => e.targetId === 'domain-a/api')!.edgeState).toBe('matched');
    // The runs edge has neither difference nor corroboration -> no reconciliation state.
    expect(target.edges!.find((e) => e.targetId === 'domain-a/web@sha256:1')!.edgeState).toBeUndefined();
  });
});

describe('edgeState (backend verdict -> visual vocabulary, never re-inferred)', () => {
  it('maps a service-projection difference to the visual state', () => {
    expect(edgeState({ difference: 'matched' })).toBe('matched');
    expect(edgeState({ difference: 'observed-not-expected' })).toBe('drift');
    expect(edgeState({ difference: 'expected-not-observed' })).toBe('expected-not-observed');
    expect(edgeState({ difference: 'insufficient' })).toBe('insufficient');
  });
  it('maps a fine-grained service corroboration to the same visual states', () => {
    expect(edgeState({ serviceCorroboration: 'matched' })).toBe('matched');
    expect(edgeState({ serviceCorroboration: 'expected-not-observed' })).toBe('expected-not-observed');
    expect(edgeState({ serviceCorroboration: 'insufficient' })).toBe('insufficient');
  });
  it('prefers an edge-scope difference over service corroboration and defaults to none', () => {
    expect(edgeState({ difference: 'matched', serviceCorroboration: 'insufficient' })).toBe('matched');
    expect(edgeState({})).toBe('');
  });
});

describe('graph signatures (Part 5: topology vs presentation)', () => {
  const base = (): GraphData => ({
    nodes: [
      { id: 'a', serviceName: 'a', status: 'Compliant', kind: 'service', edges: [{ targetId: 'b', type: 'dependency', edgeState: 'matched' }] },
      { id: 'b', serviceName: 'b', status: 'Compliant', kind: 'service', edges: [] },
    ],
  });

  it('an identical graph has identical signatures', () => {
    expect(topoSignature(base())).toBe(topoSignature(base()));
    expect(presentationSignature(base())).toBe(presentationSignature(base()));
  });

  it('a status change alters the presentation signature but NOT the topology signature', () => {
    const changed = base();
    changed.nodes[0].status = 'NonCompliant';
    expect(topoSignature(changed)).toBe(topoSignature(base()));
    expect(presentationSignature(changed)).not.toBe(presentationSignature(base()));
  });

  it('a label change alters the presentation signature but NOT the topology signature', () => {
    const changed = base();
    changed.nodes[0].serviceName = 'a-renamed';
    expect(topoSignature(changed)).toBe(topoSignature(base()));
    expect(presentationSignature(changed)).not.toBe(presentationSignature(base()));
  });

  it('an edge reconciliation-state change alters presentation but NOT topology', () => {
    const changed = base();
    changed.nodes[0].edges![0].edgeState = 'drift';
    expect(topoSignature(changed)).toBe(topoSignature(base()));
    expect(presentationSignature(changed)).not.toBe(presentationSignature(base()));
  });

  it('adding a node/edge alters the topology signature', () => {
    const changed = base();
    changed.nodes.push({ id: 'c', serviceName: 'c', status: 'Compliant', kind: 'service', edges: [] });
    expect(topoSignature(changed)).not.toBe(topoSignature(base()));
  });
});
