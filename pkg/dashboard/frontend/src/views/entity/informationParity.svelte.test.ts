/**
 * Information-parity regression gate (requirement 15).
 *
 * The migration from the old service-detail page to the Product entity model lost
 * substantial inspectable detail once already: counts replaced content, and the
 * revision page could answer "3 Interfaces" and nothing more. These tests exist so
 * that regression cannot happen silently a second time.
 *
 * They assert SEMANTIC AVAILABILITY, never layout: for a detail payload that carries
 * a fact, the fact must be reachable in the rendered page. They deliberately do not
 * assert element structure, class names or ordering, so a redesign is free to move
 * anything — it is only forbidden to DROP information the backend already sends.
 *
 * Each capability below is annotated with the old surface it replaces, so a reader
 * can trace it back to the parity matrix in the ledger.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const { detailFn, docFn } = vi.hoisted(() => ({ detailFn: vi.fn(), docFn: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return {
    ...actual,
    api: {
      fleetEntityDetail: (...a: unknown[]) => detailFn(...a),
      fleetRevisionDocument: (...a: unknown[]) => docFn(...a),
    },
  };
});

// @ts-expect-error — Svelte component has no declaration file
import FleetEntityView from '../FleetEntityView.svelte';

const meta = {
  schemaVersion: 'pacto.dev/fleet-product/v1', snapshotId: 'snap-1',
  asOf: '2026-07-29T10:00:00Z', completeness: 'complete',
  sources: [{ id: 'oci', kind: 'oci', status: 'available' }],
};

const preview = (items: unknown[], extra: Record<string, unknown> = {}) => ({
  total: items.length, count: items.length, truncated: false, items, ...extra,
});

/**
 * A revision detail carrying EVERY declared dimension the Contract Inspector is
 * responsible for (requirement 3). Anything present here must render.
 */
function richRevision(): Record<string, any> {
  return {
    meta,
    entity: {
      kind: 'revision', key: 'domain-a/payments@sha256:abc', label: 'payments 2.1.0',
      href: '/fleet/revisions/domain-a%2Fpayments@sha256:abc', status: 'Compliant',
    },
    status: 'Compliant',
    actions: ['open-graph', 'compare', 'impact'],
    revision: {
      service: { kind: 'service', key: 'domain-a/payments', label: 'payments', href: '/fleet/services/domain-a%2Fpayments' },
      version: '2.1.0',
      pactoVersion: '2.0',
      valid: true,
      identity: {
        digest: 'sha256:abcdef1234567890', resolvedRef: 'ghcr.io/acme/payments@sha256:abcdef1234567890',
        requestedRef: 'ghcr.io/acme/payments:2.1.0', retrievable: true, identityClass: 'exact',
      },
      provenance: { source: 'oci', sources: preview(['oci', 'local']), fetchedAt: '2026-07-29T09:00:00Z' },
      readiness: {
        score: 80, minScore: 70, totalWeight: 100, earnedWeight: 80, partialCredit: 0.5,
        doneCount: 4, partialCount: 1, notDoneCount: 1, deferredCount: 1, expired: false, passing: true,
        checks: preview([{ id: 'runbook', status: 'done', category: 'operability', description: 'Runbook published' }]),
      },
      validation: preview([{ code: 'CONFIG_SCHEMA_LOOSE', severity: 'warning', message: 'configuration schema allows additional properties' }]),
      interfaces: preview([{
        name: 'http', type: 'openapi', ref: 'interfaces/openapi.json', visibility: 'public',
        operationsKnown: true,
        operations: preview([
          { name: 'listPayments', method: 'get', path: '/payments', summary: 'List payments', mutating: false },
          { name: 'createPayment', method: 'post', path: '/payments', summary: 'Create a payment', mutating: true },
        ]),
      }]),
      configurations: preview([{
        name: 'runtime', required: true, schema: 'configuration/schema.json',
        values: preview([{ key: 'timeoutSeconds', value: '30' }, { key: 'region', value: 'eu-west-1' }]),
      }, {
        name: 'shared-limits', required: false, ref: 'oci://acme/limits:1.0.0',
        resolution: {
          resolved: true,
          service: { kind: 'service', key: 'domain-a/limits', label: 'limits', href: '/fleet/services/domain-a%2Flimits' },
        },
        values: preview([]),
      }]),
      policies: preview([
        { name: 'pii-redaction', schema: 'policy/schema.json', target: 'data' },
        {
          name: 'retention', ref: 'oci://acme/policies/retention',
          resolution: { resolved: false, reason: 'no service in this domain publishes that contract' },
        },
      ]),
      capabilities: preview([
        { type: 'health', binding: { type: 'http', interface: 'http', path: '/healthz' } },
        { type: 'metrics' },
      ]),
      workload: 'deployment',
      state: { type: 'stateful', persistenceScope: 'cluster', persistenceDurability: 'durable', dataCriticality: 'high' },
      dependencies: preview([{
        id: 'e1', relation: 'dependency', expected: true, observed: true, provenance: 'declared+observed',
        difference: 'matched',
        from: { kind: 'service', key: 'domain-a/payments', label: 'payments', href: '#' },
        to: { kind: 'service', key: 'domain-b/ledger', label: 'ledger', href: '/fleet/services/domain-b%2Fledger' },
        declaredClaims: preview([{
          sourceRevision: 'domain-a/payments@sha256:abc', required: true, compatibility: '^1.2.0',
          requestedRef: 'ghcr.io/acme/ledger:1.2.0', lockedVersion: '1.2.3', lockedDigest: 'sha256:fedcba9876543210',
        }]),
        observationSources: preview(['otel']),
      }]),
      tools: preview([{ name: 'http_listPayments', method: 'get', path: '/payments', summary: 'List payments', mutating: false }]),
      skills: preview(['refund-workflow.md']),
      docs: preview([{ path: 'docs/operations.md', title: 'operations' }]),
      sbom: {
        format: 'spdx', packages: 42,
        licenses: [{ license: 'MIT', count: 30 }, { license: 'Apache-2.0', count: 9 }, { license: 'unspecified', count: 2 }],
        otherLicensed: 1,
      },
      metadata: preview([{ key: 'tier', value: 'gold' }, { key: 'costCenter', value: 'CC-1234' }]),
      exactTargets: preview([{ kind: 'target', key: 'prod/k8s/payments', label: 'payments', href: '/fleet/targets/prod%2Fk8s%2Fpayments' }]),
      inferredTargets: preview([]),
      previous: { kind: 'revision', key: 'domain-a/payments@sha256:old', label: 'payments 2.0.0', href: '/fleet/revisions/x' },
      next: null,
      ownership: { owner: 'team/payments', ref: { kind: 'owner', key: 'team/payments', label: 'team/payments', href: '/fleet/owners/team%2Fpayments' }, conflicts: preview([]) },
      limitations: preview([]),
    },
  };
}

/**
 * mountView resolves once the ENTITY BODY exists, not once the page has any text: the
 * page-level h1 carries the entity key while the request is still in flight, so
 * waiting for the entity's name would resolve against the loading shell.
 */
async function mountView(kind: string, key: string) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const component = mount(FleetEntityView, { target, props: { kind, entityKey: key, refreshTick: 0 } });
  await vi.waitFor(() => expect(target.querySelector('.ev-head')).toBeTruthy());
  return { target, component };
}

async function renderRevision() {
  detailFn.mockResolvedValue(richRevision());
  const { target, component } = await mountView('revision', 'domain-a/payments@sha256:abc');
  await vi.waitFor(() => expect(target.textContent).toContain('payments'));
  return { target, component, text: () => target.textContent || '' };
}

describe('revision detail keeps the whole contract inspectable (requirement 3)', () => {
  beforeEach(() => detailFn.mockReset());

  // The named regression: the page used to reduce the declared surface to four count
  // chips, so "3 Interfaces" WAS the interface experience. A count is not an answer.
  it('renders each declared operation, not a count of interfaces', async () => {
    const { target, component, text } = await renderRevision();
    for (const fragment of ['listPayments', 'createPayment', '/payments', 'List payments']) {
      expect(text()).toContain(fragment);
    }
    // Both HTTP methods survive, so the mutating operation is distinguishable.
    expect(text().toLowerCase()).toContain('post');
    unmount(component); target.remove();
  });

  // Old surface: ConfigSection — schema path plus the effective key/value table.
  it('renders configuration values, not just the scope name', async () => {
    const { target, component, text } = await renderRevision();
    for (const fragment of ['runtime', 'configuration/schema.json', 'timeoutSeconds', '30', 'region', 'eu-west-1']) {
      expect(text()).toContain(fragment);
    }
    unmount(component); target.remove();
  });

  // Old surface: PolicySection — name, local/remote kind, definition, target.
  it('renders each policy with its definition and target', async () => {
    const { target, component, text } = await renderRevision();
    for (const fragment of ['pii-redaction', 'policy/schema.json', 'data', 'retention', 'oci://acme/policies/retention']) {
      expect(text()).toContain(fragment);
    }
    unmount(component); target.remove();
  });

  it('renders each capability with its binding, and says so when there is none', async () => {
    const { target, component, text } = await renderRevision();
    expect(text()).toContain('health');
    expect(text()).toContain('/healthz');
    expect(text()).toContain('metrics');
    expect(text()).toContain('no binding declared');
    unmount(component); target.remove();
  });

  // Old surface: DependenciesSection — the "Depends on" table had Ref, Required,
  // Compatibility and the pacto.lock pin. A list of service names is not that table.
  it('renders what a dependency DECLARES: ref, required, compatibility and pin', async () => {
    const { target, component, text } = await renderRevision();
    expect(text()).toContain('ledger');
    expect(text()).toContain('ghcr.io/acme/ledger:1.2.0');
    expect(text()).toContain('Compatibility');
    expect(text()).toContain('^1.2.0');
    expect(text()).toContain('1.2.3');
    expect(text()).toContain('fedcba98'); // the locked digest, shortened but present
    unmount(component); target.remove();
  });

  // Old surface: SbomSection. The package inventory itself is deliberately not
  // retained, so the summary carries the whole burden: the exact count and the mix.
  it('renders the software inventory summary with exact counts', async () => {
    const { target, component, text } = await renderRevision();
    expect(target.querySelector('[data-testid="revision-sbom"]')).not.toBeNull();
    expect(text()).toContain('SPDX');
    expect(text()).toContain('42');
    expect(text()).toContain('MIT');
    expect(text()).toContain('30 packages');
    expect(text()).toContain('unspecified');
    // The long tail is named rather than dropped from a chart that claims coverage.
    expect(text()).toContain('Less common licenses');
    unmount(component); target.remove();
  });

  // Old surface: OverviewSection metadata card.
  it('renders free-form contract metadata verbatim', async () => {
    const { target, component, text } = await renderRevision();
    for (const fragment of ['tier', 'gold', 'costCenter', 'CC-1234']) {
      expect(text()).toContain(fragment);
    }
    unmount(component); target.remove();
  });

  // Old surface: DocsSection (titles/paths), skills list, ValidationSection.
  it('renders docs, skills, validation findings, readiness checks and provenance', async () => {
    const { target, component, text } = await renderRevision();
    for (const fragment of [
      'docs/operations.md', 'refund-workflow.md', 'configuration schema allows additional properties',
      'runbook', 'Runbook published', 'oci', '2.1.0', 'deployment', 'stateful',
    ]) {
      expect(text()).toContain(fragment);
    }
    unmount(component); target.remove();
  });

  // Old surface: ConfigSection / PolicySection made a remote contract reference
  // NAVIGABLE. The product page rendered the ref as a copyable string and stopped
  // there, which is the loss this asserts against. The destination comes from the
  // backend's own resolution -- the raw ref is never parsed to guess it.
  it('links a resolved contract reference to the referenced service, raw ref intact', async () => {
    const { target, component, text } = await renderRevision();
    expect(text()).toContain('oci://acme/limits:1.0.0'); // the authored ref survives
    const links = Array.from(target.querySelectorAll('a[href]')).map((a) => a.getAttribute('href') || '');
    expect(links.some((h) => h.includes('domain-a%2Flimits'))).toBe(true);
    unmount(component); target.remove();
  });

  it('states an unresolved reference instead of fabricating a destination', async () => {
    const { target, component, text } = await renderRevision();
    expect(text()).toContain('oci://acme/policies/retention');
    expect(text()).toContain('Unresolved');
    expect(text()).toContain('no service in this domain publishes that contract');
    unmount(component); target.remove();
  });

  // Old surface: DocsSection rendered the document BODY as Markdown. The product page
  // listed title and path only, which made the docs unreadable in the product IA.
  it('reads a bundle document on demand and renders it as formatted content', async () => {
    docFn.mockResolvedValue({
      meta,
      revision: { kind: 'revision', key: 'domain-a/payments@sha256:abc', label: 'payments 2.1.0', href: '#' },
      document: { path: 'docs/operations.md', title: 'operations', bytes: 24, content: '# Runbook\n\nRestart the pods.' },
    });
    const { target, component } = await renderRevision();

    const doc = target.querySelector('details.rd-doc') as HTMLDetailsElement;
    expect(doc).toBeTruthy();
    doc.open = true;
    doc.dispatchEvent(new Event('toggle'));

    await vi.waitFor(() => expect(target.querySelector('.markdown-body h1')).toBeTruthy());
    // Formatted content, not raw Markdown source.
    expect(target.querySelector('.markdown-body h1')?.textContent).toBe('Runbook');
    expect(target.textContent).toContain('Restart the pods.');
    // Read by CANONICAL REVISION KEY plus the exact published path -- never by name.
    expect(docFn).toHaveBeenCalledWith('domain-a/payments@sha256:abc', 'docs/operations.md');
    unmount(component); target.remove();
  });

  it('states why a document is unavailable rather than showing an empty reading pane', async () => {
    docFn.mockRejectedValue(new Error('document "docs/operations.md" is unavailable: it exceeds the 524288-byte read bound'));
    const { target, component } = await renderRevision();

    const doc = target.querySelector('details.rd-doc') as HTMLDetailsElement;
    doc.open = true;
    doc.dispatchEvent(new Event('toggle'));

    await vi.waitFor(() => expect(target.querySelector('.rd-error')).toBeTruthy());
    expect(target.querySelector('.rd-error')?.textContent).toContain('exceeds the 524288-byte read bound');
    expect(target.querySelector('.markdown-body')).toBeNull();
    unmount(component); target.remove();
  });

  it('renders the resolved identity and the revision chronology', async () => {
    const { target, component, text } = await renderRevision();
    expect(text()).toContain('sha256:abcdef1234567890');
    expect(text()).toContain('ghcr.io/acme/payments:2.1.0');
    expect(text()).toContain('payments 2.0.0');
    expect(target.querySelector('[data-testid="revision-history-link"]')).not.toBeNull();
    unmount(component); target.remove();
  });
});

/**
 * The service page replaces the old page's fleet-level framing: how much is running,
 * how certain we are about what is running, and what is wrong RIGHT NOW. Its numbers
 * come from backend-authoritative aggregates over complete populations, never from
 * counting a bounded preview, so the aggregates themselves must reach the page.
 */
function richService(): Record<string, any> {
  return {
    meta,
    entity: { kind: 'service', key: 'domain-a/payments', label: 'payments', href: '/fleet/services/domain-a%2Fpayments', status: 'NonCompliant' },
    status: 'NonCompliant',
    actions: ['open-graph', 'compare', 'impact'],
    service: {
      domain: 'domain-a',
      ownership: { owner: 'team/payments', ref: { kind: 'owner', key: 'team/payments', label: 'team/payments', href: '#' }, conflicts: preview([]) },
      summary: {
        revisions: 7, invalidRevisions: 1, revisionsInUse: 2, targets: 5,
        compliance: { compliant: 3, nonCompliant: 1, unknown: 1, invalid: 0, other: 0 },
        links: { exact: 3, inferred: 1, ambiguous: 0, unresolved: 1 },
        findings: { errors: 2, warnings: 1, infos: 0, unknown: 0 },
        evidence: {
          withEvidence: 4, withoutEvidence: 1, stale: 1, quarantined: 0,
          oldest: '2026-07-20T08:00:00Z', newest: '2026-07-29T08:00:00Z',
        },
        declaredDependencies: 2, reconciledMatched: 1, declaredNotObserved: 1,
        observedNotDeclared: 3, unresolvedDeclared: 1,
      },
      revisions: preview([{ kind: 'revision', key: 'domain-a/payments@sha256:abc', label: 'payments 2.1.0', href: '#' }], { total: 7, count: 1, truncated: true }),
      activeRevisions: preview([{ kind: 'revision', key: 'domain-a/payments@sha256:abc', label: 'payments 2.1.0', href: '#' }]),
      deployments: preview([{ kind: 'target', key: 'prod/k8s/payments', label: 'payments', href: '#' }]),
      dependencies: preview([{ kind: 'service', key: 'domain-b/ledger', label: 'ledger', href: '#' }]),
      dependents: preview([{ kind: 'service', key: 'domain-c/checkout', label: 'checkout', href: '#' }]),
      referencedBy: preview([{ kind: 'service', key: 'domain-a/billing', label: 'billing', href: '/fleet/services/domain-a%2Fbilling' }]),
      relationships: preview([]),
      findings: preview([{ finding: { code: 'SCHEMA_DRIFT', severity: 'error', message: 'response schema drifted' }, entity: { kind: 'target', key: 'prod/k8s/payments', label: 'payments', href: '#' } }]),
      evidence: preview([{ target: { kind: 'target', key: 'prod/k8s/payments', label: 'payments', href: '#' }, at: '2026-07-29T08:00:00Z' }]),
      limitations: preview([]),
    },
  };
}

describe('service detail stays an operational dashboard (requirement 5)', () => {
  beforeEach(() => detailFn.mockReset());

  it('reports the complete populations, not the size of a bounded preview', async () => {
    detailFn.mockResolvedValue(richService());
    const { target, component } = await mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('payments'));
    const text = target.textContent || '';
    // 7 revisions exist; the preview shows 1. The page must say 7.
    expect(text).toContain('7');
    // Compliance, revision-match certainty, evidence freshness and finding severity
    // are the four distributions the old status pill collapsed into one word.
    for (const fragment of ['Compliant', 'Exact', 'Inferred', 'Unresolved', 'Stale evidence', 'Errors']) {
      expect(text).toContain(fragment);
    }
    // The concrete failure, attributed to the target it affects.
    expect(text).toContain('response schema drifted');
    expect(text).toContain('ledger');
    expect(text).toContain('checkout');
    unmount(component); target.remove();
  });

  // A configuration/policy reference is not a dependency and never enters the graph,
  // so without this section the referenced service cannot see who reaches into it --
  // the reverse of the link the revision page renders forward.
  it('names the services that reference this one, navigably', async () => {
    detailFn.mockResolvedValue(richService());
    const { target, component } = await mountView('service', 'domain-a/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('billing'));
    const links = Array.from(target.querySelectorAll('a[href]')).map((a) => a.getAttribute('href') || '');
    expect(links.some((h) => h.includes('domain-a%2Fbilling'))).toBe(true);
    unmount(component); target.remove();
  });
});

/**
 * The owner page replaces the old owner view, whose whole point was posture: an owner
 * opens it to find out whether THEIR estate is behaving. For a while it was four
 * bounded lists and no answer. It must ask the same three questions a service page
 * asks, one scope up, from the backend aggregate over the owner's complete
 * populations — never by counting the capped previews beside it.
 */
function richOwner(): Record<string, any> {
  return {
    meta,
    entity: { kind: 'owner', key: 'team/payments', label: 'team/payments', href: '/fleet/owners/team%2Fpayments' },
    actions: ['attention'],
    owner: {
      summary: {
        services: 4, revisions: 9, invalidRevisions: 1, targets: 6,
        compliance: { compliant: 3, nonCompliant: 2, unknown: 1, invalid: 0, other: 0 },
        links: { exact: 4, inferred: 1, ambiguous: 0, unresolved: 1 },
        findings: { errors: 1, warnings: 2, infos: 0, unknown: 0 },
        evidence: {
          withEvidence: 5, withoutEvidence: 1, stale: 2, quarantined: 0,
          oldest: '2026-07-20T08:00:00Z', newest: '2026-07-29T08:00:00Z',
        },
      },
      services: preview([{ kind: 'service', key: 'domain-a/payments', label: 'payments', href: '#' }], { total: 4, count: 1, truncated: true }),
      revisions: preview([{ kind: 'revision', key: 'domain-a/payments@sha256:abc', label: 'payments 2.1.0', href: '#' }], { total: 9, count: 1, truncated: true }),
      deployments: preview([{ kind: 'target', key: 'prod/k8s/payments', label: 'payments', href: '#' }], { total: 6, count: 1, truncated: true }),
      attention: preview([{ severity: 'error', category: 'compliance', entity: { kind: 'target', key: 'prod/k8s/payments', label: 'payments', href: '#' }, summary: 'response schema drifted' }]),
    },
  };
}

describe('owner detail answers the posture question (requirement 6)', () => {
  beforeEach(() => detailFn.mockReset());

  it('draws compliance, revision-match and freshness from the complete estate', async () => {
    detailFn.mockResolvedValue(richOwner());
    const { target, component } = await mountView('owner', 'team/payments');
    const text = target.textContent || '';
    // The estate, from the summary: the previews below it show one row each.
    expect(text).toContain('4');
    expect(text).toContain('9');
    expect(text).toContain('6');
    expect(text).toContain('1 invalid');
    // The same three orthogonal questions the service page asks, never collapsed
    // into one owner health score.
    for (const fragment of ['Compliant', 'Exact', 'Stale evidence', 'Errors']) {
      expect(text).toContain(fragment);
    }
    // And every bucket drills into THIS owner's backlog, not the fleet's.
    const drill = Array.from(target.querySelectorAll('a[href*="attention"]'))
      .map((a) => a.getAttribute('href') || '');
    expect(drill.length).toBeGreaterThan(0);
    expect(drill.every((h) => h.includes('owner=team%2Fpayments'))).toBe(true);
    expect(drill.some((h) => h.includes('category=non-compliant'))).toBe(true);
    unmount(component); target.remove();
  });
});

/**
 * The target page replaces ObservedRuntimeSection + SourcesPanel: what a runtime
 * source actually reported, and who reported it. It must never present service-scoped
 * context as target-scoped observation.
 */
function richTarget(): Record<string, any> {
  return {
    meta,
    entity: { kind: 'target', key: 'prod/k8s/payments', label: 'payments', href: '/fleet/targets/prod%2Fk8s%2Fpayments', status: 'NonCompliant', scope: 'prod' },
    status: 'NonCompliant',
    actions: ['open-graph', 'service'],
    target: {
      service: { kind: 'service', key: 'domain-a/payments', label: 'payments', href: '#' },
      revision: { kind: 'revision', key: 'domain-a/payments@sha256:abc', label: 'payments 2.1.0', href: '#' },
      linkState: 'exact', scope: 'prod', kind: 'Deployment', compliance: 'NonCompliant',
      coverage: { required: 10, evaluated: 7 },
      identity: { digest: 'sha256:abcdef1234567890', resolvedRef: 'ghcr.io/acme/payments@sha256:abcdef1234567890', retrievable: true, identityClass: 'exact' },
      findings: preview([{ code: 'SCHEMA_DRIFT', severity: 'error', message: 'response schema drifted' }]),
      observedRuntime: preview([{ key: 'replicas', value: '3' }, { key: 'image', value: 'ghcr.io/acme/payments@sha256:abcdef' }]),
      labels: preview([{ key: 'app.kubernetes.io/name', value: 'payments' }]),
      readiness: null,
      serviceRelationships: preview([]),
      sources: preview(['k8s', 'otel']),
      source: 'k8s',
      evidenceAt: '2026-07-29T08:00:00Z',
      reconciledAt: '2026-07-29T08:05:00Z',
      stale: false,
      ownership: { owner: 'team/payments', ref: { kind: 'owner', key: 'team/payments', label: 'team/payments', href: '#' }, conflicts: preview([]) },
      limitations: preview([]),
    },
  };
}

describe('target detail stays a runtime inspector (requirement 11)', () => {
  beforeEach(() => detailFn.mockReset());

  it('renders the observed runtime, the labels, the coverage and every contributing source', async () => {
    detailFn.mockResolvedValue(richTarget());
    const { target, component } = await mountView('target', 'prod/k8s/payments');
    await vi.waitFor(() => expect(target.textContent).toContain('payments'));
    const text = target.textContent || '';
    for (const fragment of [
      'replicas', '3', 'app.kubernetes.io/name', 'response schema drifted',
      'k8s', 'otel', 'Deployment', 'prod',
    ]) {
      expect(text).toContain(fragment);
    }
    // Coverage is the "how much of the contract could we even check" number the old
    // page carried; a compliance verdict without it overstates what was measured.
    expect(text).toContain('7');
    expect(text).toContain('10');
    unmount(component); target.remove();
  });
});
