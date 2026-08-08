/**
 * Compile-time type tests for the entity-detail discriminated union (requirement,
 * item 7). Type-checked by `npm run lint` (svelte-check, threshold error): each
 * `@ts-expect-error` asserts an INVALID payload combination does NOT type-check; if
 * one ever became valid, the unused directive would itself fail lint.
 *
 * The union (NarrowedEntityDetail) and its variant types are DERIVED from the
 * generated ProductEntityDetail - they refer to the generated payload types and add
 * only kind-narrowing and exclusivity, never redeclaring a payload schema. It runs
 * no code; the `void` uses keep the assertions from being unused.
 */

import type {
  ProductEntityDetail,
  NarrowedEntityDetail,
  ProductMeta,
  FleetEntitiesInput,
  FleetNeighborhoodInput,
  FleetAttentionInput,
  FleetImpactInput,
  EntityKind,
  KnowledgeView,
} from './api.ts';
import {
  api,
  isServiceDetail,
  isRevisionDetail,
  isTargetDetail,
  isOwnerDetail,
  isSourceDetail,
} from './api.ts';
import type { operations } from './generated/schema';

// ── type-level assertion helpers ─────────────────────────────────────────────
type Expect<T extends true> = T;
type Equal<A, B> =
  (<G>() => G extends A ? 1 : 2) extends (<G>() => G extends B ? 1 : 2) ? true : false;
type IsAny<T> = 0 extends 1 & T ? true : false;
type IsUnknown<T> = IsAny<T> extends true ? false : unknown extends T ? true : false;

// ── requirement, item 2: facade request types are DERIVED from generated ops ──
// Every NON-transformed wire request field flows into the facade input automatically;
// only `kinds`/`views` are the deliberate ergonomic (array vs comma-joined) refinement.
// If a future OpenAPI change adds/removes/retypes a wire field and the facade fails to
// inherit it (a hand-redeclared shape), these equalities break at compile time.
type EntitiesWireQuery = NonNullable<operations['fleet-entities']['parameters']['query']>;
type NeighborhoodWireQuery = NonNullable<operations['fleet-neighborhood']['parameters']['query']>;
type AttentionWireQuery = NonNullable<operations['fleet-attention']['parameters']['query']>;
type ImpactWireBody = NonNullable<operations['fleet-impact-post']['requestBody']>['content']['application/json'];

type _EntitiesDerived = Expect<Equal<Omit<FleetEntitiesInput, 'kinds'>, Omit<EntitiesWireQuery, 'kinds'>>>;
type _EntitiesKindsTransformed = Expect<Equal<NonNullable<FleetEntitiesInput['kinds']>, EntityKind[]>>;
type _NeighborhoodDerived = Expect<Equal<Omit<FleetNeighborhoodInput, 'views'>, Omit<NeighborhoodWireQuery, 'views'>>>;
type _NeighborhoodViewsTransformed = Expect<Equal<NonNullable<FleetNeighborhoodInput['views']>, KnowledgeView[]>>;
type _AttentionDerived = Expect<Equal<FleetAttentionInput, AttentionWireQuery>>;
type _ImpactDerived = Expect<Equal<FleetImpactInput, ImpactWireBody>>;

// ── requirement, item 3: no dashboard backend operation returns `unknown` ─────
// The set of facade methods whose awaited return is `unknown` must be empty.
type UnknownReturnMethods = {
  [K in keyof typeof api]: IsUnknown<Awaited<ReturnType<typeof api[K]>>> extends true ? K : never;
}[keyof typeof api];
type _NoUnknownReturns = Expect<Equal<UnknownReturnMethods, never>>;

// ── requirement, item 4: entity detail leaves the facade as NarrowedEntityDetail ──
type _EntityDetailNarrowed = Expect<Equal<Awaited<ReturnType<typeof api.fleetEntityDetail>>, NarrowedEntityDetail>>;

// Reference the assertion aliases so they are not reported as unused declarations.
export type _ItemTwoThreeFour = [
  _EntitiesDerived, _EntitiesKindsTransformed, _NeighborhoodDerived, _NeighborhoodViewsTransformed,
  _AttentionDerived, _ImpactDerived, _NoUnknownReturns, _EntityDetailNarrowed,
];

// Non-vacuousness guard: prove Expect/Equal actually REJECT a false equality, so the
// assertions above are meaningful. The @ts-expect-error is "used" only because
// `Expect<Equal<string, number>>` genuinely does not type-check; were the helpers ever
// vacuous, the directive would be unused and svelte-check (threshold error) would fail.
// @ts-expect-error string and number are not equal, so this must be a type error.
export type _NonVacuous = Expect<Equal<string, number>>;

type Entity = NonNullable<ProductEntityDetail['entity']>;
type ServicePayload = NonNullable<ProductEntityDetail['service']>;
type RevisionPayload = NonNullable<ProductEntityDetail['revision']>;
type TargetPayload = NonNullable<ProductEntityDetail['target']>;
type OwnerPayload = NonNullable<ProductEntityDetail['owner']>;
type SourcePayload = NonNullable<ProductEntityDetail['source']>;

declare const meta: ProductMeta;
declare const serviceRef: Entity & { kind: 'service' };
declare const revisionRef: Entity & { kind: 'revision' };
declare const targetRef: Entity & { kind: 'target' };
declare const ownerRef: Entity & { kind: 'owner' };
declare const sourceRef: Entity & { kind: 'source' };
declare const service: ServicePayload;
declare const revision: RevisionPayload;
declare const target: TargetPayload;
declare const owner: OwnerPayload;
declare const source: SourcePayload;

// Valid: each kind carries exactly its own payload.
const okService: NarrowedEntityDetail = { meta, entity: serviceRef, service };
const okRevision: NarrowedEntityDetail = { meta, entity: revisionRef, revision };
const okTarget: NarrowedEntityDetail = { meta, entity: targetRef, target };
const okOwner: NarrowedEntityDetail = { meta, entity: ownerRef, owner };
const okSource: NarrowedEntityDetail = { meta, entity: sourceRef, source };
void okService;
void okRevision;
void okTarget;
void okOwner;
void okSource;

// @ts-expect-error a service-kind entity with NO payload is invalid (payload required).
const missingPayload: NarrowedEntityDetail = { meta, entity: serviceRef };
void missingPayload;

// @ts-expect-error a service-kind entity carrying a revision payload is invalid.
const wrongPayload: NarrowedEntityDetail = { meta, entity: serviceRef, revision };
void wrongPayload;

// @ts-expect-error a service-kind entity carrying TWO payloads is invalid.
const twoPayloads: NarrowedEntityDetail = { meta, entity: serviceRef, service, revision };
void twoPayloads;

// @ts-expect-error a target payload with a service-kind entity is invalid.
const mismatchedTarget: NarrowedEntityDetail = { meta, entity: serviceRef, target };
void mismatchedTarget;

// The type guards narrow (via entity.kind) to the correct payload variant.
function narrows(d: ProductEntityDetail): ServicePayload | RevisionPayload | TargetPayload | OwnerPayload | SourcePayload | null {
  if (isServiceDetail(d)) return d.service;
  if (isRevisionDetail(d)) return d.revision;
  if (isTargetDetail(d)) return d.target;
  if (isOwnerDetail(d)) return d.owner;
  if (isSourceDetail(d)) return d.source;
  return null;
}
void narrows;

// After narrowing to the service variant, the service payload is present and the
// revision payload is provably absent (typed `never`).
function narrowedPayloads(d: ProductEntityDetail): ServicePayload | null {
  if (isServiceDetail(d)) {
    const r: RevisionPayload | undefined = d.revision; // `never` here
    void r;
    return d.service;
  }
  return null;
}
void narrowedPayloads;

// On the un-narrowed generated shape a payload is only ever `T | undefined`, so it
// cannot be used as a definite payload without narrowing first.
function noBlindDefiniteAccess(d: ProductEntityDetail): ServicePayload {
  // @ts-expect-error 'service' is possibly undefined on the un-narrowed shape.
  return d.service;
}
void noBlindDefiniteAccess;
