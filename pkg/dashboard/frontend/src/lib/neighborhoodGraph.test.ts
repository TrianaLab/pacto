/**
 * Tests for the pure ProductNeighborhood -> GraphData adapter: it
 * produces a graph node for EVERY returned node, keeps mixed kinds distinct, maps
 * dependency vs runs to distinct edge types, tints only a service-projection
 * observed-not-expected difference as drift, and NEVER invents a node or an edge to a
 * node the backend did not return.
 */
import { describe, it, expect } from 'vitest';
import { neighborhoodToGraph, cyEdgeId, edgeState, topoSignature, presentationSignature, groupByOwner, GROUP_THRESHOLD } from './neighborhoodGraph.ts';
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

describe('neighborhoodToGraph — revision labels', () => {
  it('puts a revision version on its own label line so truncation never cuts it', () => {
    const g = neighborhoodToGraph({
      nodes: [{ ref: { kind: 'revision', key: 'r1', label: 'payments-ledger-replica 12.4.0', version: '12.4.0' }, status: 'Compliant' }],
      edges: [],
    } as never);
    expect(g.nodes[0]).toMatchObject({ serviceName: 'payments-ledger-replica', version: '12.4.0' });
  });
});

describe('groupByOwner', () => {
  // A focus, `near` direct neighbors and `far` second-hop entities split over owners.
  const graph = (near: number, far: number, owners = ['team-a', 'team-b']): GraphData => {
    const nodes: GraphData['nodes'] = [{ id: 'focus', serviceName: 'focus', status: 'Compliant', depth: 0, edges: [] }];
    for (let i = 0; i < near; i++) nodes.push({ id: `n${i}`, serviceName: `n${i}`, status: 'Compliant', depth: 1, owner: owners[i % owners.length], edges: [{ targetId: 'focus' }] });
    for (let i = 0; i < far; i++) nodes.push({ id: `f${i}`, serviceName: `f${i}`, status: 'Compliant', depth: 2, owner: owners[i % owners.length], edges: [{ targetId: `n${i % Math.max(1, near)}`, edgeState: 'matched' }] });
    return { nodes };
  };

  it('leaves a graph at or under the threshold alone', () => {
    const g = graph(10, GROUP_THRESHOLD - 11);
    expect(groupByOwner(g)).toEqual({ graph: g, groups: [], folded: 0 });
    expect(groupByOwner(graph(10, 60), { all: true }).groups).toEqual([]);
  });

  it('folds the entities past the direct neighbors into one node per team, with folded edges', () => {
    const { graph: out, groups, folded } = groupByOwner(graph(4, 60));
    expect(groups).toEqual([{ owner: 'team-a', count: 30 }, { owner: 'team-b', count: 30 }]);
    expect(folded).toBe(60);
    const ids = out.nodes.map((n) => n.id);
    expect(ids).toContain('focus');
    expect(ids.filter((id) => /^n\d/.test(id))).toHaveLength(4);
    expect(ids.some((id) => /^f\d/.test(id))).toBe(false);
    const a = out.nodes.find((n) => n.id === 'group:team-a')!;
    expect(a).toMatchObject({ serviceName: 'team-a', version: '30 entities', group: 30, depth: 2 });
    // One edge per team and neighbor, never a state it cannot vouch for for every entity.
    expect(a.edges).toEqual([{ targetId: 'n0', type: undefined }, { targetId: 'n2', type: undefined }]);
  });

  it('folds the direct neighbors too when they alone are past the threshold, but never the focus or a team of one', () => {
    const g = graph(GROUP_THRESHOLD + 5, 0, ['team-a', 'team-b', 'solo']);
    g.nodes[g.nodes.length - 1].owner = 'only-me'; // a team of one stays itself
    const { graph: out, groups } = groupByOwner(g);
    expect(groups.map((x) => x.owner)).toEqual(['team-a', 'team-b', 'solo']);
    expect(out.nodes.map((n) => n.id)).toEqual(expect.arrayContaining(['focus', `n${GROUP_THRESHOLD + 4}`]));
    // Edges inside one team vanish; edges to the focus keep it.
    expect(out.nodes.find((n) => n.id === 'group:team-a')!.edges).toEqual([{ targetId: 'focus', type: undefined }]);
  });

  it('shows an opened team as its entities, and keeps an entity edge between two shown entities as it is', () => {
    const { graph: out, groups } = groupByOwner(graph(4, 60), { open: new Set(['team-a']) });
    expect(groups.map((x) => x.owner)).toEqual(['team-b']);
    expect(out.nodes.find((n) => n.id === 'f0')!.edges).toEqual([{ targetId: 'n0', edgeState: 'matched' }]);
  });

  it('groups entities with no owner together and returns the graph as it is when nothing folds', () => {
    const g = graph(2, 50, ['']);
    expect(groupByOwner(g).groups).toEqual([{ owner: 'No owner', count: 50 }]);
    const singles = graph(2, 50, Array.from({ length: 50 }, (_, i) => `t${i}`));
    expect(groupByOwner(singles)).toEqual({ graph: singles, groups: [], folded: 0 });
  });

  it('reads hop depth and owner from the neighborhood', () => {
    const g = neighborhoodToGraph({ nodes: [
      { ref: { kind: 'service', key: 'a', label: 'a' }, focus: true, depth: 0 },
      { ref: { kind: 'service', key: 'b', label: 'b' }, depth: 2, owner: 'team-b' },
      { ref: { kind: 'service', key: 'c', label: 'c' } },
    ] } as never);
    expect(g.nodes.map((n) => [n.depth, n.owner])).toEqual([[0, undefined], [2, 'team-b'], [undefined, undefined]]);
  });
});
