/**
 * Typed product-API DTOs (Phase 1 item 8).
 *
 * These mirror the dashboard product transport DTOs (pkg/dashboard/producttransport.go
 * + fleet_product.go) that the HTTP endpoints return. They are the primary
 * frontend contract: every reference carries a canonical `href` added by the
 * transport, and every collection is a bounded preview or page.
 *
 * Finite backend vocabularies are modeled as literal-union types (a client-side
 * refinement of the plain-string wire), and ProductEntityDetail is a real
 * discriminated union keyed by entity.kind.
 *
 * Drift is CI-blocking: TestProductTypesMatchOpenAPI (pkg/dashboard) parses this
 * file and the api.ts operations and compares them STRUCTURALLY (field names,
 * types, arrays and item types, refs, required-vs-optional, previews/pages,
 * enums, and operation query/body parameters) against the generated OpenAPI, so
 * the Go structs and this contract can never diverge silently. Keep interfaces to
 * one field per line so the drift gate parses them.
 */

/** The product schema version this client understands. */
export const PRODUCT_SCHEMA_VERSION = 'pacto.dev/fleet-product/v1';

/**
 * SchemaCompatibilityError is thrown when the server's product schema version is
 * not the one this client was built against, so the UI can show an actionable
 * "reload / upgrade" message instead of misreading an incompatible payload.
 */
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
export function checkProductSchema(meta: ProductMeta | undefined): void {
  const v = meta?.schemaVersion ?? '';
  if (v !== PRODUCT_SCHEMA_VERSION) {
    throw new SchemaCompatibilityError(v);
  }
}

// ── finite backend vocabularies (client-side refinement of the string wire) ──

/** EntityKind is the discriminator for every navigable entity. */
export type EntityKind = 'service' | 'revision' | 'target' | 'owner' | 'source';
/** Completeness is the snapshot completeness level. */
export type Completeness = 'complete' | 'partial' | 'empty';
/** SourceHealth is a source's health status. */
export type SourceHealth = 'available' | 'partial' | 'stale' | 'unavailable';
/** KnowledgeView selects the expected / observed / differences knowledge layer. */
export type KnowledgeView = 'expected' | 'observed' | 'differences';
/** DifferenceState is an edge's declared-vs-observed reconciliation verdict. */
export type DifferenceState =
  | 'matched'
  | 'expected-not-observed'
  | 'observed-not-expected'
  | 'insufficient';
/** LinkState is a target's revision-link classification. */
export type LinkState = 'exact' | 'inferred' | 'ambiguous' | 'unresolved';
/** Direction is a graph traversal direction. */
export type Direction = 'dependencies' | 'dependents' | 'both';
/** Provenance is how a relationship edge is known. */
export type Provenance = 'declared' | 'observed';
/** EntryPointView is the route-neutral destination class of an overview entry point. */
export type EntryPointView = 'attention' | 'services' | 'overview';
/** FindingSeverity ranks a finding or attention item. */
export type FindingSeverity = 'error' | 'warning' | 'info';

// ── reusable bounded shapes ──────────────────────────────────────────────────

/** Preview is a bounded, truncatable slice of items. */
export interface Preview<T> {
  total: number;
  count: number;
  truncated: boolean;
  items: T[];
}

/** Page is an offset-paged, bounded slice of items. */
export interface Page<T> {
  total: number;
  count: number;
  limit: number;
  offset: number;
  truncated: boolean;
  nextOffset?: number;
  items: T[];
}

// ── leaf value types ─────────────────────────────────────────────────────────

export interface SourceError {
  code: string;
  message: string;
}

export interface SourceState {
  id: string;
  kind: string;
  status: SourceHealth;
  lastSuccessfulSync?: string;
  observedAt?: string;
  error?: SourceError;
  revisionCount: number;
  targetCount: number;
}

export interface Limitation {
  code: string;
  source?: string;
  message: string;
}

export interface Coverage {
  evaluated: number;
  required: number;
}

export interface RevisionIdentity {
  digest?: string;
  requestedRef?: string;
  resolvedRef?: string;
  immutable: boolean;
}

export interface ToolSummary {
  name: string;
  method: string;
  path: string;
  summary?: string;
  mutating: boolean;
}

export interface DocRef {
  path: string;
  title: string;
}

export interface DeclaredClaim {
  sourceRevision?: string;
  required?: boolean;
  compatibility?: string;
  reconciliation?: string;
  requestedRef?: string;
  lockedVersion?: string;
  lockedDigest?: string;
}

export interface ObservedSourceStat {
  source: string;
  count: number;
  firstSeen?: string;
  lastSeen?: string;
}

/** SubjectRef identifies the contract element a finding is about (engine type). */
export interface SubjectRef {
  Kind: string;
  Name: string;
}

/** EvidenceRef points at the evidence backing a finding (engine type). */
export interface EvidenceRef {
  Source: string;
  ObservedAt: string;
}

/** Finding is an engine finding (validation or compliance). Fields are PascalCase to match the engine JSON. */
export interface Finding {
  Code: string;
  Severity: FindingSeverity;
  Category: string;
  Subject: SubjectRef;
  ContractPath: string;
  Message: string;
  EvidenceRefs: EvidenceRef[];
}

/** ReadinessCheck is one derived readiness check (bounded preview member). */
export interface ReadinessCheck {
  id: string;
  type?: string;
  category?: string;
  status?: string;
  evidence?: string;
  description?: string;
  weight: number;
  earnedWeight: number;
  excluded?: boolean;
}

/** ProductReadiness is the bounded, product-shaped readiness assessment. */
export interface ProductReadiness {
  score: number;
  totalWeight: number;
  earnedWeight: number;
  minScore: number;
  partialCredit: number;
  expires?: string;
  expired: boolean;
  daysRemaining?: number;
  doneCount: number;
  partialCount: number;
  notDoneCount: number;
  deferredCount: number;
  passing: boolean;
  checks: Preview<ReadinessCheck>;
}

/** RuntimeFact is one flattened observed-runtime leaf. */
export interface RuntimeFact {
  key: string;
  value: string;
}

/** ProductMeta is the completeness envelope on every product answer. */
export interface ProductMeta {
  schemaVersion: string;
  snapshotId: string;
  asOf: string;
  completeness: Completeness;
  sources?: SourceState[];
  sourcesTruncated?: boolean;
  limitations?: Limitation[];
  limitationsTruncated?: boolean;
}

/** ProductRef is a navigable entity reference with a canonical href. */
export interface ProductRef {
  kind: EntityKind;
  key: string;
  label: string;
  secondary?: string;
  status?: string;
  explanation?: string;
  domain?: string;
  scope?: string;
  parentService?: string;
  href: string;
}

export interface Ownership {
  owner?: string;
  ref?: ProductRef;
  conflicts: Preview<string>;
}

export interface AttributedFinding {
  finding: Finding;
  entity: ProductRef;
}

export interface AttributedLimitation {
  limitation: Limitation;
  entity?: ProductRef;
}

// ── overview ─────────────────────────────────────────────────────────────────

export interface OverviewSummary {
  services: number;
  servicesNeedingAttention: number;
  invalidRevisions: number;
  exactTargetLinks: number;
  inferredTargetLinks: number;
  ambiguousTargetLinks: number;
  unresolvedTargetLinks: number;
  nonCompliantTargets: number;
  unknownTargets: number;
  staleTargets: number;
  unresolvedRelationships: number;
  observedOnlyRelationships: number;
  degradedSources: number;
  staleSources: number;
  unavailableSources: number;
  recentEvidence: number;
}

export interface AttentionItem {
  entity: ProductRef;
  service?: string;
  label: string;
  severity: FindingSeverity;
  code: string;
  category: string;
  summary: string;
  reason: string;
  source?: string;
  nextStep?: string;
}

export interface EvidenceItem {
  target: ProductRef;
  at?: string;
}

export interface EntryPoint {
  label: string;
  description?: string;
  view: EntryPointView;
  category?: string;
  count?: number;
  href: string;
}

export interface ProductOverview {
  meta: ProductMeta;
  summary: OverviewSummary;
  attention: AttentionItem[];
  recentEvidence: EvidenceItem[];
  entryPoints: EntryPoint[];
}

// ── entities ─────────────────────────────────────────────────────────────────

export interface ProductEntityList {
  meta: ProductMeta;
  total: number;
  count: number;
  limit: number;
  offset: number;
  truncated: boolean;
  nextOffset?: number;
  entities: ProductRef[];
}

// ── attention ────────────────────────────────────────────────────────────────

export interface ProductAttentionList {
  meta: ProductMeta;
  total: number;
  count: number;
  limit: number;
  offset: number;
  truncated: boolean;
  nextOffset?: number;
  items: AttentionItem[];
}

// ── neighborhood ─────────────────────────────────────────────────────────────

export interface NeighborhoodNode {
  ref: ProductRef;
  depth: number;
  focus?: boolean;
  status?: string;
  owner?: string;
  revisionState?: string;
  expansions?: Direction[];
}

export interface NeighborhoodEdge {
  id: string;
  from: ProductRef;
  to: ProductRef;
  expected: boolean;
  observed: boolean;
  provenance: Provenance;
  difference: DifferenceState;
  declaredClaims: Preview<DeclaredClaim>;
  observationSources: Preview<ObservedSourceStat>;
  count?: number;
  firstSeen?: string;
  lastSeen?: string;
  stale?: boolean;
  href: string;
}

export interface UnresolvedDependency {
  from: ProductRef;
  ref: string;
  sourceRevision?: string;
  requestedRef?: string;
  reason?: string;
}

export interface ProductNeighborhood {
  meta: ProductMeta;
  requestedFocus: ProductRef;
  focusService: ProductRef;
  direction: Direction;
  depth: number;
  views: KnowledgeView[];
  nodes: NeighborhoodNode[];
  edges: NeighborhoodEdge[];
  unresolvedDependencies: Preview<UnresolvedDependency>;
  truncated: boolean;
  maxNodes: number;
  maxEdges: number;
}

// ── entity detail payloads ───────────────────────────────────────────────────

export interface ServiceDetail {
  domain?: string;
  ownership?: Ownership;
  revisions: Preview<ProductRef>;
  deployments: Preview<ProductRef>;
  dependencies: Preview<ProductRef>;
  dependents: Preview<ProductRef>;
  relationships: Preview<NeighborhoodEdge>;
  findings: Preview<AttributedFinding>;
  evidence: Preview<EvidenceItem>;
  limitations: Preview<AttributedLimitation>;
}

export interface RevisionDetail {
  service: ProductRef;
  version?: string;
  pactoVersion?: string;
  identity: RevisionIdentity;
  valid: boolean;
  readiness?: ProductReadiness;
  validation: Preview<Finding>;
  interfaces: number;
  configurations: number;
  policies: number;
  capabilities: number;
  dependencies: Preview<NeighborhoodEdge>;
  tools: Preview<ToolSummary>;
  skills: Preview<string>;
  docs: Preview<DocRef>;
  exactTargets: Preview<ProductRef>;
  inferredTargets: Preview<ProductRef>;
  previous?: ProductRef;
  next?: ProductRef;
  ownership?: Ownership;
  limitations: Preview<Limitation>;
}

export interface TargetDetail {
  service: ProductRef;
  revision?: ProductRef;
  linkState: LinkState;
  scope?: string;
  kind?: string;
  compliance: string;
  coverage?: Coverage;
  findings: Preview<Finding>;
  observedRuntime: Preview<RuntimeFact>;
  sources: Preview<string>;
  source?: string;
  identity: RevisionIdentity;
  evidenceAt?: string;
  reconciledAt?: string;
  stale: boolean;
  quarantined?: boolean;
  ownership?: Ownership;
  limitations: Preview<Limitation>;
}

export interface OwnerDetail {
  services: Preview<ProductRef>;
  revisions: Preview<ProductRef>;
  deployments: Preview<ProductRef>;
  attention: Preview<AttentionItem>;
}

export interface SourceDetail {
  kind?: string;
  health: SourceHealth;
  lastSuccessfulSync?: string;
  observedAt?: string;
  revisionCount: number;
  targetCount: number;
  entities: Preview<ProductRef>;
  error?: SourceError;
  limitations: Preview<Limitation>;
}

// ── entity detail (discriminated union keyed by entity.kind) ──────────────────
//
// Each variant narrows entity to a kind-specific ProductRef, REQUIRES its own
// payload, and marks the other four payloads `?: never` so an object literal
// carrying zero payloads (the required one is missing) or more than one (a second
// payload is typed `never`) does not type-check. Because TypeScript cannot narrow
// a parent union from a nested discriminant, the exported type guards (which read
// entity.kind) are how callers narrow correctly from entity.kind.

export type ServiceRef = ProductRef & { kind: 'service' };
export type RevisionRef = ProductRef & { kind: 'revision' };
export type TargetRef = ProductRef & { kind: 'target' };
export type OwnerRef = ProductRef & { kind: 'owner' };
export type SourceRef = ProductRef & { kind: 'source' };

export interface ServiceEntityDetail {
  meta: ProductMeta;
  entity: ServiceRef;
  status?: string;
  service: ServiceDetail;
  revision?: never;
  target?: never;
  owner?: never;
  source?: never;
  actions?: string[];
}

export interface RevisionEntityDetail {
  meta: ProductMeta;
  entity: RevisionRef;
  status?: string;
  service?: never;
  revision: RevisionDetail;
  target?: never;
  owner?: never;
  source?: never;
  actions?: string[];
}

export interface TargetEntityDetail {
  meta: ProductMeta;
  entity: TargetRef;
  status?: string;
  service?: never;
  revision?: never;
  target: TargetDetail;
  owner?: never;
  source?: never;
  actions?: string[];
}

export interface OwnerEntityDetail {
  meta: ProductMeta;
  entity: OwnerRef;
  status?: string;
  service?: never;
  revision?: never;
  target?: never;
  owner: OwnerDetail;
  source?: never;
  actions?: string[];
}

export interface SourceEntityDetail {
  meta: ProductMeta;
  entity: SourceRef;
  status?: string;
  service?: never;
  revision?: never;
  target?: never;
  owner?: never;
  source: SourceDetail;
  actions?: string[];
}

/** ProductEntityDetail is a discriminated union: entity.kind selects the payload. */
export type ProductEntityDetail =
  | ServiceEntityDetail
  | RevisionEntityDetail
  | TargetEntityDetail
  | OwnerEntityDetail
  | SourceEntityDetail;

/** isServiceDetail narrows a ProductEntityDetail to its service variant via entity.kind. */
export function isServiceDetail(d: ProductEntityDetail): d is ServiceEntityDetail {
  return d.entity.kind === 'service';
}
/** isRevisionDetail narrows a ProductEntityDetail to its revision variant via entity.kind. */
export function isRevisionDetail(d: ProductEntityDetail): d is RevisionEntityDetail {
  return d.entity.kind === 'revision';
}
/** isTargetDetail narrows a ProductEntityDetail to its target variant via entity.kind. */
export function isTargetDetail(d: ProductEntityDetail): d is TargetEntityDetail {
  return d.entity.kind === 'target';
}
/** isOwnerDetail narrows a ProductEntityDetail to its owner variant via entity.kind. */
export function isOwnerDetail(d: ProductEntityDetail): d is OwnerEntityDetail {
  return d.entity.kind === 'owner';
}
/** isSourceDetail narrows a ProductEntityDetail to its source variant via entity.kind. */
export function isSourceDetail(d: ProductEntityDetail): d is SourceEntityDetail {
  return d.entity.kind === 'source';
}

// ── impact ───────────────────────────────────────────────────────────────────

export interface ImpactConsumer {
  service: ProductRef;
  path: ProductRef[];
  pathTotal: number;
  pathTruncated: boolean;
  depth: number;
  direct: boolean;
  confidence: string;
  compatibilityVerdict: string;
  owner?: string;
}

export interface ProductImpact {
  meta: ProductMeta;
  snapshotId: string;
  snapshotMatch: boolean;
  service: ProductRef;
  oldRevision?: ProductRef;
  newRevision?: ProductRef;
  classification: string;
  consumers: Page<ImpactConsumer>;
  owners: Preview<ProductRef>;
  activeTargets: Preview<ProductRef>;
  limitations: Preview<Limitation>;
}
