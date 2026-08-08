/**
 * Pacto Dashboard API facade over the generated OpenAPI SDK (ADR-6).
 *
 * Huma/OpenAPI is the single source of truth for the wire contract. This file adds
 * ONLY ergonomics: named methods, ApiError translation, product schema-version
 * validation, and a discriminated entity-detail union. It never builds an
 * `/api/...` URL, serializes a query string, or redeclares a request/response DTO -
 * the generated client (see ./transport) owns all of that. Every backend call in
 * the dashboard goes through this facade, and the facade goes through the generated
 * client, so live HTTP, the WASM demo and the static export share one transport.
 */

import type { operations, components } from './generated/schema';
import { client } from './transport';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = 'ApiError';
  }
}

// ── generated response types (never redeclared) ──────────────────────────────
// The product responses are inline operation payloads; alias them from the
// generated operations so the facade and UI consume the generated wire types.
type JSON200<T extends keyof operations> =
  operations[T]['responses'][200]['content']['application/json'];

export type ProductOverview = JSON200<'fleet-overview'>;
export type ProductEntityList = JSON200<'fleet-entities'>;
export type ProductEntityDetail = JSON200<'fleet-entity-detail'>;
export type ProductNeighborhood = JSON200<'fleet-neighborhood'>;
export type ProductAttentionList = JSON200<'fleet-attention'>;
export type ProductImpact = JSON200<'fleet-impact-post'>;
export type ProductMeta = ProductOverview['meta'];

// ── finite wire vocabularies (derived from generated enums, never hand-listed) ──
export type EntityKind = NonNullable<ProductEntityList['entities']>[number]['kind'];
export type SourceHealth = components['schemas']['Fleet.SourceState']['status'];
export type Direction = NonNullable<ProductNeighborhood['direction']>;
export type KnowledgeView = NonNullable<ProductNeighborhood['views']>[number];
/** Attention severity is the narrower domain (no "unknown"), derived from the wire. */
export type FindingSeverity = NonNullable<ProductAttentionList['items']>[number]['severity'];

// ── product schema-version compatibility ─────────────────────────────────────
export const PRODUCT_SCHEMA_VERSION = 'pacto.dev/fleet-product/v1';

export class SchemaCompatibilityError extends Error {
  expected: string;
  actual: string;
  constructor(actual: string) {
    super(
      `unsupported product schema version ${actual || '(none)'}: this dashboard expects ${PRODUCT_SCHEMA_VERSION}; reload the page or upgrade the dashboard`,
    );
    this.name = 'SchemaCompatibilityError';
    this.expected = PRODUCT_SCHEMA_VERSION;
    this.actual = actual;
  }
}

/** checkProductSchema throws SchemaCompatibilityError unless meta is compatible. */
export function checkProductSchema(meta: { schemaVersion?: string } | undefined): void {
  const v = meta?.schemaVersion ?? '';
  if (v !== PRODUCT_SCHEMA_VERSION) {
    throw new SchemaCompatibilityError(v);
  }
}

// ── entity-detail discriminated union (derived from the generated shape) ──────
// The generated ProductEntityDetail is a single object with all five payloads
// optional (Huma cannot express a nested-discriminator oneOf). These variant types
// REFER to the generated payload types (they never redeclare the payload schema)
// and add `?: never` exclusivity plus a kind-narrowed entity, so an object carrying
// zero payloads or more than one does not type-check. The type guards narrow from
// entity.kind, which TypeScript cannot do through the nested discriminant alone.
type Detail = ProductEntityDetail;
type DetailEntity = NonNullable<Detail['entity']>;

export type ServiceEntityDetail = Detail & {
  entity: DetailEntity & { kind: 'service' };
  service: NonNullable<Detail['service']>;
  revision?: never;
  target?: never;
  owner?: never;
  source?: never;
};
export type RevisionEntityDetail = Detail & {
  entity: DetailEntity & { kind: 'revision' };
  revision: NonNullable<Detail['revision']>;
  service?: never;
  target?: never;
  owner?: never;
  source?: never;
};
export type TargetEntityDetail = Detail & {
  entity: DetailEntity & { kind: 'target' };
  target: NonNullable<Detail['target']>;
  service?: never;
  revision?: never;
  owner?: never;
  source?: never;
};
export type OwnerEntityDetail = Detail & {
  entity: DetailEntity & { kind: 'owner' };
  owner: NonNullable<Detail['owner']>;
  service?: never;
  revision?: never;
  target?: never;
  source?: never;
};
export type SourceEntityDetail = Detail & {
  entity: DetailEntity & { kind: 'source' };
  source: NonNullable<Detail['source']>;
  service?: never;
  revision?: never;
  target?: never;
  owner?: never;
};

/** NarrowedEntityDetail is the discriminated view of a ProductEntityDetail. */
export type NarrowedEntityDetail =
  | ServiceEntityDetail
  | RevisionEntityDetail
  | TargetEntityDetail
  | OwnerEntityDetail
  | SourceEntityDetail;

export const isServiceDetail = (d: ProductEntityDetail): d is ServiceEntityDetail =>
  d.entity?.kind === 'service';
export const isRevisionDetail = (d: ProductEntityDetail): d is RevisionEntityDetail =>
  d.entity?.kind === 'revision';
export const isTargetDetail = (d: ProductEntityDetail): d is TargetEntityDetail =>
  d.entity?.kind === 'target';
export const isOwnerDetail = (d: ProductEntityDetail): d is OwnerEntityDetail =>
  d.entity?.kind === 'owner';
export const isSourceDetail = (d: ProductEntityDetail): d is SourceEntityDetail =>
  d.entity?.kind === 'source';

// ── transport helpers ────────────────────────────────────────────────────────

interface FetchResult<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

function errorMessage(error: unknown, response: Response): string {
  if (error && typeof error === 'object') {
    const e = error as { detail?: string; title?: string };
    if (e.detail) return e.detail;
    if (e.title) return e.title;
  }
  if (typeof error === 'string' && error) return error;
  return response.statusText || `HTTP ${response.status}`;
}

/** unwrap turns a generated-client result into data, or throws a typed ApiError. */
async function unwrap<T>(p: Promise<FetchResult<T>>): Promise<T> {
  const { data, error, response } = await p;
  if (!response.ok || error !== undefined) {
    throw new ApiError(response.status, errorMessage(error, response));
  }
  return (data ?? null) as T;
}

/** productGet/productPost validate the product schema version at the boundary. */
async function productGet<T extends { meta?: ProductMeta }>(p: Promise<FetchResult<T>>): Promise<T> {
  const res = await unwrap<T>(p);
  checkProductSchema(res?.meta);
  return res;
}

// ── the facade ───────────────────────────────────────────────────────────────
// Legacy endpoints keep an `unknown` return (as before) so existing views are
// unchanged; the product endpoints return their generated types.

export const api = {
  health: (): Promise<unknown> => unwrap(client.GET('/health')),
  capabilities: (): Promise<unknown> => unwrap(client.GET('/api/capabilities')),
  sources: (): Promise<unknown> => unwrap(client.GET('/api/sources')),
  services: (): Promise<unknown> => unwrap(client.GET('/api/services')),
  service: (name: string): Promise<unknown> =>
    unwrap(client.GET('/api/services/{name}', { params: { path: { name } } })),
  serviceAtVersion: (name: string, version: string): Promise<unknown> =>
    unwrap(client.GET('/api/services/{name}/versions/{version}', { params: { path: { name, version } } })),
  versions: (name: string): Promise<unknown> =>
    unwrap(client.GET('/api/services/{name}/versions', { params: { path: { name } } })),
  serviceSources: (name: string): Promise<unknown> =>
    unwrap(client.GET('/api/services/{name}/sources', { params: { path: { name } } })),
  dependents: (name: string): Promise<unknown> =>
    unwrap(client.GET('/api/services/{name}/dependents', { params: { path: { name } } })),
  crossRefs: (name: string): Promise<unknown> =>
    unwrap(client.GET('/api/services/{name}/refs', { params: { path: { name } } })),
  graph: (): Promise<unknown> => unwrap(client.GET('/api/graph')),
  serviceGraph: (name: string): Promise<unknown> =>
    unwrap(client.GET('/api/services/{name}/graph', { params: { path: { name } } })),
  diff: (fromName: string, fromVersion: string, toName: string, toVersion: string): Promise<unknown> =>
    unwrap(client.GET('/api/diff', {
      params: { query: { from_name: fromName, from_version: fromVersion || '', to_name: toName, to_version: toVersion || '' } },
    })),
  resolve: (ref: string, compatibility?: string): Promise<unknown> =>
    unwrap(client.POST('/api/resolve', { body: { ref, compatibility } })),
  remoteVersions: (ref: string, fetchAll?: boolean): Promise<unknown> =>
    unwrap(client.POST('/api/versions', { body: { ref, fetch: fetchAll } })),
  refresh: (): Promise<unknown> => unwrap(client.POST('/api/refresh', {})),
  debugSources: (): Promise<unknown> => unwrap(client.GET('/api/debug/sources')),

  // ── Operational graph (fleet) legacy ──
  fleetSnapshot: (): Promise<unknown> => unwrap(client.GET('/api/fleet/snapshot')),
  fleetServices: (
    params: {
      text?: string; owner?: string; scope?: string; status?: string;
      source?: string; limit?: number; offset?: number;
    } = {},
  ): Promise<unknown> => unwrap(client.GET('/api/fleet/services', { params: { query: params } })),
  fleetServiceGraph: (
    name: string,
    opts: { direction?: string; transitive?: boolean; maxDepth?: number } = {},
  ): Promise<unknown> =>
    unwrap(client.GET('/api/fleet/services/{name}/graph', { params: { path: { name }, query: opts } })),
  fleetStatus: (): Promise<unknown> => unwrap(client.GET('/api/fleet/status')),
  fleetService: (key: string): Promise<unknown> =>
    unwrap(client.GET('/api/fleet/service', { params: { query: { key } } })),
  fleetTarget: (key: string): Promise<unknown> =>
    unwrap(client.GET('/api/fleet/target', { params: { query: { key } } })),
  fleetImpact: (oldRef: string, newRef: string, includeObserved?: boolean): Promise<unknown> =>
    unwrap(client.GET('/api/fleet/impact', {
      params: { query: { old: oldRef, new: newRef, includeObserved: includeObserved ?? false } },
    })),

  // ── Product-oriented operational-graph APIs (requirement 2) ──
  fleetOverview: (): Promise<ProductOverview> =>
    productGet(client.GET('/api/fleet/overview')),
  fleetEntities: (
    params: {
      text?: string; kinds?: EntityKind[]; owner?: string; domain?: string;
      scope?: string; status?: string; sourceHealth?: SourceHealth; source?: string;
      limit?: number; offset?: number;
    } = {},
  ): Promise<ProductEntityList> =>
    productGet(client.GET('/api/fleet/entities', {
      params: {
        query: {
          text: params.text, kinds: params.kinds?.length ? params.kinds.join(',') : undefined,
          owner: params.owner, domain: params.domain, scope: params.scope, status: params.status,
          sourceHealth: params.sourceHealth, source: params.source, limit: params.limit, offset: params.offset,
        },
      },
    })),
  fleetEntityDetail: (kind: EntityKind, key: string): Promise<ProductEntityDetail> =>
    productGet(client.GET('/api/fleet/entities/{kind}', { params: { path: { kind }, query: { key } } })),
  fleetNeighborhood: (
    params: {
      kind: EntityKind; key: string; direction?: Direction; depth?: number;
      views?: KnowledgeView[]; maxNodes?: number; maxEdges?: number;
    },
  ): Promise<ProductNeighborhood> =>
    productGet(client.GET('/api/fleet/neighborhood', {
      params: {
        query: {
          kind: params.kind, key: params.key, direction: params.direction, depth: params.depth,
          views: params.views?.length ? params.views.join(',') : undefined,
          maxNodes: params.maxNodes, maxEdges: params.maxEdges,
        },
      },
    })),
  fleetAttention: (
    params: {
      category?: string; kind?: EntityKind; key?: string; service?: string; owner?: string;
      source?: string; severity?: FindingSeverity; status?: string; staleOnly?: boolean;
      limit?: number; offset?: number;
    } = {},
  ): Promise<ProductAttentionList> =>
    productGet(client.GET('/api/fleet/attention', {
      params: {
        query: {
          category: params.category, kind: params.kind, key: params.key, service: params.service,
          owner: params.owner, source: params.source, severity: params.severity, status: params.status,
          staleOnly: params.staleOnly || undefined, limit: params.limit, offset: params.offset,
        },
      },
    })),
  fleetImpactByIdentity: (body: {
    snapshotId?: string; serviceKey?: string; fromRevisionKey: string;
    toRevisionKey: string; includeObserved?: boolean; limit?: number; offset?: number;
  }): Promise<ProductImpact> => productGet(client.POST('/api/fleet/impact', { body })),
};
