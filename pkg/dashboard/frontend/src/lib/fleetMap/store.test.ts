import { describe, it, expect, vi } from 'vitest';
import { FleetMapStore, LRU_SIZE, MAX_PARALLEL, type Fetchers } from './store.svelte';
import { mapAnswer, nodesPage } from './testkit';
import type { FleetMapNodesPage } from '../api';

const tick = () => new Promise((r) => setTimeout(r, 0));

function fakes(snapshot = { id: 'snap-1' }) {
  const resolvers: { group: string; resolve: (p: FleetMapNodesPage) => void }[] = [];
  const fetchers: Fetchers = {
    map: vi.fn(async () => mapAnswer(snapshot.id)),
    nodes: vi.fn((q) => new Promise<FleetMapNodesPage>((resolve) => { resolvers.push({ group: q.group, resolve }); })),
  };
  return { fetchers, resolvers };
}

describe('fleet map store', () => {
  it('groups level requests no node page', async () => {
    const { fetchers } = fakes();
    const s = new FleetMapStore(fetchers);
    await s.load();
    s.request(0, ['payments', 'checkout']);
    expect(fetchers.nodes).not.toHaveBeenCalled();
    expect(fetchers.map).toHaveBeenCalledWith({ groupBy: 'domain' });
  });

  it('services level requests pages only for groups in view, four at once', async () => {
    const { fetchers, resolvers } = fakes();
    const s = new FleetMapStore(fetchers);
    await s.load();
    const inView = ['a', 'b', 'c', 'd', 'e', 'f'];
    s.request(1, inView);
    expect(fetchers.nodes).toHaveBeenCalledTimes(MAX_PARALLEL);
    expect(resolvers.map((r) => r.group)).toEqual(['a', 'b', 'c', 'd']);
    // A group out of view leaves the queue; the ones in view go next.
    s.request(1, ['a', 'b', 'c', 'd', 'f']);
    resolvers[0].resolve(nodesPage('a', [{}]));
    await tick();
    expect(resolvers.map((r) => r.group)).toEqual(['a', 'b', 'c', 'd', 'f']);
    expect(s.page('a')?.nodes).toHaveLength(1);
    expect(s.page('e')).toBeUndefined();
    // A cached group is never asked for again.
    s.request(1, ['a']);
    expect(fetchers.nodes).toHaveBeenCalledTimes(5);
  });

  it('reads every page of a large group', async () => {
    const fetchers: Fetchers = {
      map: async () => mapAnswer(),
      nodes: vi.fn(async (q) => nodesPage(q.group, [{}, {}], { offset: q.offset ?? 0, total: 4, truncated: (q.offset ?? 0) === 0 })),
    };
    const s = new FleetMapStore(fetchers);
    await s.load();
    s.request(1, ['big']);
    await tick(); await tick();
    expect(fetchers.nodes).toHaveBeenCalledTimes(2);
    expect(s.page('big')).toMatchObject({ total: 4, complete: true });
    expect(s.page('big')?.nodes).toHaveLength(4);
  });

  it('LRU evicts the least recent group page', async () => {
    const fetchers: Fetchers = { map: async () => mapAnswer(), nodes: async (q) => nodesPage(q.group, [{}]) };
    const s = new FleetMapStore(fetchers);
    await s.load();
    for (let i = 0; i <= LRU_SIZE; i++) {
      s.request(1, [`g${i}`]);
      await tick();
      if (i === 1) s.page('g0'); // g0 used again: g1 is now the least recent
    }
    expect(s.page('g0')).toBeDefined();
    expect(s.page('g1')).toBeUndefined();
    expect(s.page(`g${LRU_SIZE}`)).toBeDefined();
  });

  it('snapshot change clears the cache', async () => {
    const snap = { id: 'snap-1' };
    const fetchers: Fetchers = { map: async () => mapAnswer(snap.id), nodes: async (q) => nodesPage(q.group, [{}]) };
    const s = new FleetMapStore(fetchers);
    await s.load();
    s.request(1, ['payments']);
    await tick();
    expect(s.page('payments')).toBeDefined();
    await s.load();
    expect(s.page('payments')).toBeDefined();
    snap.id = 'snap-2';
    await s.load();
    expect(s.snapshotId).toBe('snap-2');
    expect(s.page('payments')).toBeUndefined();
  });

  it('a failed load says why and keeps nothing stale', async () => {
    const s = new FleetMapStore({ map: async () => { throw new Error('boom'); }, nodes: async () => nodesPage('x', []) });
    await s.load();
    expect(s.error).toBe('boom');
    expect(s.loading).toBe(false);
    s.request(1, ['x']);
    expect(s.page('x')).toBeUndefined();
  });

  it('reads the environments for the picker once per snapshot', async () => {
    const snapshot = { id: 'snap-1' };
    const { fetchers } = fakes(snapshot);
    const s = new FleetMapStore(fetchers);
    await s.load();
    await Promise.all([s.loadEnvironments(), s.loadEnvironments()]);
    await s.loadEnvironments();
    expect(fetchers.map).toHaveBeenCalledTimes(2);
    expect(fetchers.map).toHaveBeenLastCalledWith({ groupBy: 'environment' });
    snapshot.id = 'snap-2';
    await s.load();
    await s.loadEnvironments();
    expect(fetchers.map).toHaveBeenCalledTimes(4);
    // Grouped by environment, the answer itself lists them.
    await s.load('environment');
    await s.loadEnvironments();
    expect(fetchers.map).toHaveBeenCalledTimes(5);
    expect(s.environments).toBe(s.answer);
  });

  it('an environment switch never stores the page read for the previous environment', async () => {
    const { fetchers, resolvers } = fakes();
    const s = new FleetMapStore(fetchers);
    await s.load('domain', '');
    s.request(1, ['payments']);
    await s.load('domain', 'prod');
    // The new environment reads no new answer, and asks its own page.
    expect(fetchers.map).toHaveBeenCalledTimes(1);
    s.request(1, ['payments']);
    expect(resolvers.map((r) => r.group)).toEqual(['payments', 'payments']);
    expect(fetchers.nodes).toHaveBeenLastCalledWith(expect.objectContaining({ environment: 'prod' }));
    resolvers[0].resolve(nodesPage('payments', [{ inEnvironment: undefined }]));
    await tick();
    expect(s.page('payments')).toBeUndefined();
    resolvers[1].resolve(nodesPage('payments', [{ inEnvironment: false }]));
    await tick();
    expect(s.page('payments')?.nodes[0].inEnvironment).toBe(false);
  });

  it('the environment grouping asks pages in no environment', async () => {
    const { fetchers } = fakes();
    const s = new FleetMapStore(fetchers);
    await s.load('environment', 'prod');
    s.request(1, ['prod']);
    expect(fetchers.nodes).toHaveBeenCalledWith(expect.objectContaining({ groupBy: 'environment', environment: undefined }));
  });

  it('a page from a newer snapshot is dropped and the answer read again', async () => {
    const snapshot = { id: 'snap-1' };
    const fetchers: Fetchers = {
      map: vi.fn(async () => mapAnswer(snapshot.id)),
      nodes: vi.fn(async (q) => nodesPage(q.group, [{}], { meta: { ...mapAnswer('snap-2').meta } })),
    };
    const s = new FleetMapStore(fetchers);
    await s.load();
    snapshot.id = 'snap-2';
    s.request(1, ['payments']);
    await tick(); await tick();
    expect(fetchers.map).toHaveBeenCalledTimes(2);
    expect(s.snapshotId).toBe('snap-2');
    expect(s.page('payments')).toBeUndefined();
  });

  it('a group whose later page fails keeps no partial page', async () => {
    const fetchers: Fetchers = {
      map: async () => mapAnswer(),
      nodes: vi.fn(async (q) => {
        if ((q.offset ?? 0) > 0) throw new Error('502');
        return nodesPage(q.group, [{}, {}], { total: 4, truncated: true });
      }),
    };
    const s = new FleetMapStore(fetchers);
    await s.load();
    s.request(1, ['big']);
    await tick(); await tick();
    expect(s.page('big')).toBeUndefined();
  });
});
