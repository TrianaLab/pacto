/**
 * Tests for the pure ProductNeighborhood -> GraphData adapter: it
 * produces a graph node for EVERY returned node, keeps mixed kinds distinct, maps
 * dependency vs runs to distinct edge types, tints only a service-projection
 * observed-not-expected difference as drift, and NEVER invents a node or an edge to a
 * node the backend did not return.
 */
import { describe, it, expect } from 'vitest';
import { neighborhoodToGraph, cyEdgeId, edgeState, topoSignature, presentationSignature, laneGraph, sidesOf, GROUP_THRESHOLD, MAX_GROUPS_PER_SIDE, LANE_CAP } from './neighborhoodGraph.ts';
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

  it("a group's count or a group edge's words alter presentation but NOT topology", () => {
    const counted = base();
    counted.nodes[0].version = '7 entities';
    const worded = base();
    worded.nodes[0].edges![0].label = 'observed, not expected (3)';
    for (const changed of [counted, worded]) {
      expect(topoSignature(changed)).toBe(topoSignature(base()));
      expect(presentationSignature(changed)).not.toBe(presentationSignature(base()));
    }
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

describe('laneGraph', () => {
  type N = GraphData['nodes'][number];
  const node = (id: string, depth: number, owner?: string, edges: N['edges'] = []): N => ({ id, serviceName: id, status: 'Compliant', depth, ...(owner ? { owner } : {}), edges });
  // A focus with `near` dependencies (focus -> n) and `far` entities a hop past them
  // (n -> f), split over owners.
  const graph = (near: number, far: number, owners = ['team-a', 'team-b']): GraphData => {
    const nodes: N[] = [node('focus', 0, undefined, Array.from({ length: near }, (_, i) => ({ targetId: `n${i}` })))];
    for (let i = 0; i < near; i++) nodes.push(node(`n${i}`, 1, owners[i % owners.length], Array.from({ length: far }, (_, k) => k).filter((k) => k % Math.max(1, near) === i).map((k) => ({ targetId: `f${k}`, edgeState: k < 2 ? 'drift' : 'matched' }))));
    for (let i = 0; i < far; i++) nodes.push(node(`f${i}`, 2, owners[i % owners.length]));
    return { nodes };
  };
  const byId = (g: GraphData, id: string) => g.nodes.find((n) => n.id === id)!;

  it('puts each entity on a side: what the focus points at, what points at it, and the rest by who reached them', () => {
    const g: GraphData = { nodes: [
      node('focus', 0, undefined, [{ targetId: 'dep' }, { targetId: 'both' }, { targetId: 'focus' }]),
      node('dep', 1, undefined, [{ targetId: 'dep2' }]),
      node('both', 1, undefined, [{ targetId: 'focus' }]),
      node('user', 1, undefined, [{ targetId: 'focus' }]),
      node('dep2', 2), node('sibling', 2, undefined, [{ targetId: 'user' }]), node('island', 2),
    ] };
    expect(Object.fromEntries(sidesOf(g, 'focus'))).toEqual({
      dep: 'depends-on', both: 'depends-on', user: 'used-by', dep2: 'depends-on', sibling: 'used-by', island: 'used-by',
    });
  });

  it('returns a graph with no focus as it is', () => {
    const g: GraphData = { nodes: [node('a', 1), node('b', 1)] };
    expect(laneGraph(g)).toEqual({ graph: g, groups: [], folded: 0 });
  });

  it('draws a small neighborhood in lanes without folding anything', () => {
    const g = graph(2, 3);
    g.nodes.push(node('user', 1, 'team-a', [{ targetId: 'focus', edgeState: 'matched' }]));
    const { graph: out, groups, folded } = laneGraph(g);
    expect([groups, folded]).toEqual([[], 0]);
    // Lanes first, so the boxes exist before what goes in them.
    expect(out.nodes.slice(0, 2)).toEqual([
      { id: 'lane:depends-on', serviceName: 'Depends on (2)', status: 'lane', kind: 'service', lane: true, side: -1, edges: [] },
      { id: 'lane:used-by', serviceName: 'Used by (1)', status: 'lane', kind: 'service', lane: true, side: 1, edges: [] },
    ]);
    expect(byId(out, 'focus')).toMatchObject({ side: 0 });
    expect(byId(out, 'focus').parent).toBeUndefined();
    expect(byId(out, 'n0')).toMatchObject({ side: -1, parent: 'lane:depends-on' });
    expect(byId(out, 'user')).toMatchObject({ side: 1, parent: 'lane:used-by', edges: [{ targetId: 'focus', edgeState: 'matched' }] });
    // A far entity sits outside the lane, a column further out.
    expect(byId(out, 'f0')).toMatchObject({ side: -1, depth: 2 });
    expect(byId(out, 'f0').parent).toBeUndefined();
    expect(out.nodes).toHaveLength(2 + g.nodes.length);
    expect(laneGraph(graph(4, 60), { all: true }).groups).toEqual([]);
  });

  it('folds everything two hops out into one group per team and side, with one edge to its lane', () => {
    const { graph: out, groups, folded } = laneGraph(graph(4, 60));
    expect(groups).toEqual([
      { id: 'group:depends-on:team-a', owner: 'team-a', side: 'depends-on', count: 30 },
      { id: 'group:depends-on:team-b', owner: 'team-b', side: 'depends-on', count: 30 },
    ]);
    expect(folded).toBe(60);
    expect(out.nodes.some((n) => /^f\d/.test(n.id))).toBe(false);
    expect(byId(out, 'group:depends-on:team-a')).toMatchObject({ serviceName: 'team-a', version: '30 entities', status: 'group', group: 30, depth: 2, side: -1 });
    expect(byId(out, 'group:depends-on:team-a').parent).toBeUndefined();
    // The lane points out at each group, once, in the worst state among what it stands for.
    expect(byId(out, 'lane:depends-on').edges).toEqual([
      { targetId: 'group:depends-on:team-a', type: 'dependency', edgeState: 'drift', label: 'observed, not expected (1)' },
      { targetId: 'group:depends-on:team-b', type: 'dependency', edgeState: 'drift', label: 'observed, not expected (1)' },
    ]);
    expect(byId(out, 'n0').edges).toEqual([]);
  });

  it('points a used-by group into its lane, and says how many links when there is no state to show', () => {
    const nodes: N[] = [node('focus', 0), node('u', 1, 't', [{ targetId: 'focus' }])];
    for (let i = 0; i < GROUP_THRESHOLD; i++) nodes.push(node(`f${i}`, 2, i ? 'team-x' : 'team-y', i < 2 ? [{ targetId: 'u', type: 'runs' }] : [{ targetId: 'u' }]));
    const { graph: out, groups } = laneGraph({ nodes });
    expect(groups.map((g) => [g.id, g.count])).toEqual([['group:used-by:team-x', 39], ['group:used-by:*', 1]]);
    expect(byId(out, 'group:used-by:team-x').edges).toEqual([{ targetId: 'lane:used-by', type: 'dependency', label: '39 links' }]);
    // A group whose only link is a runs edge draws a runs edge.
    expect(byId(out, 'group:used-by:*')).toMatchObject({ serviceName: 'Other teams', version: '1 entity', edges: [{ targetId: 'lane:used-by', type: 'runs', label: '1 link' }] });
  });

  it('lets small teams, and teams past the per-side budget, share an Other teams group', () => {
    const owners = Array.from({ length: MAX_GROUPS_PER_SIDE + 3 }, (_, i) => `t${i}`);
    const g = graph(2, 60, owners);
    g.nodes.push(node('solo', 2, 'just-me'));
    byId(g, 'n0').edges!.push({ targetId: 'solo', edgeState: 'insufficient' });
    const { graph: out, groups } = laneGraph(g);
    const other = groups.find((x) => x.id === 'group:depends-on:*')!;
    expect(groups.filter((x) => x.id !== other.id)).toHaveLength(MAX_GROUPS_PER_SIDE);
    expect(other).toMatchObject({ owner: 'Other teams', side: 'depends-on' });
    expect(other.count).toBe(61 - groups.filter((x) => x !== other).reduce((t, x) => t + x.count, 0));
    expect(byId(out, 'group:depends-on:*').serviceName).toBe('Other teams');
    // Opening Other teams shows its entities.
    const opened = laneGraph(g, { open: new Set(['group:depends-on:*']) });
    expect(opened.groups.some((x) => x.id === 'group:depends-on:*')).toBe(false);
    expect(opened.graph.nodes.some((n) => n.id === 'solo')).toBe(true);
  });

  it('shows an opened group as its entities and keeps an edge between two drawn entities as it is', () => {
    const { graph: out, groups } = laneGraph(graph(4, 60), { open: new Set(['group:depends-on:team-a']) });
    expect(groups.map((x) => x.owner)).toEqual(['team-b']);
    expect(byId(out, 'n0').edges).toHaveLength(15);
    expect(byId(out, 'n0').edges![0]).toEqual({ targetId: 'f0', edgeState: 'drift' });
    expect(byId(out, 'f0')).toMatchObject({ side: -1, depth: 2 });
  });

  it('counts an edge between two groups into both, drops one inside a group and draws no edge for a group with no links', () => {
    const nodes: N[] = [node('focus', 0, undefined, [{ targetId: 'n' }]), node('n', 1, 'a', [{ targetId: 'a1' }])];
    for (let i = 0; i < 20; i++) nodes.push(node(`a${i}`, 2, 'team-a', [...(i < 19 ? [{ targetId: `a${i + 1}` }] : []), { targetId: `b${i}`, edgeState: 'expected-not-observed' }]));
    for (let i = 0; i < 20; i++) nodes.push(node(`b${i}`, 2, 'team-b'));
    nodes.push(node('c0', 2, 'team-c'), node('c1', 2, 'team-c')); // reached by nothing
    const { graph: out } = laneGraph({ nodes });
    const lane = byId(out, 'lane:depends-on').edges!;
    expect(lane.find((e) => e.targetId === 'group:depends-on:team-a')).toEqual({ targetId: 'group:depends-on:team-a', type: 'dependency', edgeState: 'expected-not-observed', label: 'expected, not observed (20)' });
    expect(lane.find((e) => e.targetId === 'group:depends-on:team-b')).toMatchObject({ label: 'expected, not observed (20)' });
    expect(byId(out, 'group:used-by:team-c').edges).toEqual([]);
    expect(byId(out, 'lane:used-by').serviceName).toBe('Used by (0)');
  });

  it('folds the direct neighbors by team too when they alone are past the threshold, attached to the focus', () => {
    const g = graph(GROUP_THRESHOLD + 5, 0, ['team-a', 'team-b', 'solo']);
    g.nodes[g.nodes.length - 1].owner = 'only-me'; // a direct neighbor alone in its team stays itself
    g.nodes.push(node('user1', 1, 'team-u', [{ targetId: 'focus', edgeState: 'matched' }]), node('user2', 1, 'team-u', [{ targetId: 'focus' }]));
    const { graph: out, groups } = laneGraph(g);
    expect(groups.map((x) => x.id)).toEqual(['group:depends-on:team-a', 'group:depends-on:team-b', 'group:depends-on:solo', 'group:used-by:team-u']);
    expect(out.nodes.map((n) => n.id)).toContain(`n${GROUP_THRESHOLD + 4}`);
    expect(byId(out, 'group:depends-on:team-a')).toMatchObject({ depth: 1, parent: 'lane:depends-on' });
    expect(byId(out, 'focus').edges).toEqual(expect.arrayContaining([
      { targetId: 'group:depends-on:team-a', type: 'dependency', label: '15 links' },
      { targetId: `n${GROUP_THRESHOLD + 4}` },
    ]));
    expect(byId(out, 'group:used-by:team-u').edges).toEqual([{ targetId: 'focus', type: 'dependency', edgeState: 'matched', label: 'matched (1)' }]);
  });

  it('shows a lane its first eight direct neighbors, the ones worth a look first, and holds the rest behind "+N more"', () => {
    const g = graph(12, 0);
    byId(g, 'focus').edges!.find((e) => e.targetId === 'n11')!.edgeState = 'drift';
    byId(g, 'n10').status = 'NonCompliant';
    const { graph: out, groups, folded, more } = laneGraph(g);
    expect([groups, folded]).toEqual([[], 0]);
    expect(more).toEqual([{ id: 'group:depends-on:+', owner: '+4 more', side: 'depends-on', count: 4 }]);
    const shown = out.nodes.filter((n) => n.parent === 'lane:depends-on').map((n) => n.id);
    expect(shown).toHaveLength(LANE_CAP + 1);
    expect(shown).toEqual(expect.arrayContaining(['n11', 'n10', 'group:depends-on:+']));
    expect(byId(out, 'group:depends-on:+')).toMatchObject({ serviceName: '+4 more', group: 4, depth: 1, side: -1 });
    // Its one edge has no words: the gap by the focus has no room for them.
    expect(byId(out, 'focus').edges!.find((e) => e.targetId === 'group:depends-on:+')).toEqual({ targetId: 'group:depends-on:+', type: 'dependency' });
    // Opened, the lane shows every direct neighbor; nine is not worth a "+1 more".
    expect(laneGraph(g, { open: new Set(['group:depends-on:+']) }).more).toBeUndefined();
    expect(laneGraph(graph(LANE_CAP + 1, 0)).more).toBeUndefined();
  });

  it('groups entities with no owner together', () => {
    expect(laneGraph(graph(2, 50, [''])).groups).toEqual([{ id: 'group:depends-on:No owner', owner: 'No owner', side: 'depends-on', count: 50 }]);
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
