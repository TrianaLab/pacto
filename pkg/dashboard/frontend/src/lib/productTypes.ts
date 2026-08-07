/**
 * Typed product-API DTOs (Phase 1 item 9).
 *
 * These mirror the dashboard product transport DTOs (pkg/dashboard/producttransport.go
 * + fleet_product.go) that the HTTP endpoints return. They are the primary
 * frontend contract: every reference carries a canonical `href` added by the
 * transport, and every collection is a bounded preview or page.
 *
 * Drift is CI-blocking: TestProductTypesMatchOpenAPI (pkg/dashboard) parses this
 * file and asserts every interface's field names equal the generated OpenAPI
 * schema's property names, so the Go structs and this contract can never diverge
 * silently. Keep interfaces to one field per line so the drift gate parses them.
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
  status: string;
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

/** ProductMeta is the completeness envelope on every product answer. */
export interface ProductMeta {
  schemaVersion: string;
  snapshotId: string;
  asOf: string;
  completeness: string;
  sources?: SourceState[];
  sourcesTruncated?: boolean;
  limitations?: Limitation[];
  limitationsTruncated?: boolean;
}

/** ProductRef is a navigable entity reference with a canonical href. */
export interface ProductRef {
  kind: string;
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
  conflicts?: string[];
}

export interface AttributedFinding {
  finding: unknown;
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
  severity: string;
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
  view: string;
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
  expansions?: string[];
}

export interface NeighborhoodEdge {
  id: string;
  from: ProductRef;
  to: ProductRef;
  expected: boolean;
  observed: boolean;
  provenance: string;
  difference: string;
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
  direction: string;
  depth: number;
  views: string[];
  nodes: NeighborhoodNode[];
  edges: NeighborhoodEdge[];
  unresolvedDependencies: Preview<UnresolvedDependency>;
  truncated: boolean;
  maxNodes: number;
  maxEdges: number;
}

// ── entity detail (discriminated by entity.kind) ─────────────────────────────

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
  readiness?: unknown;
  validation: Preview<unknown>;
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
  linkState: string;
  scope?: string;
  kind?: string;
  compliance: string;
  coverage?: Coverage;
  findings: Preview<unknown>;
  observedRuntime?: Record<string, unknown>;
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
  health: string;
  lastSuccessfulSync?: string;
  observedAt?: string;
  revisionCount: number;
  targetCount: number;
  entities: Preview<ProductRef>;
  error?: SourceError;
  limitations: Preview<Limitation>;
}

export interface ProductEntityDetail {
  meta: ProductMeta;
  entity: ProductRef;
  status?: string;
  service?: ServiceDetail;
  revision?: RevisionDetail;
  target?: TargetDetail;
  owner?: OwnerDetail;
  source?: SourceDetail;
  actions?: string[];
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
