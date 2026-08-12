// The canonical identity of every entity the live Kind fixture published.
//
// Nothing in this directory constructs a key. A ServiceKey is domain-escaped, a
// RevisionKey is a ServiceKey plus a content id, a TargetKey is scope/kind/name:
// re-deriving those escapes in TypeScript would be a second implementation of the
// identity rules that could agree with itself while disagreeing with the product.
// tests/e2e/kind/productready discovers them through /api/fleet/entities, proves each
// one resolves, and hands them here verbatim in PW_FIXTURE — so the browser layer
// addresses exactly the entities the backend published, and a journey that navigates
// somewhere else fails instead of quietly asserting about a different entity.

export interface LiveFixture {
  snapshotId: string;
  domain: string;
  checkoutService: string;
  ordersService: string;
  evidenceService: string;
  checkoutRevisionA: string;
  checkoutRevisionB: string;
  ordersRevision: string;
  checkoutTarget: string;
  ordersTarget: string;
  evidenceTarget: string;
  ociSource: string;
  observationSource: string;
  evidenceSource: string;
  checkoutVersionA: string;
  checkoutVersionB: string;
  ordersVersion: string;
  checkoutName: string;
  ordersName: string;
}

function load(): LiveFixture {
  const raw = process.env.PW_FIXTURE;
  if (!raw) {
    // Failing at import time is deliberate: a live run without the fixture would
    // otherwise silently degrade into the smoke check this suite replaced.
    throw new Error(
      'PW_FIXTURE is not set. The live product journeys run only against the Kind '
      + 'fixture: `bash tests/e2e/kind/operational-graph.sh browser` publishes it from '
      + 'tests/e2e/kind/productready.',
    );
  }
  return JSON.parse(raw) as LiveFixture;
}

export const fixture: LiveFixture = load();

// The product's own route grammar, mirroring lib/router.ts. Only the ROUTE is built
// here; every key in it was discovered.
const PLURAL: Record<string, string> = {
  service: 'services', revision: 'revisions', target: 'targets', source: 'sources', owner: 'owners',
};

export function entityUrl(kind: keyof typeof PLURAL | string, key: string): string {
  return `/#/fleet/${PLURAL[kind] ?? 'services'}/${encodeURIComponent(key)}`;
}
