/**
 * Pacto Dashboard API facade over the generated OpenAPI SDK (ADR-6).
 *
 * Huma/OpenAPI is the single source of truth for the wire contract. This file adds
 * ONLY ergonomics: named methods, ApiError translation, product schema-version
 * validation, and a discriminated entity-detail union. It never builds an
 * `/api/...` URL, serializes a query string, or redeclares a request/response DTO -
 * the generated client (see ./transport) owns all of that. Every request shape and
 * every response type is DERIVED from the generated `operations`/`paths`, so a wire
 * change flows into the facade automatically; the only handwritten shapes are the
 * deliberate ergonomic refinements (array-valued `kinds`/`views`, entity-detail
 * narrowing). Every backend call goes through this facade, and the facade goes
 * through the generated client, so live HTTP, the WASM demo and the static export
 * share one transport.
 */

import type { operations, components, paths } from './generated/schema';
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
// Product responses are inline operation payloads; alias them from the generated
// operations so the facade and UI consume the generated wire types.
type JSON200<T extends keyof operations> =
  operations[T]['responses'][200]['content']['application/json'];

export type ProductOverview = JSON200<'fleet-overview'>;
export type ProductEntityList = JSON200<'fleet-entities'>;
export type ProductEntityDetail = JSON200<'fleet-entity-detail'>;
export type ProductNeighborhood = JSON200<'fleet-neighborhood'>;
export type ProductAttentionList = JSON200<'fleet-attention'>;
export type ProductImpact = JSON200<'fleet-impact-post'>;
export type ProductMeta = ProductOverview['meta'];

// JSONBody / GetResponse / PostResponse derive an operation's 200 application/json
// body straight from the generated `paths`, keyed by the exact path a facade method
// calls. No dashboard backend operation is typed `unknown`: every method preserves
// its generated response type (requirement, item 3).
type JSONBody<R> = R extends { content: { 'application/json': infer T } } ? T : never;
type GetResponse<P extends keyof paths> =
  paths[P] extends { get: { responses: { 200: infer R } } } ? JSONBody<R> : never;
type PostResponse<P extends keyof paths> =
  paths[P] extends { post: { responses: { 200: infer R } } } ? JSONBody<R> : never;

// ── finite wire vocabularies (derived from generated enums, never hand-listed) ──
export type EntityKind = NonNullable<ProductEntityList['entities']>[number]['kind'];
export type SourceHealth = components['schemas']['Fleet.SourceState']['status'];
export type Direction = NonNullable<ProductNeighborhood['direction']>;
export type KnowledgeView = NonNullable<ProductNeighborhood['views']>[number];
/** Attention severity is the narrower domain (no "unknown"), derived from the wire. */
export type FindingSeverity = NonNullable<ProductAttentionList['items']>[number]['severity'];

// ── product request shapes (derived from the generated operations) ────────────
// The wire query/body types come straight from the generated operations, so adding,
// changing or removing an OPTIONAL wire request field flows into the facade type
// automatically. The only fields redeclared are the deliberate ergonomic
// refinements: `kinds`/`views` are arrays here but a comma-joined string on the wire
// (requirement, item 2). Every other field is inherited, never repeated by hand.
type FleetEntitiesQuery = NonNullable<operations['fleet-entities']['parameters']['query']>;
type FleetNeighborhoodQuery = NonNullable<operations['fleet-neighborhood']['parameters']['query']>;
type FleetAttentionQuery = NonNullable<operations['fleet-attention']['parameters']['query']>;
type FleetImpactBody = NonNullable<operations['fleet-impact-post']['requestBody']>['content']['application/json'];

export type FleetEntitiesInput = Omit<FleetEntitiesQuery, 'kinds'> & { kinds?: EntityKind[] };
export type FleetNeighborhoodInput = Omit<FleetNeighborhoodQuery, 'views'> & { views?: KnowledgeView[] };
export type FleetAttentionInput = FleetAttentionQuery;
export type FleetImpactInput = FleetImpactBody;

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

/**
 * ApiContractError signals that a well-formed HTTP response violated the product API
 * contract at runtime in a way the generated types cannot express - e.g. an
 * entity-detail whose kind and payload disagree. It is distinct from a version
 * mismatch (SchemaCompatibilityError) and a transport error (ApiError).
 */
export class ApiContractError extends Error {
  constructor(message: string) {
    super(`product API contract violation: ${message}`);
    this.name = 'ApiContractError';
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

// DETAIL_KINDS is the closed set of entity-detail payload discriminants; it doubles
// as the payload-field names (each kind's payload lives under the same key).
const DETAIL_KINDS = ['service', 'revision', 'target', 'owner', 'source'] as const;

/**
 * narrowEntityDetail validates the runtime invariant the generated broad shape
 * cannot enforce (requirement, item 4) and returns the discriminated union the UI
 * consumes: the entity exists, its kind is one of the supported kinds, EXACTLY ONE
 * detail payload is present, that payload corresponds to the kind, and no
 * contradictory payload is present. Any violation is a typed ApiContractError. Every
 * payload TYPE stays derived from the generated schema; nothing is redeclared.
 */
export function narrowEntityDetail(raw: ProductEntityDetail): NarrowedEntityDetail {
  const entity = raw.entity;
  if (!entity || !entity.kind) {
    throw new ApiContractError('entity-detail response carries no entity');
  }
  const kind = entity.kind;
  if (!(DETAIL_KINDS as readonly string[]).includes(kind)) {
    throw new ApiContractError(`entity-detail response has unsupported kind ${JSON.stringify(kind)}`);
  }
  const present = DETAIL_KINDS.filter((k) => raw[k] != null);
  if (present.length !== 1) {
    throw new ApiContractError(
      `entity-detail response must carry exactly one payload, found ${present.length}${present.length ? ` (${present.join(', ')})` : ''}`,
    );
  }
  if (present[0] !== kind) {
    throw new ApiContractError(`entity-detail response kind ${kind} does not match its payload ${present[0]}`);
  }
  return raw as NarrowedEntityDetail;
}

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
// Legacy endpoints preserve their GENERATED response types (never `unknown`); the
// legacy UI consuming them is untyped Svelte and is not part of this task, but the
// facade still carries the contract so a future typed consumer inherits it.

export const api = {
  health: (): Promise<GetResponse<'/health'>> => unwrap(client.GET('/health')),
  capabilities: (): Promise<GetResponse<'/api/capabilities'>> => unwrap(client.GET('/api/capabilities')),
  sources: (): Promise<GetResponse<'/api/sources'>> => unwrap(client.GET('/api/sources')),
  services: (): Promise<GetResponse<'/api/services'>> => unwrap(client.GET('/api/services')),
  service: (name: string): Promise<GetResponse<'/api/services/{name}'>> =>
    unwrap(client.GET('/api/services/{name}', { params: { path: { name } } })),
  serviceAtVersion: (name: string, version: string): Promise<GetResponse<'/api/services/{name}/versions/{version}'>> =>
    unwrap(client.GET('/api/services/{name}/versions/{version}', { params: { path: { name, version } } })),
  versions: (name: string): Promise<GetResponse<'/api/services/{name}/versions'>> =>
    unwrap(client.GET('/api/services/{name}/versions', { params: { path: { name } } })),
  serviceSources: (name: string): Promise<GetResponse<'/api/services/{name}/sources'>> =>
    unwrap(client.GET('/api/services/{name}/sources', { params: { path: { name } } })),
  dependents: (name: string): Promise<GetResponse<'/api/services/{name}/dependents'>> =>
    unwrap(client.GET('/api/services/{name}/dependents', { params: { path: { name } } })),
  crossRefs: (name: string): Promise<GetResponse<'/api/services/{name}/refs'>> =>
    unwrap(client.GET('/api/services/{name}/refs', { params: { path: { name } } })),
  graph: (): Promise<GetResponse<'/api/graph'>> => unwrap(client.GET('/api/graph')),
  serviceGraph: (name: string): Promise<GetResponse<'/api/services/{name}/graph'>> =>
    unwrap(client.GET('/api/services/{name}/graph', { params: { path: { name } } })),
  diff: (fromName: string, fromVersion: string, toName: string, toVersion: string): Promise<GetResponse<'/api/diff'>> =>
    unwrap(client.GET('/api/diff', {
      params: { query: { from_name: fromName, from_version: fromVersion || '', to_name: toName, to_version: toVersion || '' } },
    })),
  resolve: (ref: string, compatibility?: string): Promise<PostResponse<'/api/resolve'>> =>
    unwrap(client.POST('/api/resolve', { body: { ref, compatibility } })),
  remoteVersions: (ref: string, fetchAll?: boolean): Promise<PostResponse<'/api/versions'>> =>
    unwrap(client.POST('/api/versions', { body: { ref, fetch: fetchAll } })),
  refresh: (): Promise<PostResponse<'/api/refresh'>> => unwrap(client.POST('/api/refresh', {})),
  debugSources: (): Promise<GetResponse<'/api/debug/sources'>> => unwrap(client.GET('/api/debug/sources')),

  // ── Operational graph (fleet) legacy ──
  fleetSnapshot: (): Promise<GetResponse<'/api/fleet/snapshot'>> => unwrap(client.GET('/api/fleet/snapshot')),
  fleetServices: (
    params: {
      text?: string; owner?: string; scope?: string; status?: string;
      source?: string; limit?: number; offset?: number;
    } = {},
  ): Promise<GetResponse<'/api/fleet/services'>> => unwrap(client.GET('/api/fleet/services', { params: { query: params } })),
  fleetServiceGraph: (
    name: string,
    opts: { direction?: string; transitive?: boolean; maxDepth?: number } = {},
  ): Promise<GetResponse<'/api/fleet/services/{name}/graph'>> =>
    unwrap(client.GET('/api/fleet/services/{name}/graph', { params: { path: { name }, query: opts } })),
  fleetStatus: (): Promise<GetResponse<'/api/fleet/status'>> => unwrap(client.GET('/api/fleet/status')),
  fleetService: (key: string): Promise<GetResponse<'/api/fleet/service'>> =>
    unwrap(client.GET('/api/fleet/service', { params: { query: { key } } })),
  fleetTarget: (key: string): Promise<GetResponse<'/api/fleet/target'>> =>
    unwrap(client.GET('/api/fleet/target', { params: { query: { key } } })),
  fleetImpact: (oldRef: string, newRef: string, includeObserved?: boolean): Promise<GetResponse<'/api/fleet/impact'>> =>
    unwrap(client.GET('/api/fleet/impact', {
      params: { query: { old: oldRef, new: newRef, includeObserved: includeObserved ?? false } },
    })),

  // ── Product-oriented operational-graph APIs (requirement 2) ──
  fleetOverview: (): Promise<ProductOverview> =>
    productGet(client.GET('/api/fleet/overview')),
  fleetEntities: (params: FleetEntitiesInput = {}): Promise<ProductEntityList> => {
    const { kinds, ...rest } = params;
    return productGet(client.GET('/api/fleet/entities', {
      params: { query: { ...rest, kinds: kinds?.length ? kinds.join(',') : undefined } },
    }));
  },
  fleetEntityDetail: async (kind: EntityKind, key: string): Promise<NarrowedEntityDetail> => {
    const raw = await productGet(client.GET('/api/fleet/entities/{kind}', { params: { path: { kind }, query: { key } } }));
    return narrowEntityDetail(raw);
  },
  fleetNeighborhood: (params: FleetNeighborhoodInput): Promise<ProductNeighborhood> => {
    const { views, ...rest } = params;
    return productGet(client.GET('/api/fleet/neighborhood', {
      params: { query: { ...rest, views: views?.length ? views.join(',') : undefined } },
    }));
  },
  fleetAttention: (params: FleetAttentionInput = {}): Promise<ProductAttentionList> =>
    productGet(client.GET('/api/fleet/attention', {
      params: { query: { ...params, staleOnly: params.staleOnly || undefined } },
    })),
  fleetImpactByIdentity: (body: FleetImpactInput): Promise<ProductImpact> =>
    productGet(client.POST('/api/fleet/impact', { body })),
};
