/**
 * Pure adapter: a fleet FleetSnapshot → the GraphData the Cytoscape renderer
 * consumes, for the Operational Graph's three perspectives (service | revision |
 * target) and its relationship layers (declared | observed | reconciled | all).
 *
 * Identity is domain-qualified end to end: every node id is a ServiceKey,
 * RevisionKey or TargetKey — never a bare display name — so two same-named
 * services in different domains stay distinct nodes with distinct edges.
 *
 * Honesty (matches the backend): the snapshot carries declared relationships AND,
 * when an observation source is configured (OTel traces folded in by fleet.Build),
 * domain-qualified observed relationships (provenance "observed"). layerAvailability
 * reports which layers actually have backing data so the view disables the empty
 * ones rather than shipping a placebo toggle. "reconciled" is DERIVED — a declared
 * edge whose target service actually has operational targets running — so it is real.
 * The target perspective NEVER fabricates instance-to-instance edges (a Cartesian
 * mesh the snapshot cannot substantiate): an instance links to the dependency
 * SERVICE it depends on, not to peer instances.
 */

import type { GraphData, GraphNode, GraphEdge } from './graph.ts';

export type Perspective = 'service' | 'revision' | 'target';
export type Layer = 'declared' | 'observed' | 'reconciled' | 'all';

export interface FleetRelationship {
  fromService: string;
  fromRevision?: string;
  toService?: string;
  resolvedRevision?: string;
  type: string;
  provenance?: string;
  required?: boolean;
  resolved?: boolean;
  compatibility?: string;
}
export interface FleetServiceRecord {
  key: string;
  name: string;
  domain?: string;
  status?: string;
  owner?: unknown;
  revisions?: string[];
  targets?: string[];
  sources?: string[];
}
export interface FleetRevisionRecord {
  key: string;
  serviceKey: string;
  service: string;
  domain?: string;
  version?: string;
  valid?: boolean;
}
export interface FleetTargetRecord {
  key: string;
  serviceKey: string;
  service: string;
  domain?: string;
  name: string;
  scope?: string;
  compliance?: string;
  contractRevision?: string;
  stale?: boolean;
}
export interface FleetSnapshot {
  services?: Record<string, FleetServiceRecord>;
  revisions?: Record<string, FleetRevisionRecord>;
  targets?: Record<string, FleetTargetRecord>;
  relationships?: FleetRelationship[];
}

/** Filters applied before building the graph. Empty string = no filter. */
export interface GraphFilters {
  domain?: string;
  scope?: string;
  owner?: string;
  status?: string;
  source?: string;
  freshness?: string; // '', 'fresh', 'stale'
}

export interface LayerAvailability {
  declared: boolean;
  observed: boolean;
  reconciled: boolean;
}

/** ownerKeyOf mirrors format.ownerKey without importing it (keeps this pure). */
function ownerKeyOf(owner: unknown): string {
  if (!owner || typeof owner !== 'object') return '';
  const o = owner as Record<string, unknown>;
  if (typeof o.team === 'string' && o.team) return o.team;
  if (typeof o.dri === 'string' && o.dri) return o.dri;
  return '';
}

/** A declared dependency edge that is "reconciled": resolved to a concrete
 * service that actually has operational targets running. */
function isReconciled(rel: FleetRelationship, snap: FleetSnapshot): boolean {
  if (!rel.resolved || !rel.toService) return false;
  const to = snap.services?.[rel.toService];
  return !!to && (to.targets?.length ?? 0) > 0;
}

/** layerAvailability reports which relationship layers have ANY backing data, so
 * the view disables the empty ones instead of offering a placebo control. */
export function layerAvailability(snap: FleetSnapshot | null | undefined): LayerAvailability {
  const rels = snap?.relationships ?? [];
  let declared = false;
  let observed = false;
  let reconciled = false;
  for (const r of rels) {
    if (r.type !== 'dependency') continue;
    if (r.provenance === 'observed') observed = true;
    else declared = true;
    if (isReconciled(r, snap!)) reconciled = true;
  }
  return { declared, observed, reconciled };
}

function serviceMatches(s: FleetServiceRecord, snap: FleetSnapshot, f: GraphFilters): boolean {
  if (f.domain && (s.domain || '') !== f.domain) return false;
  if (f.status && (s.status || '') !== f.status) return false;
  if (f.owner && ownerKeyOf(s.owner) !== f.owner) return false;
  if (f.source && !(s.sources || []).includes(f.source)) return false;
  if (f.scope || f.freshness) {
    // Scope/freshness are target-level predicates: keep the service if some target
    // of it matches.
    const targets = (s.targets || []).map((tk) => snap.targets?.[tk]).filter(Boolean) as FleetTargetRecord[];
    if (f.scope && !targets.some((t) => (t.scope || '') === f.scope)) return false;
    if (f.freshness === 'stale' && !targets.some((t) => t.stale)) return false;
    if (f.freshness === 'fresh' && !targets.some((t) => !t.stale)) return false;
  }
  return true;
}

/** relationshipInLayer decides whether an edge belongs in the selected layer. */
function relationshipInLayer(rel: FleetRelationship, snap: FleetSnapshot, layer: Layer): boolean {
  if (rel.type !== 'dependency') return false;
  const observed = rel.provenance === 'observed';
  switch (layer) {
    case 'declared':
      return !observed;
    case 'observed':
      return observed;
    case 'reconciled':
      return isReconciled(rel, snap);
    case 'all':
      return true;
  }
}

/** buildFleetGraph converts a snapshot into renderer GraphData for one
 * perspective and layer, honoring the filters. */
export function buildFleetGraph(
  snap: FleetSnapshot | null | undefined,
  perspective: Perspective,
  layer: Layer,
  filters: GraphFilters = {},
): GraphData {
  if (!snap) return { nodes: [] };
  if (perspective === 'revision') return revisionGraph(snap, layer, filters);
  if (perspective === 'target') return targetGraph(snap, layer, filters);
  return serviceGraph(snap, layer, filters);
}

function serviceGraph(snap: FleetSnapshot, layer: Layer, f: GraphFilters): GraphData {
  const services = snap.services || {};
  const keep = new Set<string>();
  for (const [key, s] of Object.entries(services)) {
    if (serviceMatches(s, snap, f)) keep.add(key);
  }
  const nodes: GraphNode[] = [];
  const edgesByFrom = new Map<string, GraphEdge[]>();
  for (const rel of snap.relationships || []) {
    if (!relationshipInLayer(rel, snap, layer)) continue;
    if (!keep.has(rel.fromService) || !rel.toService || !keep.has(rel.toService)) continue;
    const list = edgesByFrom.get(rel.fromService) || [];
    list.push({ targetId: rel.toService, required: rel.required, type: rel.type });
    edgesByFrom.set(rel.fromService, list);
  }
  for (const key of keep) {
    const s = services[key];
    nodes.push({
      id: key,
      serviceName: s.name,
      status: s.status || 'Unknown',
      kind: 'service',
      edges: edgesByFrom.get(key) || [],
    });
  }
  return { nodes };
}

function revisionGraph(snap: FleetSnapshot, layer: Layer, f: GraphFilters): GraphData {
  const services = snap.services || {};
  const revisions = snap.revisions || {};
  const keepService = new Set<string>();
  for (const [key, s] of Object.entries(services)) {
    if (serviceMatches(s, snap, f)) keepService.add(key);
  }
  const keepRev = new Set<string>();
  for (const [rk, rev] of Object.entries(revisions)) {
    if (keepService.has(rev.serviceKey)) keepRev.add(rk);
  }
  const edgesByFrom = new Map<string, GraphEdge[]>();
  for (const rel of snap.relationships || []) {
    if (!relationshipInLayer(rel, snap, layer)) continue;
    if (!rel.fromRevision || !rel.resolvedRevision) continue;
    if (!keepRev.has(rel.fromRevision) || !keepRev.has(rel.resolvedRevision)) continue;
    const list = edgesByFrom.get(rel.fromRevision) || [];
    list.push({ targetId: rel.resolvedRevision, required: rel.required, type: rel.type });
    edgesByFrom.set(rel.fromRevision, list);
  }
  const nodes: GraphNode[] = [];
  for (const rk of keepRev) {
    const rev = revisions[rk];
    nodes.push({
      id: rk,
      serviceName: rev.service,
      status: rev.valid === false ? 'NonCompliant' : (services[rev.serviceKey]?.status || 'Unknown'),
      version: rev.version || '',
      kind: 'revision',
      edges: edgesByFrom.get(rk) || [],
    });
  }
  return { nodes };
}

/** Prefix for a dependency-SERVICE aggregate node in the target perspective, kept
 * distinct from any TargetKey so ids never collide. */
export const TARGET_DEP_SERVICE_PREFIX = 'depsvc::';

function targetGraph(snap: FleetSnapshot, layer: Layer, f: GraphFilters): GraphData {
  const services = snap.services || {};
  const targets = snap.targets || {};
  const keepService = new Set<string>();
  for (const [key, s] of Object.entries(services)) {
    if (serviceMatches(s, snap, f)) keepService.add(key);
  }
  // Index the instances of each kept service.
  const instancesOf = new Map<string, string[]>();
  for (const [tk, t] of Object.entries(targets)) {
    if (!keepService.has(t.serviceKey)) continue;
    const list = instancesOf.get(t.serviceKey) || [];
    list.push(tk);
    instancesOf.set(t.serviceKey, list);
  }
  // Dependency edges in the target perspective link a deployed instance to the
  // SERVICE its service depends on — NOT to each peer instance of that service.
  // Drawing instance→instance for every (fromTarget, toTarget) pair would assert a
  // full-mesh runtime routing that the snapshot never observed (OTel service.name
  // resolves to a service, not an instance). That Cartesian fan-out is the lie this
  // view must not tell. So the edge points at a single dependency-service aggregate
  // node (N×1), honest as "this instance depends on service B". Layers apply: only
  // dependencies in the selected layer are drawn.
  const edgesByFrom = new Map<string, GraphEdge[]>();
  const depServiceNodes = new Map<string, string>(); // aggregate node id -> serviceKey
  for (const rel of snap.relationships || []) {
    if (!relationshipInLayer(rel, snap, layer)) continue;
    if (!rel.resolved || !rel.toService || !keepService.has(rel.fromService)) continue;
    const fromTargets = instancesOf.get(rel.fromService) || [];
    if (fromTargets.length === 0) continue;
    const aggId = TARGET_DEP_SERVICE_PREFIX + rel.toService;
    depServiceNodes.set(aggId, rel.toService);
    for (const ft of fromTargets) {
      const list = edgesByFrom.get(ft) || [];
      list.push({ targetId: aggId, required: rel.required, type: rel.type });
      edgesByFrom.set(ft, list);
    }
  }
  const nodes: GraphNode[] = [];
  for (const [sk, tks] of instancesOf) {
    void sk;
    for (const tk of tks) {
      const t = targets[tk];
      nodes.push({
        id: tk,
        serviceName: t.name,
        status: t.compliance || 'Unknown',
        version: t.scope || '',
        reason: t.stale ? 'not_found' : undefined,
        kind: 'target',
        edges: edgesByFrom.get(tk) || [],
      });
    }
  }
  // The dependency-service aggregate nodes an instance points at (logical services,
  // rendered distinctly from instances). They carry no outgoing edges here.
  for (const [aggId, sk] of depServiceNodes) {
    const s = services[sk];
    nodes.push({ id: aggId, serviceName: s?.name || sk, status: s?.status || 'Unknown', kind: 'service', edges: [] });
  }
  return { nodes };
}

/** distinctValues collects the sorted distinct filter option values present in
 * the snapshot, so the view populates its filter selectors from real data. */
export function distinctValues(snap: FleetSnapshot | null | undefined): {
  domains: string[];
  scopes: string[];
  owners: string[];
  statuses: string[];
  sources: string[];
} {
  const domains = new Set<string>();
  const owners = new Set<string>();
  const statuses = new Set<string>();
  const sources = new Set<string>();
  const scopes = new Set<string>();
  for (const s of Object.values(snap?.services || {})) {
    if (s.domain) domains.add(s.domain);
    const ok = ownerKeyOf(s.owner);
    if (ok) owners.add(ok);
    if (s.status) statuses.add(s.status);
    for (const src of s.sources || []) sources.add(src);
  }
  for (const t of Object.values(snap?.targets || {})) {
    if (t.scope) scopes.add(t.scope);
  }
  const sorted = (set: Set<string>) => Array.from(set).sort();
  return {
    domains: sorted(domains),
    scopes: sorted(scopes),
    owners: sorted(owners),
    statuses: sorted(statuses),
    sources: sorted(sources),
  };
}
