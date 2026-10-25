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
import { client, dashboardFetch } from './transport';

export class ApiError extends Error {
  status: number;
  /** The stable refusal code a delivery answer carries (FreshLoginRequired,
   *  NotYourTeam and so on); empty for any other error. */
  code: string;
  /** The fresh plan a PlanStale refusal carries. */
  plan?: DeliveryPlan;
  constructor(status: number, message: string, code = '', plan?: DeliveryPlan) {
    super(message);
    this.status = status;
    this.name = 'ApiError';
    this.code = code;
    this.plan = plan;
  }
}

// ── generated response types (never redeclared) ──────────────────────────────
// Product responses are inline operation payloads; alias them from the generated
// operations so the facade and UI consume the generated wire types.
// Only operations that answer 200: a create answers 201 and has no 200 entry.
type OpsWith200 = {
  [K in keyof operations]: operations[K]['responses'] extends { 200: unknown } ? K : never;
}[keyof operations];
type JSON200<T extends OpsWith200> =
  operations[T]['responses'][200]['content']['application/json'];

export type ProductOverview = JSON200<'fleet-overview'>;
export type ProductEntityList = JSON200<'fleet-entities'>;
export type ProductEntityDetail = JSON200<'fleet-entity-detail'>;
export type ProductNeighborhood = JSON200<'fleet-neighborhood'>;
export type ProductAttentionList = JSON200<'fleet-attention'>;
export type ProductImpact = JSON200<'fleet-impact-post'>;
export type ProductRevisionDocument = JSON200<'fleet-revision-document'>;
export type ProductMeta = ProductOverview['meta'];

// JSONBody / GetResponse / PostResponse derive an operation's 200 application/json
// body straight from the generated `paths`, keyed by the exact path a facade method
// calls. No dashboard backend operation is typed `unknown`: every method preserves
// its generated response type.
type JSONBody<R> = R extends { content: { 'application/json': infer T } } ? T : never;
type GetResponse<P extends keyof paths> =
  paths[P] extends { get: { responses: { 200: infer R } } } ? JSONBody<R> : never;
type PostResponse<P extends keyof paths> =
  paths[P] extends { post: { responses: { 200: infer R } } } ? JSONBody<R> : never;
type CreatedResponse<P extends keyof paths> =
  paths[P] extends { post: { responses: { 201: infer R } } } ? JSONBody<R> : never;
type DeleteResponse<P extends keyof paths> =
  paths[P] extends { delete: { responses: { 200: infer R } } } ? JSONBody<R> : never;

// ── delivery wire types ──────────────────────────────────────────────────────
export type DeliverySession = GetResponse<'/api/session'>;
export type DeliveryInbox = GetResponse<'/api/delivery/inbox'>;
export type DeliveryEnvironments = GetResponse<'/api/delivery/environments'>;
export type DeliveryPublished = GetResponse<'/api/delivery/published'>;
export type DeliveryHistory = GetResponse<'/api/delivery/history'>;
export type DeliveryOperation = GetResponse<'/api/delivery/operations/{namespace}/{name}'>;
export type DeliveryBlame = GetResponse<'/api/delivery/blame'>;
export type DeliveryUndo = GetResponse<'/api/delivery/undo'>;
export type DeliveryRefusals = GetResponse<'/api/delivery/refusals'>;
export type DeliveryPlan = components['schemas']['Delivery.Plan'];
export type DeliveryRequest = components['schemas']['Delivery.Request'];
export type DeliveryDecision = components['schemas']['DecideInputBody']['decision'];
export type DeliveryVerdict = components['schemas']['Delivery.ExplainedVerdict'];
export type InboxItem = components['schemas']['InboxItem'];
export type EnvironmentRow = components['schemas']['EnvironmentRow'];

// ── finite wire vocabularies (derived from generated enums, never hand-listed) ──
/** One canonical entity reference as the backend emits it, href and all. */
export type ProductEntityRef = NonNullable<ProductEntityList['entities']>[number];
export type EntityKind = ProductEntityRef['kind'];
export type SourceHealth = components['schemas']['Fleet.SourceState']['status'];
export type Direction = NonNullable<ProductNeighborhood['direction']>;
export type KnowledgeView = NonNullable<ProductNeighborhood['views']>[number];
/** Attention severity is the narrower domain (no "unknown"), derived from the wire. */
export type FindingSeverity = NonNullable<ProductAttentionList['items']>[number]['severity'];

// ── product request shapes (derived from the generated operations) ────────────
// The wire query/body types come straight from the generated operations, so adding,
// changing or removing an OPTIONAL wire request field flows into the facade type
// automatically. The only field redeclared is the deliberate ergonomic refinement:
// `views` is an array here but a comma-joined string on the wire. Every other
// field is inherited, never repeated by hand.
type FleetEntitiesQuery = NonNullable<operations['fleet-entities']['parameters']['query']>;
type FleetNeighborhoodQuery = NonNullable<operations['fleet-neighborhood']['parameters']['query']>;
type FleetAttentionQuery = NonNullable<operations['fleet-attention']['parameters']['query']>;
type FleetImpactBody = NonNullable<operations['fleet-impact-post']['requestBody']>['content']['application/json'];

export type FleetEntitiesInput = FleetEntitiesQuery;
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
 * cannot enforce and returns the discriminated union the UI
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

function errorCode(error: unknown): { code: string; plan?: DeliveryPlan } {
  if (error && typeof error === 'object') {
    const e = error as { code?: string; plan?: DeliveryPlan };
    return { code: e.code ?? '', plan: e.plan };
  }
  return { code: '' };
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
    const { code, plan } = errorCode(error);
    throw new ApiError(response.status, errorMessage(error, response), code, plan);
  }
  return (data ?? null) as T;
}

/** productGet/productPost validate the product schema version at the boundary. */
async function productGet<T extends { meta?: ProductMeta }>(p: Promise<FetchResult<T>>): Promise<T> {
  const res = await unwrap<T>(p);
  checkProductSchema(res?.meta);
  return res;
}

// The CSRF token every mutating request carries, read from the session. A
// signed-in browser holds a session cookie, and the server then asks for the
// token on every POST, PUT, PATCH and DELETE, not only on delivery ones.
let csrf = '';
async function csrfHeaders(): Promise<Record<string, string>> {
  if (!csrf) csrf = (await api.session().catch(() => null))?.csrf ?? '';
  return csrf ? { 'X-Pacto-CSRF': csrf } : {};
}

// A mutating request kept unsent, so a refusal for a stale token can be retried.
const unsent = new WeakMap<Request, Request>();

client.use({
  async onRequest({ request }) {
    if (request.method !== 'GET' && request.method !== 'HEAD') {
      for (const [k, v] of Object.entries(await csrfHeaders())) request.headers.set(k, v);
      unsent.set(request, request.clone());
    }
    return request;
  },
  // Signing in again (in another tab too) issues a new session token, and the
  // server then refuses the one cached here. Read the session again and retry
  // once, only when it has a different token: any other 403 stands.
  async onResponse({ request, response }) {
    const retry = unsent.get(request);
    if (response.status !== 403 || !retry) return response;
    const sent = request.headers.get('X-Pacto-CSRF') ?? '';
    csrf = '';
    const fresh = (await csrfHeaders())['X-Pacto-CSRF'];
    if (!fresh || fresh === sent) return response;
    retry.headers.set('X-Pacto-CSRF', fresh);
    return dashboardFetch(retry);
  },
});

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
  diff: (fromName: string, fromVersion: string, toName: string, toVersion: string): Promise<GetResponse<'/api/diff'>> =>
    unwrap(client.GET('/api/diff', {
      params: { query: { from_name: fromName, from_version: fromVersion || '', to_name: toName, to_version: toVersion || '' } },
    })),
  refresh: (): Promise<PostResponse<'/api/refresh'>> => unwrap(client.POST('/api/refresh', {})),

  // ── Operational graph (fleet) legacy ──
  fleetSnapshot: (): Promise<GetResponse<'/api/fleet/snapshot'>> => unwrap(client.GET('/api/fleet/snapshot')),
  fleetServices: (
    params: NonNullable<operations['fleet-search']['parameters']['query']> = {},
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

  // ── Product-oriented operational-graph APIs ──
  fleetOverview: (): Promise<ProductOverview> =>
    productGet(client.GET('/api/fleet/overview')),
  fleetEntities: (params: FleetEntitiesInput = {}): Promise<ProductEntityList> => {
    const { kinds, ...rest } = params;
    return productGet(client.GET('/api/fleet/entities', {
      params: { query: { ...rest, kinds: kinds?.length ? kinds : undefined } },
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
  /** Lazily reads ONE document body belonging to exactly this revision. Kept off
   *  the entity detail so a revision page stays bounded. */
  fleetRevisionDocument: (key: string, path: string): Promise<ProductRevisionDocument> =>
    productGet(client.GET('/api/fleet/revisions/document', { params: { query: { key, path } } })),

  // ── Delivery. Every call runs as the caller: the signed-in person, or the
  // kubeconfig user of a local dashboard. The apiserver decides. ──
  session: async (): Promise<DeliverySession> => {
    const s = await unwrap(client.GET('/api/session'));
    csrf = s?.csrf ?? '';
    return s;
  },
  /** Ends the session (POST /auth/logout, outside the API) and returns the identity
   *  provider's logout page when it has one. */
  signOut: async (): Promise<string> => {
    const res = await dashboardFetch(new Request(new URL('/auth/logout', location.origin), {
      method: 'POST', headers: await csrfHeaders(),
    }));
    if (!res.ok) throw new ApiError(res.status, (await res.text()) || res.statusText);
    csrf = '';
    const body = (await res.json().catch(() => ({}))) as { logoutURL?: string };
    return body.logoutURL ?? '';
  },
  deliveryInbox: (): Promise<DeliveryInbox> => unwrap(client.GET('/api/delivery/inbox')),
  deliveryEnvironments: (): Promise<DeliveryEnvironments> => unwrap(client.GET('/api/delivery/environments')),
  deliveryPublished: (): Promise<DeliveryPublished> => unwrap(client.GET('/api/delivery/published')),
  deliveryHistory: (namespace = '', stage = ''): Promise<DeliveryHistory> =>
    unwrap(client.GET('/api/delivery/history', {
      params: { query: { namespace: namespace || undefined, stage: stage || undefined } },
    })),
  deliveryBlame: (namespace: string, stage: string, digest = ''): Promise<DeliveryBlame> =>
    unwrap(client.GET('/api/delivery/blame', { params: { query: { namespace, stage, digest: digest || undefined } } })),
  deliveryUndo: (namespace: string, name: string, stage = ''): Promise<DeliveryUndo> =>
    unwrap(client.GET('/api/delivery/undo', { params: { query: { namespace, name, stage: stage || undefined } } })),
  deliveryRefusals: (): Promise<DeliveryRefusals> => unwrap(client.GET('/api/delivery/refusals')),
  deliveryOperation: (namespace: string, name: string): Promise<DeliveryOperation> =>
    unwrap(client.GET('/api/delivery/operations/{namespace}/{name}', { params: { path: { namespace, name } } })),
  deliveryPlan: (body: DeliveryRequest): Promise<DeliveryPlan> =>
    unwrap(client.POST('/api/delivery/plan', { body })),
  deliveryPropose: (body: DeliveryRequest): Promise<CreatedResponse<'/api/delivery/operations'>> =>
    unwrap(client.POST('/api/delivery/operations', { body })),
  deliveryDecide: (
    namespace: string, name: string, decision: DeliveryDecision, note = '',
  ): Promise<CreatedResponse<'/api/delivery/operations/{namespace}/{name}/decision'>> =>
    unwrap(client.POST('/api/delivery/operations/{namespace}/{name}/decision', {
      params: { path: { namespace, name } }, body: { decision, note: note || undefined },
    })),
  deliveryCancel: (namespace: string, name: string): Promise<DeleteResponse<'/api/delivery/operations/{namespace}/{name}'>> =>
    unwrap(client.DELETE('/api/delivery/operations/{namespace}/{name}', {
      params: { path: { namespace, name } },
    })),
};
