/**
 * The fleet map's data: the server's whole-fleet answer (groups, ribbons and
 * tallies, laid out once) and the node pages of the groups the reader looks at.
 *
 * The Groups level needs no node: the answer alone draws it. From the
 * Services level on, the component asks for the groups that intersect the
 * viewport; their pages are fetched four groups at a time and kept in a small
 * LRU keyed by snapshot, grouping and group, so panning back is free and a new
 * snapshot never mixes with an old one.
 */
import { api, type FleetMapAnswer, type FleetMapNodesPage } from '../api';
import type { MapEdge, MapNode } from './lens';

/** Groups fetched at once. */
export const MAX_PARALLEL = 4;
/** Group pages kept. */
export const LRU_SIZE = 64;
/** Nodes asked for a request (the server's default page). */
export const PAGE_LIMIT = 1000;

export type GroupBy = 'domain' | 'team' | 'environment';

export interface Fetchers {
  map: (q: { groupBy?: string; environment?: string }) => Promise<FleetMapAnswer>;
  nodes: (q: { groupBy?: string; group: string; environment?: string; offset?: number; limit?: number }) => Promise<FleetMapNodesPage>;
}

/** Every node of one group read so far, in sunflower order, and the edges between them. */
export interface GroupPage {
  group: string;
  nodes: MapNode[];
  edges: MapEdge[];
  total: number;
  /** Every page of the group is in. */
  complete: boolean;
}

const defaultFetchers: Fetchers = { map: (q) => api.fleetMap(q), nodes: (q) => api.fleetMapNodes(q) };

export class FleetMapStore {
  answer = $state.raw<FleetMapAnswer | null>(null);
  error = $state<string | null>(null);
  loading = $state(false);
  /** Bumped whenever a group page arrives, so a reader can redraw. */
  revision = $state(0);
  /** The environment-grouped answer, read once per snapshot for the environment picker. */
  environments = $state.raw<FleetMapAnswer | null>(null);
  groupBy: GroupBy = 'domain';
  environment = '';

  private pages = new Map<string, GroupPage>();
  private queue: string[] = [];
  private inflight = new Set<string>();
  private failed = new Set<string>();
  private loadSeq = 0;

  constructor(private fetchers: Fetchers = defaultFetchers) {}

  get snapshotId(): string { return this.answer?.meta?.snapshotId ?? ''; }

  private key(group: string): string { return `${this.snapshotId}|${this.groupBy}|${group}`; }

  /** load reads the whole-fleet answer; a new snapshot or grouping drops every cached page. */
  async load(groupBy: GroupBy = this.groupBy, environment = this.environment): Promise<void> {
    const seq = ++this.loadSeq;
    this.loading = true;
    try {
      const a = await this.fetchers.map({ groupBy, environment: environment || undefined });
      if (seq !== this.loadSeq) return;
      if (a?.meta?.snapshotId !== this.snapshotId || groupBy !== this.groupBy || environment !== this.environment) this.clear();
      this.groupBy = groupBy;
      this.environment = environment;
      this.answer = a;
      this.error = null;
    } catch (e) {
      if (seq === this.loadSeq) this.error = e instanceof Error ? e.message : String(e);
    } finally {
      if (seq === this.loadSeq) this.loading = false;
    }
  }

  private envSeq = 0;

  /**
   * loadEnvironments reads the environments for the picker: the answer itself
   * when it is grouped by environment, otherwise that grouping read once per
   * snapshot. A failure leaves the picker with All environments only.
   */
  async loadEnvironments(): Promise<void> {
    if (this.groupBy === 'environment') { this.environments = this.answer; return; }
    if (this.envSeq || this.environments?.meta?.snapshotId === this.snapshotId) return;
    const seq = ++this.envSeq;
    try {
      const a = await this.fetchers.map({ groupBy: 'environment' });
      if (seq === this.envSeq) this.environments = a;
    } catch {
      // ponytail: no retry; the next snapshot asks again.
    } finally {
      if (seq === this.envSeq) this.envSeq = 0;
    }
  }

  private clear(): void {
    this.pages.clear();
    this.queue = [];
    this.failed.clear();
  }

  /** page is the group's nodes read so far, or undefined; reading it marks it recently used. */
  page(group: string): GroupPage | undefined {
    const k = this.key(group);
    const p = this.pages.get(k);
    if (p) { this.pages.delete(k); this.pages.set(k, p); }
    return p;
  }

  /**
   * request asks for the node pages of the groups in view. At the Groups level
   * it asks for nothing. Groups no longer in view leave the queue; a fetch
   * already running finishes.
   */
  request(lvl: number, groups: readonly string[]): void {
    if (lvl < 1 || !this.answer) return;
    this.queue = groups.filter((g) => {
      const k = this.key(g);
      if (this.pages.has(k)) { this.page(g); return false; }
      return !this.inflight.has(k) && !this.failed.has(k);
    });
    this.pump();
  }

  private pump(): void {
    while (this.inflight.size < MAX_PARALLEL && this.queue.length) {
      const group = this.queue.shift()!;
      const k = this.key(group);
      this.inflight.add(k);
      void this.fetchGroup(group, k).finally(() => { this.inflight.delete(k); this.pump(); });
    }
  }

  private async fetchGroup(group: string, k: string): Promise<void> {
    const page: GroupPage = { group, nodes: [], edges: [], total: 0, complete: false };
    const q = { groupBy: this.groupBy, group, environment: this.environment || undefined, limit: PAGE_LIMIT };
    try {
      for (let offset = 0; ; offset += PAGE_LIMIT) {
        const r = await this.fetchers.nodes({ ...q, offset });
        if (k !== this.key(group)) return; // the snapshot or grouping moved on
        page.nodes.push(...(r.nodes ?? []));
        page.edges.push(...(r.edges ?? []));
        page.total = r.total;
        // A group page shows as soon as its first part is in.
        this.store(k, page);
        if (!r.truncated || !r.nodes?.length) break;
      }
      page.complete = true;
      this.revision++;
    } catch {
      if (k === this.key(group)) this.failed.add(k);
    }
  }

  private store(k: string, page: GroupPage): void {
    this.pages.delete(k);
    this.pages.set(k, page);
    while (this.pages.size > LRU_SIZE) this.pages.delete(this.pages.keys().next().value!);
    this.revision++;
  }
}
