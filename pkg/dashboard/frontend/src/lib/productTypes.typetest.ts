/**
 * Compile-time type tests for the ProductEntityDetail discriminated union
 * (Phase 1 item 8). This file is type-checked by `npm run lint` (svelte-check,
 * threshold error): each `@ts-expect-error` asserts that an INVALID payload
 * combination does NOT type-check — if such a combination ever became valid, the
 * unused `@ts-expect-error` directive would itself become an error and fail lint.
 *
 * It runs no code; the `void` uses keep the assertions from being unused.
 */

import type {
  ProductEntityDetail,
  ProductMeta,
  ServiceRef,
  RevisionRef,
  ServiceDetail,
  RevisionDetail,
  TargetDetail,
} from './productTypes';
import { isServiceDetail, isRevisionDetail } from './productTypes';

declare const meta: ProductMeta;
declare const serviceRef: ServiceRef;
declare const revisionRef: RevisionRef;
declare const service: ServiceDetail;
declare const revision: RevisionDetail;
declare const target: TargetDetail;

// Valid: a service-kind entity with the service payload.
const okService: ProductEntityDetail = { meta, entity: serviceRef, service };
void okService;

// Valid: a revision-kind entity with the revision payload.
const okRevision: ProductEntityDetail = { meta, entity: revisionRef, revision };
void okRevision;

// @ts-expect-error a service-kind entity with NO payload is invalid (payload required).
const missingPayload: ProductEntityDetail = { meta, entity: serviceRef };
void missingPayload;

// @ts-expect-error a service-kind entity carrying a revision payload is invalid.
const wrongPayload: ProductEntityDetail = { meta, entity: serviceRef, revision };
void wrongPayload;

// @ts-expect-error a service-kind entity carrying TWO payloads is invalid.
const twoPayloads: ProductEntityDetail = { meta, entity: serviceRef, service, revision };
void twoPayloads;

// @ts-expect-error a target payload with a service-kind entity is invalid.
const mismatchedTarget: ProductEntityDetail = { meta, entity: serviceRef, target };
void mismatchedTarget;

// The type guards narrow (via entity.kind) to the correct payload variant.
function narrows(d: ProductEntityDetail): ServiceDetail | RevisionDetail | null {
  if (isServiceDetail(d)) {
    return d.service; // narrowed to ServiceEntityDetail
  }
  if (isRevisionDetail(d)) {
    return d.revision; // narrowed to RevisionEntityDetail
  }
  return null;
}
void narrows;

// After narrowing to the service variant, the service payload is present and the
// revision payload is provably absent (typed `never`, i.e. undefined at runtime).
function narrowedPayloads(d: ProductEntityDetail): ServiceDetail | null {
  if (isServiceDetail(d)) {
    const r: RevisionDetail | undefined = d.revision; // `never` — always absent here
    void r;
    return d.service;
  }
  return null;
}
void narrowedPayloads;

// On the un-narrowed union a payload is only ever `T | undefined`, so it cannot be
// used as a definite payload without narrowing/guarding first.
function noBlindDefiniteAccess(d: ProductEntityDetail): ServiceDetail {
  // @ts-expect-error 'service' is possibly undefined on the un-narrowed union.
  return d.service;
}
void noBlindDefiniteAccess;
