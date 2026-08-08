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
} from './api.ts';
import {
  isServiceDetail,
  isRevisionDetail,
  isTargetDetail,
  isOwnerDetail,
  isSourceDetail,
} from './api.ts';

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
