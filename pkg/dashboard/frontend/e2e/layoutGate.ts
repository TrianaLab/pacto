import { expect, type Locator, type Page } from '@playwright/test';
import { settle } from './ready';

// G1, the transversal layout gate, and the scale fixture it runs on.
//
// `layoutGate.ts`, not `*.spec.ts`, so Playwright does not collect it. layout-matrix.spec.ts
// drives every route through it; any spec may call `layoutProblems` on a page it
// already opened.
//
// The fixture amends the real engine's answers in the page, through
// `window.__pactoServe` (the one function boot.js routes every API call through;
// Playwright's interception never sees a wasm-served fetch). It turns the 38-service
// demo into the population the spec's figures must survive: 5,000 services, 300 owners,
// 300 domains, 50 sources, 1,284 consumers (412 direct, 8 highlights, 3 hidden,
// partial), 25 providers, 60 delivery environments (waiting in the inbox and counted
// by state), a history timeline, autonomy levels and published versions in the
// thousands, and
// identities of 60 to 200 characters including an emoji and a right-to-left override.
// Keys and hrefs never change, so every link still lands.

/** Installs the scale fixture. Pass to `page.addInitScript` before the first load. */
export function installScaleFixture(): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => Promise<ServeResult>;
  let real: Serve | null = null;

  const WORDS = ['settlement', 'reconciliation', 'ledger', 'export', 'worker', 'eu', 'west', 'partner', 'feed',
    'compliance', 'archive', 'backfill', 'canary', 'bridge', 'stream', 'projection', 'read', 'model', 'consumer'];
  const hash = (s: string) => { let h = 2166136261; for (const c of s) h = Math.imul(h ^ c.codePointAt(0)!, 16777619) >>> 0; return h; };
  const memo = new Map<string, string>();
  // Deterministic per name, so the same identity is the same long name on every page.
  const longName = (s: string): string => {
    if (!s || memo.has(s)) return memo.get(s) ?? s;
    const h = hash(s);
    const target = 60 + (h % 141); // 60..200 code points
    let out = s;
    if (h % 7 === 0) out += '-\u{1F6D2}-edge';
    if (h % 11 === 0) out += '-‮reverse‬-sync';
    let k = h;
    while ([...out].length < target) { out += '-' + WORDS[k % WORDS.length]; k = (Math.imul(k, 31) + 7) >>> 0; }
    out = [...out].slice(0, target).join('').replace(/[-_.:/]+$/, '');
    memo.set(s, out);
    return out;
  };
  const ENTITY_KINDS = new Set(['service', 'revision', 'target', 'owner', 'domain', 'source', 'offering', 'external']);
  // Every entity reference carries its display label beside its key: lengthen the label,
  // keep the key. Owner names printed beside a service are lengthened the same way, and
  // so are the context bits a reference shows beside its name (its domain, its parent
  // service), except on a page's own entity, whose domain the page links by value.
  const walk = (o: unknown, field = ''): void => {
    if (Array.isArray(o)) { o.forEach((v) => walk(v)); return; }
    if (!o || typeof o !== 'object') return;
    const r = o as Record<string, unknown>;
    if (typeof r.label === 'string' && typeof r.key === 'string' && ENTITY_KINDS.has(String(r.kind))) {
      r.label = r.kind === 'revision' ? r.label : longName(r.label);
      if (field !== 'entity') {
        for (const k of ['businessDomain', 'parentService']) if (typeof r[k] === 'string') r[k] = longName(r[k] as string);
      }
    }
    if (typeof r.owner === 'string' && r.kind === 'service') r.owner = longName(r.owner);
    for (const [k, v] of Object.entries(r)) walk(v, k);
  };
  const split = (total: number, weights: number[]) => {
    const sum = weights.reduce((a, b) => a + b, 0) || 1;
    const out = weights.map((w) => Math.floor((total * w) / sum));
    out[weights.indexOf(Math.max(...weights))] += total - out.reduce((a, b) => a + b, 0);
    return out;
  };
  const scaleTally = (t: Record<string, number> | undefined, total: number) => {
    if (!t) return t;
    const keys = Object.keys(t).filter((k) => typeof t[k] === 'number');
    const parts = split(total, keys.map((k) => t[k]));
    keys.forEach((k, i) => { t[k] = parts[i]; });
    return t;
  };
  const SOURCE_COUNTS = { total: 50, available: 44, partial: 2, stale: 2, unavailable: 2 };
  const page = (doc: Record<string, unknown>, total: number) => {
    const count = Number(doc.count ?? 0), offset = Number(doc.offset ?? 0);
    doc.total = total;
    doc.truncated = offset + count < total;
    if (doc.truncated) doc.nextOffset = offset + count;
  };
  const LIST_TOTAL: Record<string, number> = { service: 5000, owner: 300, domain: 300, source: 50, offering: 2400, external: 300 };
  // The Offerings figure at fleet scale (B3): requests in the thousands across 2,400
  // offerings, the three with most failed consumers named (the backend's bound) and 37
  // more, so the failed list holds 40 offerings.
  const FAILED_OFFERINGS: [string, number][] = [[longName('load-offering-a'), 412], [longName('load-offering-b'), 97], [longName('load-offering-c'), 31]];
  const FAILED_BEYOND = 37;
  const offeringAgg = (ag: Record<string, unknown>, offerings: number) => Object.assign(ag, {
    matched: offerings, offeringCount: offerings,
    offeringPhases: { requested: 1840, provisioning: 2210, ready: 18400, failed: 1290, deleting: 120, unknown: 3300 },
    offeringPhaseConsumers: { requested: 900, provisioning: 1100, ready: 7200, failed: 640, deleting: 60, unknown: 1500 },
    failedByOffering: FAILED_OFFERINGS.map(([type, services]) => ({ type, services })), failedOfferingsBeyond: FAILED_BEYOND,
  });
  const NC_OWNERS: [string, number, number][] = [['team:load-owner-a', 40, 297], ['team:load-owner-b', 31, 60], ['team:load-owner-c', 22, 30], ['team:load-owner-d', 12, 400]];
  const NC_DOMAINS: [string, number, number][] = [[longName('load-domain-a'), 45, 310], [longName('load-domain-b'), 30, 90], [longName('load-domain-c'), 25, 40], [longName('load-domain-d'), 15, 700]];
  const NC_LISTS = new Map<string, number>([...NC_OWNERS, ...NC_DOMAINS].map(([k, n]) => [k, n] as [string, number])
    .concat([['-unidentified', 84], ['(none)', 40], ['(unknown)', 14]]));
  // The origin tally at fleet scale (B5): 5,000 services across 14 golden paths with
  // names up to 200 characters, 80 whose origin resolves to none and 3 hidden.
  const ORIGIN_PATHS: [string, number, number][] = [['load/path-a', 1800, 120], ['load/path-b', 900, 40], ['load/path-c', 320, 50]];
  const ORIGIN_LISTS = new Map<string, number>([...ORIGIN_PATHS.map(([k, n]) => [k, n] as [string, number]), ['(unresolved)', 80], ['(none)', 1300]]);

  const TRUST = 'delivery.pacto.trianalab.io/trust';
  const signerPrefix = `https://github.com/trianalab/${WORDS.join('-')}-${WORDS.join('-')}`;
  const signer = {
    issuer: 'https://token.actions.githubusercontent.com', ci: true, class: 'ci',
    subject: `${signerPrefix.slice(0, 200 - '/.github/workflows/hotfix.yml@refs/heads/hotfix'.length)}/.github/workflows/hotfix.yml@refs/heads/hotfix`,
    repository: signerPrefix.slice(0, 120),
  };
  const newSigner = (route: string, doc: Record<string, unknown>) => {
    if (route === '/api/delivery/inbox' && Array.isArray(doc.held)) {
      doc.held.unshift({ namespace: 'test', name: 'pay-agent-promote-17', stage: 'payments', service: longName('audit-log'), type: TRUST, phase: 'Held', decision: 'approve', owners: [], publisher: signer });
      const pg = doc.heldPage as { total: number } | undefined;
      if (pg) pg.total += 1; // the inbox reads its totals, not the page
    }
    if (route === '/api/delivery/operations/test/pay-agent-promote-17') {
      const e = doc.explanation as { spec: Record<string, unknown> } | undefined;
      const op = doc.operation as { spec: Record<string, unknown> } | undefined;
      for (const spec of [e?.spec, op?.spec]) if (spec) Object.assign(spec, { type: TRUST, params: { ...(spec.params as object), publisher: signer } });
    }
    const first = (doc.services as Record<string, unknown>[] | undefined)?.[0];
    if (route === '/api/delivery/published' && first) {
      first.publisher = { source: 'rule', pins: 1, pinAt: '2026-11-01T09:00:00Z', policyRef: `ghcr.io/trianalab/${longName('delivery-policy')}:latest`,
        rule: { issuer: signer.issuer, subject: `^https://github\\.com/trianalab/[^/]+/\\.github/workflows/[^@]+@refs/heads/${longName('main')}$`, repository: '^https://github\\.com/trianalab/[^/]+$' },
        verdicts: Array.from({ length: 20 }, (_, i) => ({ digest: `sha256:${String(i).padStart(64, '0')}`, version: `1.${i}.0-${longName(`rc-${i}`)}`, verdict: i % 3 ? 'new-signer' : 'accepted', source: i % 3 ? 'none' : 'pin',
          identity: signer, reason: longName(`signed by ${signer.subject}, which no publisher rule, pin or policy covers ${i}`), at: '2026-11-01T10:00:00Z' })) };
    }
  };
  const amend = (method: string, path: string, doc: Record<string, unknown>, body: string | null) => {
    const [route, query = ''] = path.split('?');
    const q = new URLSearchParams(query);
    const meta = doc.meta as Record<string, unknown> | undefined;
    if (meta && meta.sourceCounts) meta.sourceCounts = { ...SOURCE_COUNTS };
    if (route === '/api/capabilities' && statsMode() !== 'engine') doc.stats = statsMode() !== 'off';
    // 510-S9: `sessionStorage['fixture.mcp'] = 'served'` answers as a token sign-in dashboard that
    // serves /mcp (a signed-in pacto-viewer), so the Connect an agent line shows its URL and
    // snippet instead of the demo's text. `issuer` answers as an issuer-only dashboard: /mcp at
    // a fixed (long) URL, and no Kubernetes-token recipe.
    const inst = doc.installation as Record<string, unknown> | undefined;
    if (route === '/api/capabilities' && inst && mcpMode() === 'served') inst.mcp = { ...(inst.mcp as object), served: true, source: 'this dashboard, at /mcp' };
    if (route === '/api/session' && mcpMode() === 'served') Object.assign(doc, { mode: 'shared', tokenLogin: true, signedIn: true, method: 'token', user: 'system:serviceaccount:pacto:pacto-viewer' });
    if (route === '/api/capabilities' && inst && mcpMode() === 'issuer') inst.mcp = { ...(inst.mcp as object), served: true, source: ISSUER_MCP_SOURCE };
    // A narrowed list (a domain, an owner, a status, a service, an offering's consumers)
    // keeps the engine's total: the fixture leaves the tallies that link to it (an
    // offering's phases, the non-compliant rankings) alone.
    const narrowed = ['businessDomain', 'service', 'consumes', 'ownerKey', 'owner', 'status', 'ownership', 'sourceHealth', 'text', 'createdFrom', 'failed', 'lifecycle', 'gap', 'class', 'declaration', 'cluster'].some((k) => q.get(k));
    // The golden path list keeps the engine's paths: a fleet has a handful.
    if (route === '/api/fleet/entities' && Number(q.get('limit') || 25) > 1 && !narrowed && q.get('template') !== 'only') {
      const total = LIST_TOTAL[q.get('kinds') || ''];
      if (total) {
        page(doc, total);
        const ag = doc.aggregate as Record<string, unknown> | undefined;
        if (ag && q.get('kinds') === 'service') {
          ag.matched = total; ag.services = total;
          scaleTally(ag.serviceCompliance as Record<string, number>, total);
          scaleTally(ag.ownership as Record<string, number>, total);
          ag.distinctOwners = 300; ag.rankedOwners = 300;
        } else if (ag) {
          ag.matched = total;
        }
      }
    }
    // Offering rows at their longest: long owners, every availability with how Pacto
    // knows it, consumers you may not see and target counts that cannot be known.
    if (route === '/api/fleet/entities' && q.get('kinds') === 'offering') {
      const AV: [string, string][] = [['unavailable', 'inferred'], ['degraded', 'observed'], ['available', 'observed'], ['unknown', 'unknown']];
      (doc.entities as Record<string, unknown>[]).forEach((r, i) => {
        r.owner = i % 3 === 1 ? '2 owners claim it' : longName(String(r.owner || 'platform-owner'));
        [r.availability, r.availabilityProvenance] = AV[i % AV.length];
        r.lifecycle = ['deprecated', 'sunset', 'supported', 'unknown'][i % 4];
        r.consumers = 1200 + i; r.failedRequests = i % 2 ? 0 : 340 + i;
        if (i % 2) { r.hiddenConsumers = 1234; r.hiddenProviders = 12; r.targetsUnknown = true; r.targets = 0; }
      });
      const ag = doc.aggregate as Record<string, unknown> | undefined;
      // Only the bare failed list is the figure's: a failed list narrowed further keeps the engine's answer.
      if (q.get('failed') && !q.get('lifecycle') && !q.get('text')) {
        const n = FAILED_OFFERINGS.length + FAILED_BEYOND;
        // The figure's failed offerings are made up, so the engine lists none of them:
        // stand-ins fill the page and the total is what the Failed station lists.
        const ents = doc.entities as Record<string, unknown>[];
        if (!ents.length) {
          const lim = Math.min(n, Number(q.get('limit') || 25));
          doc.entities = Array.from({ length: lim }, (_, i) => ({ kind: 'offering', key: `load-failed-${i}`, label: longName(`load-failed-${i}`), offering: `load-failed-${i}`,
            owner: longName(`owner-${i}`), services: 1, consumers: 900 + i, failedRequests: 20 + i, lifecycle: 'deprecated', availability: 'degraded', availabilityProvenance: 'inferred', href: `/fleet/offerings/load-failed-${i}` }));
          doc.count = lim;
        }
        page(doc, n);
        if (ag) offeringAgg(ag, n);
      } else if (ag && !narrowed && Number(q.get('limit') || 25) > 1) {
        offeringAgg(ag, LIST_TOTAL.offering);
      }
    }
    // The non-compliant rankings at fleet scale (389 of 5,000 across 300 owners and
    // domains, names up to 200 characters), and every list a lane links to holds exactly
    // what the lane counts.
    if (route === '/api/fleet/entities' && q.get('kinds') === 'service') {
      const ag = doc.aggregate as Record<string, unknown> | undefined;
      if (ag && q.get('limit') === '1' && !narrowed) {
        scaleTally(ag.ownership as Record<string, number>, 5000);
        Object.assign(ag, {
          services: 5000, matched: 5000, serviceCompliance: { ...(ag.serviceCompliance as object), nonCompliant: 389 },
          nonCompliantByOwner: NC_OWNERS.map(([key, n, size]) => ({ key, label: longName(key.slice(5)), kind: 'team', services: size, nonCompliant: n })),
          nonCompliantBeyondRanking: 389 - 105 - 84, nonCompliantUnidentified: 84, nonCompliantOwners: 120,
          nonCompliantByDomain: NC_DOMAINS.map(([domain, n, size]) => ({ domain, services: size, nonCompliant: n })),
          nonCompliantDomainsBeyond: 389 - 115 - 40 - 14, nonCompliantDomains: 140, nonCompliantNoDomain: 40, nonCompliantDomainUnknown: 14,
          origins: {
            byPath: ORIGIN_PATHS.map(([key, n, nc]) => ({ key, label: longName(key.slice(5)), services: n, nonCompliant: nc, hidden: key.endsWith('a') ? 3 : 0 })),
            paths: 14, beyond: { services: 600, nonCompliant: 30 }, unresolved: { services: 80, nonCompliant: 9 }, none: { services: 1300, nonCompliant: 140 }, hidden: 3,
          },
        });
      }
      const from = q.get('createdFrom') || '';
      const listed = !q.get('status') && ORIGIN_LISTS.get(from);
      if (listed) {
        // The made-up paths created nothing the engine lists: stand-ins fill the page.
        const ents = doc.entities as Record<string, unknown>[];
        if (!ents.length && q.get('limit') !== '1') {
          const lim = Math.min(listed, Number(q.get('limit') || 25));
          doc.entities = Array.from({ length: lim }, (_, i) => ({ kind: 'service', key: `load/from-${i}`, label: `from-${i}`, status: 'Compliant', href: `/fleet/services/load%2Ffrom-${i}` }));
          doc.count = lim;
        }
        if (q.get('limit') !== '1') page(doc, listed);
      }
      if (q.get('status') === 'NonCompliant') {
        const n = NC_LISTS.get(q.get('ownerKey') || q.get('businessDomain') || (q.get('ownership') === 'unidentified' ? '-unidentified' : ''));
        if (n != null) {
          // The ranking's groups are made up, so the engine lists none of their services:
          // the page is filled with stand-ins and counts what the lane counted.
          const ents = doc.entities as Record<string, unknown>[];
          if (!ents.length) {
            const lim = Math.min(n, Number(q.get('limit') || 25));
            doc.entities = Array.from({ length: lim }, (_, i) => ({ kind: 'service', key: `load/nc-${i}`, label: `nc-${i}`, status: 'NonCompliant', href: `/fleet/services/load%2Fnc-${i}` }));
            doc.count = lim;
          }
          page(doc, n);
        }
      }
    }
    // The inferred population at fleet scale (509-S1): 12,400 workloads no contract
    // describes, each row's image a digest reference with no break point.
    if (route === '/api/fleet/entities' && q.get('declaration') === 'inferred' && Number(q.get('limit') || 25) > 1
      && !['text', 'ownerKey', 'owner', 'status', 'ownership', 'gap', 'lifecycle', 'businessDomain'].some((k) => q.get(k))) {
      page(doc, 12400);
      const ag = doc.aggregate as Record<string, unknown> | undefined;
      if (ag) { ag.matched = 12400; ag.services = 0; ag.inferredServices = 12400; }
      for (const r of (doc.entities as Record<string, unknown>[] | undefined) ?? []) {
        if (r.declaration !== 'inferred') continue;
        r.image = `registry.example.com/${longName(`inferred-image-${String(r.key)}`).replace(/[^a-z0-9-]/g, '')}@sha256:${'9f8e'.repeat(16)}`;
        // 509-S3: a 200-character group label and a 300-character repository URL.
        const g = (r.group ?? { rule: 'argocd', namespace: 'shop', name: 'shop', from: 'annotation argocd.argoproj.io/tracking-id', key: 'argocd/shop/shop' }) as Record<string, unknown>;
        r.group = { ...g, label: `Argo CD Application ${longName(`group-${String(r.key)}`)}`.padEnd(200, 'x').slice(0, 200) };
        r.repository = `https://github.com/${longName(`repository-${String(r.key)}`)}`.padEnd(300, 'y').slice(0, 300);
      }
    }
    // Detected providers at scale (509-S2): 30 rows from 4 sources in 3 clusters, long
    // names and evidence, 20 objects, two partial rows with reasons, and 6 Kubernetes
    // sources Pacto could not read, with ids and errors of 60 to 200 characters.
    if (route === '/api/fleet/providers') {
      const clusters = ['prod', 'staging', 'edge'].map((c) => longName(`cluster-${c}`));
      doc.providers = Array.from({ length: 30 }, (_, i) => ({
        id: `provider-${i}`, source: `k8s-${i % 4}`, cluster: clusters[i % 3], name: longName(`provider-name-${i}`),
        evidence: longName(`evidence-${i}-served-pacto-trianalab-io-v1alpha1`), usedFor: ['inferred-services', 'declared-targets'],
        status: i < 2 ? 'partial' : 'available',
        objects: Array.from({ length: 20 }, (_, j) => ({ kind: 'Deployment', namespace: `ns-${j}`, name: longName(`object-${i}-${j}`) })),
        objectsTotal: 40,
        limitations: i < 2 ? [{ code: 'PROVIDER_SOURCE_DEGRADED', source: `k8s-${i % 4}`, message: longName(`source-k8s-${i}-is-partial-the-counts-cover-only-what-it-read`) }] : undefined,
      }));
      // The first row carries an offer (516-S6) whose text runs to 200 characters, so the
      // line has to wrap under the row at every width, its Set up link included.
      Object.assign((doc.providers as Record<string, unknown>[])[0], { offers: [{ id: 'argocd-repo-server-plugin',
        text: `Gate every Argo CD sync with one repo-server plugin ${longName('offer')}`.padEnd(200, 'o').slice(0, 200),
        doc: 'https://pacto.run/latest/integrations/kubernetes/gitops/#gate-every-argo-cd-sync' }] });
      // Six of them the operator inferred (510-S5): 200-character values, five details,
      // a long hint, and one redacted for a viewer who cannot read its namespace.
      const pad = (s: string) => (s + '-' + 'x'.repeat(200)).slice(0, 200);
      for (let i = 2; i < 8; i++) {
        const r = (doc.providers as Record<string, unknown>[])[i];
        Object.assign(r, { source: 'cluster-detection', provenance: 'inferred', asOf: new Date(Date.now() - 4 * 60_000).toISOString(), status: i === 3 ? 'partial' : 'available',
          evidence: pad(`registry-${i}.example.com/${longName(`team-${i}`)} in 9 of 11 namespaces, 87% of workloads`),
          details: Array.from({ length: 5 }, (_, j) => pad(`candidate-${j}.example.com/${longName(`detail-${i}-${j}`)}`)),
          hint: `--set dashboard.httpRoute.enabled=true --set dashboard.httpRoute.parentRefs[0].name=${longName(`gateway-${i}`)} --set dashboard.httpRoute.parentRefs[0].namespace=${longName(`infra-${i}`)}` });
        if (i === 7) Object.assign(r, { evidence: 'Registry found in a namespace you cannot read', hint: undefined, details: undefined, objects: undefined, objectsTotal: 3 });
      }
      // The operator's declared Installation row (510-S6), with no scope or objects and
      // its worst case in details: three 256-rune refs, eight 256-rune push prefixes, a
      // 64-character key id and a last push to a cluster-uid tag, and its inferred
      // Pull Secret proposal with a 253-character <namespace>/<name> and a long hint.
      const runes = (s: string) => (s + '/' + 'r'.repeat(256)).slice(0, 256);
      const secret = `${'n'.repeat(63)}/${'s'.repeat(189)}`;
      (doc.providers as Record<string, unknown>[]).push({
        id: 'installation', source: 'cluster-detection', provenance: 'declared', asOf: new Date(Date.now() - 4 * 60_000).toISOString(), name: 'Installation',
        evidence: `profile hardened, idp; registry ${runes('ghcr.io/acme')}; tags only under the listed prefixes, referrers by digest elsewhere`, status: 'available', cluster: clusters[0],
        usedFor: ['installation profile, target snapshot, organization index and inferred contracts; set by a person with the profile and registry values'],
        details: [[`target snapshot ${runes('ghcr.io/acme/pacto-targets')}`, `organization index ${runes('ghcr.io/acme/pacto-index')}`,
          `inferred contracts ${runes('ghcr.io/acme/pacto-inferred')}`].join('; '),
          `push prefixes ${Array.from({ length: 8 }, (_, i) => runes(`quay.io/stats-${i}/pacto`)).join(', ')}`, `snapshot key ${'f'.repeat(64)}`,
          `last push ${runes('ghcr.io/acme/pacto-targets')}:cluster-5b0c6c3e-1f2a-4c3d-9e8f-0a1b2c3d4e5f at 2026-11-16T09:00:00Z`],
      });
      (doc.providers as Record<string, unknown>[]).push({
        id: 'pull-secret', source: 'cluster-detection', provenance: 'inferred', asOf: new Date(Date.now() - 4 * 60_000).toISOString(), name: 'Pull Secret',
        evidence: `${secret} named in the pod specs of 4 running workloads that pulled from ghcr.io; Pacto did not read the Secret`,
        status: 'available', cluster: clusters[0], objects: [{ kind: 'Namespace', namespace: 'n'.repeat(63), name: 'n'.repeat(63) }],
        usedFor: ['proposal for registryPullSecret: it needs push on <registry>/pacto-*; a pod pull Secret usually grants pull only'],
        hint: `--set registryPullSecret=${secret}`,
      });
      doc.total = 32;
      const meta = doc.meta as Record<string, unknown>;
      meta.sources = [...((meta.sources as unknown[] | undefined) ?? []), ...Array.from({ length: 6 }, (_, i) => ({
        id: longName(`unreadable-cluster-${i}`), kind: 'kubernetes', status: 'unavailable', revisionCount: 0, targetCount: 0,
        error: { code: 'Unavailable', message: longName(`dial-tcp-connection-refused-while-reading-cluster-${i}`) },
      }))];
    }
    if (route === '/api/fleet/overview') {
      const s = doc.summary as Record<string, unknown>;
      s.inferredServices = 12400; s.inferredUnverified = 310;
      s.services = 5000; s.servicesNeedingAttention = 389; s.domains = 300;
      s.externals = 300; s.hiddenExternals = 12;
      s.ownership = { consistent: 3120, conflicting: 640, unowned: 1060, unknown: 180 };
      s.degradedSources = 2; s.staleSources = 2; s.unavailableSources = 2;
      // The health and knowledge bands and the domain table at fleet scale: every
      // count four digits, every gap kind present and long domain names.
      Object.assign(s, {
        targets: 9000, compliantTargets: 6100, staleCompliantTargets: 1240, nonCompliantTargets: 1200, unknownTargets: 1700,
        warningTargets: 0, invalidTargets: 0, referenceTargets: 0, notEvaluatedTargets: 0, otherComplianceTargets: 0,
        exactTargetLinks: 7000, inferredTargetLinks: 1200, ambiguousTargetLinks: 500, unresolvedTargetLinks: 300,
        evidence: { withEvidence: 8100, withoutEvidence: 900, stale: 1500 },
      });
      s.operationalHealth = { available: 5200, degraded: 0, unavailable: 1180, unknown: 1820, notObserved: 800 };
      s.knowledge = { services: 5000, described: 1100, withGaps: 3400, unknown: 500,
        gaps: { noSummary: 2900, noDomain: 1400, noInterfaceDocs: 1200, noReadiness: 3100, notDeployed: 1050, unverifiedCapability: 1010 } };
      s.coverage = { evaluated: 81200, required: 96400, reporting: 7900, notReporting: 1100 };
      const row = { targets: 2100, nonCompliant: 1300, failingHealth: 1100, unknown: 1400, stale: 1250, notDeployed: 1020, withGaps: 1900, unowned: 1080, ownerUnknown: 1010 };
      s.byDomain = [...NC_DOMAINS.map(([domain], i) => ({ ...row, domain, services: 1200 - i * 100 })),
        { ...row, domain: '', domainUnknown: true, services: 1400 }, { ...row, domain: '', services: 1100 }];
      const at = doc.attention as Record<string, unknown>;
      if (at) { at.total = 2400; at.actionable = 1900; }
      // 'overview watched namespaces': a Kubernetes source reading 12 long namespaces,
      // one of which it could not read. Behind a flag so no other route moves.
      if ((window as unknown as { __scoped?: boolean }).__scoped && meta) {
        // The first is a legal 63-character DNS label with no hyphen: no break point of its own.
        const ns = Array.from({ length: 12 }, (_, i) => (i === 0 ? 'paymentsproductionnortheuropewestonealphabravocharliedeltaecho1' : longName(`watched-namespace-${i}`)));
        meta.sources = [{ id: 'k8s-scoped', kind: 'kubernetes', status: 'partial', revisionCount: 0, targetCount: 0, namespaces: ns, namespacesTotal: 12 },
          ...((meta.sources as unknown[] | undefined) ?? [])];
        meta.limitations = [
          { code: 'K8S_NAMESPACE_SCOPED', source: 'k8s-scoped', message: `source k8s-scoped watches 12 namespaces (${ns.slice(0, 3).join(', ')} and 9 more); services in any other namespace are unknown` },
          { code: 'K8S_NAMESPACE_UNREADABLE', source: 'k8s-scoped', message: `source k8s-scoped could not read namespace ${longName('unreadable-namespace')}: forbidden` },
          ...((meta.limitations as unknown[] | undefined) ?? [])];
      }
    }
    if (route === '/api/fleet/attention') {
      page(doc, 2400);
      scaleTally(doc.severities as Record<string, number>, 2400);
      // Every category holds items, so each bar and its label are drawn at scale.
      const cats = doc.categories as { count: number }[] | undefined;
      if (cats) { const parts = split(2400, cats.map((c) => c.count || 1)); cats.forEach((c, i) => { c.count = parts[i] || 1; }); }
      // Each item's cause and next action at their longest: the code chip, the reason and
      // the words of #472's nextAction must wrap inside the row.
      for (const it of (doc.items as Record<string, unknown>[] | undefined) ?? []) {
        it.code = `${String(it.code || 'UNKNOWN')}_${'OBSERVATION_UNSUPPORTED_'.repeat(3)}CAUSE`;
        it.reason = longName(String(it.reason || 'reason'));
        it.nextAction = { verb: 'edit-contract', tool: 'pacto_edit', environment: longName('production-environment') };
        // An inferred caller's name holds ':' and '/' with no break point; walk lengthens its label.
        const caller = it.caller as Record<string, unknown> | undefined;
        if (caller) caller.label = `${String(caller.label)}-${'reconciliation/'.repeat(8)}`;
      }
    }
    // Owner and domain rows with every count four digits.
    if (route === '/api/fleet/entities' && ['owner', 'domain'].includes(q.get('kinds') || '')) {
      (doc.entities as Record<string, unknown>[]).forEach((r, i) => Object.assign(r, { unknown: 1400 + i, stale: 1250 + i, failingHealth: 1100 + i, withGaps: 1900 + i }));
    }
    // The owner and domain pages: their health, knowledge and facts line at fleet scale.
    if (route === '/api/fleet/entities/owner' || route === '/api/fleet/entities/domain') {
      const sum = ((doc.owner ?? doc.domain) as Record<string, unknown> | undefined)?.summary as Record<string, unknown> | undefined;
      if (sum) {
        Object.assign(sum, {
          targets: 9000, health: { available: 5200, degraded: 0, unavailable: 1180, unknown: 1820, notObserved: 800 },
          knowledge: { services: 5000, described: 1100, withGaps: 3400, unknown: 500,
            gaps: { noSummary: 2900, noDomain: 1400, noInterfaceDocs: 1200, noReadiness: 3100, notDeployed: 1050, unverifiedCapability: 1010 } },
          notDeployed: 1050, readiness: { passing: 2100, belowThreshold: 1300, expired: 1100, notDeclared: 3100 },
          unresolvedDependencies: 1200, undeclaredDependencies: 1400,
        });
      }
    }
    // The external list's hidden line: systems only services you cannot see declare.
    if (route === '/api/fleet/entities' && q.get('kinds') === 'external' && !narrowed) doc.hidden = 12;
    // 'external entity name only, hidden, folded and truncated': the states a demo
    // external (every one catalog-joined) never reaches. Behind a flag so the plain
    // external entity route keeps the engine's answer.
    if (route === '/api/fleet/entities/external' && (window as unknown as { __externalStates?: boolean }).__externalStates) {
      const x = doc.external as Record<string, unknown>;
      const rec = x.external as Record<string, unknown>;
      const c0 = (x.consumers as Record<string, unknown>[])[0];
      const svc = c0.service as Record<string, unknown>;
      x.consumers = Array.from({ length: 50 }, (_, i) => ({ ...c0, service: { ...svc, key: `${svc.key}-c${i}`, label: longName(`consumer-${i}`), href: `${svc.href}-c${i}` } }));
      delete x.catalog; delete rec.catalog;
      Object.assign(rec, { class: 'unclassified', joinedBy: 'normalized-name', consumersTotal: 120, sourcesTotal: 140, hiddenConsumers: 3 });
      x.foldedInto = 'other-egress';
    }
    // 'external entity, inferred contract' (517-S3): the provider block at its longest, a
    // 200-character managed-cloud identity, operator-signed, from a partial run with long
    // limitation codes, with 2,000 hosts, ports and clients (past the 12-row cap), each
    // with two sources.
    if (route === '/api/fleet/entities/external' && (window as unknown as { __inferredContract?: boolean }).__inferredContract) {
      const rec = (doc.external as Record<string, unknown>).external as Record<string, unknown>;
      const id = `aws/rds/123456789012/eu-west-1/${'orders-db-replica-'.repeat(12)}`.slice(0, 200).replace(/-+$/, '');
      const seen = '2026-07-29T12:00:00Z';
      const fifty = (f: (i: number) => string) => Array.from({ length: 2000 }, (_, i) => ({ value: f(i), sources: ['k8s-production', longName(`inferred-source-${i}`)], firstSeen: seen, lastSeen: seen }));
      rec.provider = {
        repository: `registry.example.com/acme/pacto-inferred/${id}`, revisionsTotal: 120, rangeStatus: 'unknown', rangeReason: 'INFERRED_PROVIDER',
        revisions: [{
          version: '0.0.1234', digest: `sha256:${'9f8e'.repeat(16)}`, ref: `registry.example.com/acme/pacto-inferred/${id}@sha256:${'9f8e'.repeat(16)}`,
          signature: { status: 'unverified', source: 'operator', bundles: 1 },
          surface: { identity: id, class: 'managed-cloud', asOf: seen, completeness: { state: 'partial', limitations: ['EXTERNALS_UNREPORTED', 'TARGET_SNAPSHOT_SCOPED', longName('SOURCE_LIMITATION_CODE').toUpperCase()] },
            hosts: fifty((i) => `${longName(`inferred-host-${i}`).replace(/[^a-z0-9-]/g, '')}.eu-west-1.rds.amazonaws.com:5432`),
            ports: fifty((i) => String(5000 + i)), clients: fifty((i) => `@aws-sdk/${longName(`client-${i}`).replace(/[^a-z0-9-]/g, '')}/3`) },
        }],
      };
    }
    // 509-S3: an inferred service's Group and Repository facts at their longest.
    const groupWorkload = (doc.service as Record<string, Record<string, unknown>> | undefined)?.workload;
    if (route === '/api/fleet/entities/service' && groupWorkload && (window as unknown as { __groupLongValues?: boolean }).__groupLongValues) {
      const long = (seed: string, n: number) => `${seed}-${'segment/'.repeat(Math.ceil(n / 8))}`.slice(0, n);
      groupWorkload.group = { ...(groupWorkload.group as Record<string, unknown>), label: long('Argo CD Application shop', 200), from: long('annotation argocd.argoproj.io/tracking-id', 300) };
      groupWorkload.repositories = [0, 1, 2, 3, 4].map((i) => ({ url: long(`https://github.com/example/${i}`, 300), path: long(`deploy/${i}`, 300),
        from: long(`Application argocd/shop spec.sources[${i}].repoURL`, 300), namespace: 'argocd', kind: 'Application' }));
      groupWorkload.repositoryUnknown = long('the source is Helm chart', 300);
    }
    if (route === '/api/fleet/entities/offering') {
      const off = doc.offering as Record<string, Record<string, unknown>>;
      const prov = off?.providers as { items: Record<string, unknown>[]; total: number; count: number; truncated: boolean } | undefined;
      if (prov && prov.items.length) {
        const base = prov.items[0];
        const svc = base.service as Record<string, unknown>;
        prov.items = Array.from({ length: Math.min(10, 25) }, (_, i) => i === 0 ? base : ({
          ...base, service: { ...svc, key: `${svc.key}-p${i}`, label: `${svc.label}-provider-${i}`, href: `${svc.href}-p${i}` },
        }));
        prov.total = 25; prov.count = prov.items.length; prov.truncated = true;
      }
      // Availability at its longest (#464): long environment names, five unreachable
      // reporters with long keys, a partial environment and a long successor ref.
      const unreachable = Array.from({ length: 5 }, (_, u) => {
        const key = `domain-load/${longName(`unreachable-${u}`)}`;
        return { kind: 'service', key, label: longName(`unreachable-${u}`), href: `/fleet/services/${encodeURIComponent(key)}` };
      });
      const states = ['available', 'degraded', 'unavailable', 'unknown'];
      const avail = (seed: string) => ({
        state: 'degraded', asOf: '2026-11-10T08:00:00Z', total: 6,
        byEnvironment: states.map((state, e) => ({
          environment: longName(`${seed}-production-${e}`), state, provenance: ['observed', 'inferred', 'inferred', 'unknown'][e],
          reason: longName(`${e} of 4 targets reporting; ${unreachable[0].label} cannot reach it`), reporting: e, targets: 4,
          evidenceAt: '2026-11-10T07:00:00Z', partial: e === 1, unreachable: e ? unreachable : undefined,
          providers: (prov?.items ?? []).slice(0, 3).map((p) => p.service),
        })),
      });
      if (off) {
        off.availability = avail('offering') as unknown as Record<string, unknown>;
        (off as Record<string, unknown>).hiddenProviders = 2;
      }
      for (const [i, p] of (prov?.items ?? []).entries()) {
        p.availability = avail(`provider-${i}`);
        p.successor = { provenance: i % 2 ? 'unknown' : 'inferred', ref: `oci://ghcr.io/${longName('successor-registry-namespace')}/${longName('successor')}:17.0.0`, reason: i % 2 ? longName('no revision 17.0.0 in this snapshot') : undefined };
      }
      const pv = (prov?.items ?? [])[0]?.service as Record<string, unknown> | undefined;
      if (off && pv) {
        off.observedConsumers = {
          items: Array.from({ length: 30 }, (_, o) => o % 3 === 2
            ? { caller: longName(`uncontracted-caller-${o}`), provider: pv, provenance: 'observed', uncontracted: true }
            : { consumer: { kind: 'service', key: `domain-load/observed-${o}`, label: longName(`observed-caller-${o}`), href: `/fleet/services/${encodeURIComponent(`domain-load/observed-${o}`)}` },
              provider: pv, provenance: 'observed', lastSeen: '2026-11-08T08:00:00Z', stale: o % 2 === 0, sources: [longName('mira-observation-source')] }),
          total: 412, count: 30, truncated: true,
        };
        (off as Record<string, unknown>).hiddenObservedConsumers = 7;
      }
      // Adapters at scale (#463), guarded like the providers: an offering with no
      // adapter keeps its real empty state. One item has no slug ("unnamed adapter").
      if ((off?.adapters as { items?: unknown[] } | undefined)?.items?.length) {
        off.adapters = {
          total: 40, count: 10, truncated: true, unattributed: 3200, conflicting: 400,
          items: Array.from({ length: 10 }, (_, i) => ({
            provider: i === 9 ? '' : longName(`scanner-${i}`), producer: longName(`security-adapter-${i}`), verified: i % 2 === 0,
            environments: Array.from({ length: 12 }, (_, e) => longName(`prod-${e}`)),
            phases: { requested: 1200, provisioning: 30, ready: 2400, readyUnsigned: i % 2 === 0 ? 0 : 2400, failed: 7, deleting: 2, unknown: 900 },
            lastReportedAt: '2026-11-13T09:00:00Z', provenance: 'observed', disagrees: i === 3 ? 400 : 0,
          })),
        };
      }
      if (((off?.unresolvedUses as { count?: number } | undefined)?.count ?? 0) > 0) {
        off.unresolvedUses = { count: 4000, reason: longName('the reference is not digest-pinned or locked'), provenance: 'inferred' };
      }
      // The unsigned share of every Ready tally, so the stations read it at scale.
      const cons = off?.consumers as { phases?: Record<string, number>; phaseConsumers?: Record<string, number>; items?: Record<string, unknown>[] } | undefined;
      // Every fourth dependency consumer, the first included, declares no range, so its
      // badges read "Range not declared".
      let deps = 0;
      // The engine grades a successor or supported range from the declared range, so
      // with none it grades those unknown too.
      for (const u of cons?.items ?? []) {
        if (u.via !== 'dependency' || deps++ % 4 !== 0) continue;
        u.compatibility = 'unknown'; u.rangeStatus = 'unknown';
        for (const k of ['successorRange', 'supportedRange']) if (u[k]) u[k] = 'unknown';
      }
      for (const t of [cons?.phases, cons?.phaseConsumers]) if (t?.ready) t.readyUnsigned = Math.floor(t.ready / 2);
    }
    // A checked range's answer carries the provider's successor at its longest ref.
    if (route === '/api/fleet/versions') {
      for (const pr of (doc.providers as { versions?: Record<string, unknown> }[] | undefined) ?? []) {
        if (pr.versions && pr.versions.range) {
          pr.versions.successor = { provenance: 'unknown', ref: `oci://ghcr.io/${longName('successor-registry-namespace')}/${longName('successor')}:17.0.0`, reason: longName('no revision 17.0.0 in this snapshot') };
          pr.versions.successorRange = 'unknown';
        }
      }
    }
    // A new publisher waiting for a person (511-S3), on the routes that set
    // __pactoNewSigner: a CI signer whose subject is 200 characters long, the held trust
    // change it raises (the demo's held change, so its link lands) and a publisher line
    // with 20 verdicts.
    if (flags.__pactoNewSigner) newSigner(route, doc);
    // The inbox figure at fleet scale: 60 environments with long names, a declared chain
    // through some of them and counts in the thousands. Only the figure's tallies change;
    // the lists stay the engine's, and the figure caps itself at seven stations.
    if (route === '/api/delivery/inbox') {
      doc.byEnvironment = Array.from({ length: 60 }, (_, i) => {
        const environment = longName(`prod-${i}`);
        return { environment, errored: (i * 37) % 1300, held: (i * 53) % 900, frozen: i % 4, upstream: i ? [longName(`prod-${i - 1}`)] : [] };
      });
      // A frozen row's unfreeze offer at its longest reason.
      for (const r of (doc.frozen as Record<string, unknown>[] | undefined) ?? []) {
        for (const o of (r.offers as Record<string, unknown>[] | undefined) ?? []) o.reason = longName(String(o.reason ?? 'offer'));
      }
    }
    // The environments figure at fleet scale: 60 environments with long names and a
    // declared chain through them, Stages in the thousands; the board stays the engine's.
    if (route === '/api/delivery/environments') {
      const counts = doc.counts as Record<string, unknown> | undefined;
      if (counts) {
        counts.byEnvironment = Array.from({ length: 60 }, (_, i) => ({
          environment: longName(`prod-${i}`), tier: 'prod', stages: 900, failed: (i * 37) % 120, frozen: i % 4, autoHold: i % 3, waiting: (i * 53) % 90,
          unverified: 40, empty: 2, verified: 900 - 40 - 2 - ((i * 37) % 120) - (i % 4) - (i % 3) - ((i * 53) % 90),
          upstream: i ? [longName(`prod-${i - 1}`)] : [],
        }));
      }
      // The Stage rows' free text at its longest (the service page reads it per row);
      // keys, digests and hrefs stay the engine's.
      // Every row names its applier at its longest; every third one conflicts and the second
      // reads unknown and stale, so the warning line is checked at scale too.
      const fresh = new Date().toISOString().replace(/\.\d+Z$/, 'Z');
      for (const [i, r] of ((doc.environments as Record<string, unknown>[] | undefined) ?? []).entries()) {
        const state = i % 3 === 2 ? 'Conflict' : i === 1 ? 'Unknown' : 'Adopted';
        r.applier = { manager: state === 'Unknown' ? 'unsupported' : 'argocd', owner: longName(`Application ${String(r.namespace)}/${String(r.service)}`),
          evidence: 'tracking annotation', display: longName(`Argo CD (Application ${String(r.service)}, tracking annotation)`), state,
          message: state === 'Adopted' ? '' : longName(`${state} manager owns Deployment ${String(r.service)}`), completeness: 'complete',
          asOf: state === 'Unknown' ? '2026-11-08T08:00:00Z' : fresh };
        if (q.get('service') && longName(String(r.service)) === q.get('service')) r.service = q.get('service');
        if (flags.__pactoLongPropose) {
          for (const k of ['namespace', 'stage', 'candidateVersion']) if (typeof r[k] === 'string' && r[k]) r[k] = longName(r[k] as string);
        }
        for (const o of (r.offers as Record<string, unknown>[] | undefined) ?? []) o.reason = longName(String(o.reason ?? 'offer'));
        for (const k of ['readyFix', 'readyMessage', 'sourceRevision', 'contractRef']) if (typeof r[k] === 'string') r[k] = longName(r[k] as string);
        const lc = r.lastCheck as { verdicts?: Record<string, unknown>[] } | undefined;
        for (const v of lc?.verdicts ?? []) v.message = longName(String(v.message ?? v.gate));
      }
    }
    // The Why card at its longest: many reasons, each with a long message and fix.
    if (route === '/api/delivery/progress') {
      const base = (doc.reasons as Record<string, unknown>[] | null) ?? [];
      doc.summary = longName(String(doc.summary ?? 'summary'));
      doc.reasons = Array.from({ length: 12 }, (_, i) => ({ ...(base[i % Math.max(base.length, 1)] ?? { code: 'NotReady' }),
        message: longName(`reason-${i}`), fix: i % 2 ? `pacto delivery approve -n ${longName(`ns-${i}`)} op-${i}` : longName(`fix-${i}`) }));
      if (typeof doc.autoPromote === 'string') doc.autoPromote = longName(doc.autoPromote || 'auto');
    }
    // A service's environments: long environment names, owners, reasons and version
    // labels (keys and hrefs stay the engine's). storefront-web holds 200 environments
    // and 200 differences, so the section is drawn at fleet scale. The plain and the
    // reached=true request both answer here (matched on the pathname).
    if (route === '/api/fleet/entities/service') {
      const svc = doc.service as Record<string, unknown> | undefined;
      const pl = svc?.placement as Record<string, unknown> & { items?: Record<string, unknown>[] } | undefined;
      const changes = svc?.changes as Record<string, unknown> & { items?: Record<string, unknown>[] } | undefined;
      const entityKey = (doc.entity as { key?: string } | undefined)?.key;
      // One dependency the service declares with no range (compatibility: unknown): the
      // page shows the claims of the edges that leave it.
      const firstRel = (svc?.relationships as { items?: { from?: { key?: string }; declaredClaims?: { items?: Record<string, unknown>[] } }[] } | undefined)?.items
        ?.find((e) => e.from?.key === entityKey && e.declaredClaims?.items?.length);
      for (const c of firstRel?.declaredClaims?.items ?? []) { c.compatibility = 'unknown'; c.rangeStatus = 'unknown'; }
      if (pl && entityKey === 'storefront-web') {
        const tpl = (pl.items ?? []).find((it) => it.declaration) ?? {};
        const names = Array.from({ length: 200 }, (_, i) => `env-${i}`);
        pl.items = names.map((n, i) => ({ ...tpl, environment: n, targetsComplete: i % 4 !== 0, targetsReason: i % 4 ? undefined : 'reason-targets',
          provisioningTracked: i % 5 !== 0, provisioningReason: i % 5 ? undefined : `reason-provisioning-${i}`,
          declaration: i % 7 === 0 ? undefined : { ...(tpl.declaration as Record<string, unknown>), name: n, lifetime: ['temporary', 'persistent', 'not-declared'][i % 3],
            expires: i % 3 === 0 ? '2026-09-30T00:00:00Z' : undefined, expired: i % 6 === 0, owner: i % 2 ? { team: `owner-${i}` } : undefined },
          owner: i % 2 ? { provenance: 'declared', owner: `owner-${i}` } : { provenance: 'inferred', owner: 'service-owner', reason: 'reason-owner' } }));
        pl.total = 200; pl.count = 200; pl.truncated = false;
        pl.differences = Array.from({ length: 200 }, (_, i) => ({ configuration: `config-${i % 5}`, path: `spec.path-${i}`,
          values: Object.fromEntries(names.slice(i % 190, (i % 190) + 4).map((n, j) => [n, j ? `value-${i}-${j}` : { nested: `value-${i}` }])) }));
        pl.differencesTotal = 240;
      }
      // Changes at scale (#459): 200 events across every kind with long subjects and
      // values, three reached Stages each with long names, three gaps, partial, and
      // capped: 350 held, the 200 newest shown.
      if (changes && entityKey === 'storefront-web') {
        const first = (changes.items ?? [])[0];
        const rev = first?.revision ?? pl?.currentRevision;
        const prev = first?.previous ?? rev;
        const kinds = ['owner', 'domain', 'offering', 'dependency', 'lifecycle', 'environment', 'finding'];
        changes.items = Array.from({ length: 200 }, (_, i) => ({ kind: kinds[i % 7], change: ['added', 'removed', 'changed', 'present'][i % 4],
          subject: i % 7 >= 3 ? longName(`subject-${i}`) : undefined, fields: i % 4 === 2 ? ['file', 'lifetime', 'expires', 'owner'] : undefined,
          from: i % 4 === 3 ? undefined : longName(`from-value-${i}`), to: longName(`to-value-${i}`), revision: rev, previous: i % 4 === 3 ? undefined : prev,
          provenance: 'declared', source: i % 7 === 6 ? 'validation' : 'contract',
          reached: [0, 1, 2].map((j) => ({ namespace: longName(`namespace-${j}`), stage: longName(`stage-${j}`), environment: longName(`environment-${j}`),
            at: '2026-11-10T14:02:00Z', record: j === 2 ? undefined : `rec-${i}-${j}`, verified: j === 0, fromStatus: j === 2 })) }));
        Object.assign(changes, { total: 350, count: 200, truncated: true, gaps: 3, completeness: 'partial', completenessReason: longName('reason-changes'), times: 'read' });
      }
      // A caller who may not list Stages: every line reads "time hidden".
      if (changes && entityKey === 'notification-worker') {
        Object.assign(changes, { times: 'hidden', timesReason: longName('stages.delivery.pacto.trianalab.io is forbidden: User cannot list resource stages in API group delivery.pacto.trianalab.io at the cluster scope') });
        for (const e of changes.items ?? []) delete e.reached;
      }
      // Resources at scale (#468), on orders-service only so catalog-service stays the
      // realistic case: 40 uses with long provider labels, long configuration keys and
      // values, long external refs, three environments each (Failed, stale Unknown and
      // unsigned Ready), usage reported, untrusted and with 16 long metrics, and three
      // providers the viewer may not see.
      const used = svc?.consumedOfferings as Record<string, unknown> & { items?: Record<string, unknown>[] } | undefined;
      if (svc && used && (used.items ?? []).length && (doc.entity as { key?: string } | undefined)?.key === 'orders-service') {
        const base = (used.items ?? [])[0];
        const pv = base.provider as Record<string, unknown>;
        const phases = [
          { phase: 'Failed', verified: true, producer: longName('platform-adapter'), source: 'evidence', asOf: '2026-11-10T07:00:00Z' },
          { phase: 'Unknown', verified: false, source: 'target-state', stale: true, reportedPhase: 'Ready', reason: longName('the last report is older than the provisioning window'), asOf: '2026-07-29T09:40:00Z' },
          { phase: 'Ready', verified: false, producer: longName('platform-adapter'), source: 'target-state', asOf: '2026-11-10T07:00:00Z' },
        ];
        // Usage per environment: reported, unknown with untrusted figures and long figures.
        const figures = (n: number) => ({ metrics: Array.from({ length: n }, (_, m) => ({ name: longName(`storage-used-metric-${m}`), value: 123456789.123456, unit: 'GiB-month' })),
          cost: { amount: '123456789012345.123456', currency: 'USD', period: 'P1Y' } });
        const usage = [
          { state: 'reported', figures: figures(2), producer: longName('platform-adapter'), source: 'evidence', verified: true, asOf: '2026-11-10T07:00:00Z' },
          { state: 'unknown', reason: longName('the usage report is not signed'), reported: figures(3), source: 'target-state', verified: false, asOf: '2026-07-29T09:40:00Z' },
          { state: 'reported', figures: figures(16), producer: longName('platform-adapter'), source: 'evidence', verified: true, asOf: '2026-11-10T07:00:00Z' },
          { state: 'unknown', reason: longName('the providers that report usage for this resource disagree'), conflict: [longName('producer-a'), longName('producer-b')] },
        ];
        const consumerRevision = { kind: 'revision', key: `orders-service@sha256:${'b'.repeat(64)}`, label: longName('orders-service 1.4.0'), href: `/fleet/revisions/${encodeURIComponent(`orders-service@sha256:${'b'.repeat(64)}`)}` };
        used.items = Array.from({ length: 40 }, (_, i) => {
          const key = `${pv.key}-r${i}`;
          return {
            ...base, offering: i % 3 === 2 ? '' : base.offering, via: i % 2 ? 'dependency' : 'configuration_reference',
            provider: { ...pv, key, label: longName(`resource-provider-${i}`), href: `${pv.href}-r${i}` },
            providerAvailability: { state: ['available', 'degraded', 'unavailable', 'unknown'][i % 4], byEnvironment: [], total: 0, asOf: '2026-11-10T08:00:00Z' },
            requested: i % 2 ? undefined : [{ entry: longName(`entry-${i}`), configuration: longName(`configuration-${i}`), provenance: 'declared', consumerRevision,
              revision: { kind: 'revision', key: `${key}@sha256:${'a'.repeat(64)}`, label: longName(`resource-provider-${i} 16.1.0`), href: `/fleet/revisions/${encodeURIComponent(`${key}@sha256:${'a'.repeat(64)}`)}` },
              values: { total: 3, count: 3, scanned: 3, truncated: false, items: [0, 1, 2].map((k) => ({ key: longName(`spec.parameters.key-${i}-${k}`), value: longName(`value-${i}-${k}`) })) } }],
            provisioning: [0, 1, 2].map((e) => ({ ...phases[e], environment: longName(`production-${e}`), provenance: 'observed', provider: longName('crossplane-adapter'),
              externalRef: `${longName('databases/external-reference')}/${longName(`resource-${i}-${e}`)}` })),
            consumption: i % 4 === 3
              ? [{ state: 'not-reported', provenance: 'observed', reason: 'no provider reports usage or cost for this resource to Pacto; ask Mira for live signals' }]
              : [0, 1, 2, 3].map((e) => ({ ...usage[e], environment: longName(`production-${e}`), entry: longName(`entry-${i}`), provenance: 'observed',
                revision: { kind: 'revision', key: consumerRevision.key, label: consumerRevision.label } })),
          };
        });
        used.total = 120; used.count = 40; used.truncated = true;
        svc.hiddenProviders = 3;
      }
      // The first resource a service uses and the first requirement of a golden path
      // declare no range, so their badges read "Range not declared".
      const tpl = svc?.template as { requirements?: { items?: Record<string, unknown>[] } } | undefined;
      for (const u of [used?.items?.[0], tpl?.requirements?.items?.[0]]) if (u) { u.compatibility = 'unknown'; u.rangeStatus = 'unknown'; }
      for (const it of pl?.items ?? []) {
        if (it.environment) it.environment = longName(String(it.environment));
        for (const v of (it.revisions as Record<string, unknown>[] | undefined) ?? []) v.version = longName(String(v.version ?? 'v'));
        for (const k of ['targetsReason', 'provisioningReason']) if (typeof it[k] === 'string') it[k] = longName(it[k] as string);
        const o = it.owner as Record<string, unknown> | undefined;
        if (o) for (const k of ['owner', 'reason']) if (typeof o[k] === 'string') o[k] = longName(o[k] as string);
        const d = it.declaration as Record<string, unknown> | undefined;
        if (d) { d.name = it.environment; d.file = longName(String(d.file ?? 'file')); if (d.owner) d.owner = { team: longName(String((d.owner as { team?: string }).team ?? 'team')) }; }
      }
      for (const df of (pl?.differences as Record<string, unknown>[] | undefined) ?? []) {
        df.configuration = longName(String(df.configuration)); df.path = longName(String(df.path));
        df.values = Object.fromEntries(Object.entries(df.values as Record<string, unknown>).map(([k, v]) => [longName(k), typeof v === 'string' ? longName(v) : v]));
      }
      for (const e of changes?.items ?? []) for (const k of ['subject', 'from', 'to']) if (typeof e[k] === 'string' && e[k]) e[k] = longName(e[k] as string);
      if (typeof changes?.completenessReason === 'string') changes.completenessReason = longName(changes.completenessReason);
    }
    // A revision's and a target's environments at their longest: names, files and owners.
    const lengthenEnv = (d: Record<string, unknown> | undefined) => {
      if (!d) return;
      d.name = longName(String(d.name ?? 'env')); d.file = longName(String(d.file ?? 'file'));
      if (d.owner) d.owner = { team: longName(String((d.owner as { team?: string }).team ?? 'team')) };
    };
    if (route === '/api/fleet/entities/revision') {
      const rev = doc.revision as Record<string, unknown> | undefined;
      for (const d of (rev?.environments as { items?: Record<string, unknown>[] } | undefined)?.items ?? []) lengthenEnv(d);
      // pactoVersion 2.5 at its longest: 20 announced break consumers of 25 with a long
      // reason (every other one linked to the service it names), a 5-digit port on
      // every interface, a long inferred source and a dependency named from its ref
      // with no range declared.
      if (rev) {
        // A declared pactoVersion behind this Pacto adds a badge to the Pacto version fact.
        rev.pactoVersionCurrent = '2.5'; delete rev.pactoVersionUnknown;
        // pacto ci published this revision from an older pactoVersion: 'plain' is the
        // current version migrated; any other value adds a synthetic newer Pacto, the
        // row at its longest (version, muted text, behind badge, mark).
        const migrated = (window as unknown as { __migrated?: string }).__migrated;
        if (migrated) {
          rev.pactoVersion = rev.pactoVersionCurrent; rev.migratedFrom = '2.4';
          if (migrated === 'plain') delete rev.pactoVersionCurrent; else rev.pactoVersionCurrent = '99.0';
        }
        rev.breaks = { total: 25, count: 20, truncated: true, hidden: 3, items: Array.from({ length: 20 }, (_, i) => ({
          consumer: longName(`break-consumer-${i}`), reason: longName(`break-reason-${i}`), expires: '2026-12-14',
          ...(i % 2 ? { service: { kind: 'service', key: `default/break-consumer-${i}`, label: `break-consumer-${i}` } } : {}) })) };
        const firstDep = (rev.dependencies as { items?: { declaredClaims?: { items?: Record<string, unknown>[] } }[] } | undefined)?.items?.[0];
        for (const c of firstDep?.declaredClaims?.items ?? []) { c.compatibility = 'unknown'; c.nameFromRef = true; }
        for (const it of (rev.interfaces as { items?: Record<string, unknown>[] } | undefined)?.items ?? []) it.port = 65535;
        // The first row at its longest; the engine's other rows stay, so inferredTotal and the "more" rows still agree.
        const longInferred = { field: 'service.name', value: longName('inferred-service-name'), source: `${longName('charts/deeply/nested/chart-folder')}/Chart.yaml:2`, provenance: 'inferred' };
        rev.inferred = [longInferred, ...((rev.inferred as unknown[] | undefined) ?? []).slice(1)];
        // Discovery at its longest: an owner read from an owner file that outranks three
        // long sources, and 20 findings of 100 (truncation first) with long sources.
        const longSource = (s: string) => `${longName(`deeply/nested/${s}`)}/${s}`;
        (rev.inferred as unknown[]).push({ field: 'service.owner', value: longName('team-growth'), source: `${longSource('catalog-info.yaml')}:7`,
          provenance: 'inferred', confidence: 'likely', class: 'ownerFile',
          outranks: [`repository:${longSource('CODEOWNERS')}:4`, `${longSource('deployment.yaml')}:6`, `${longSource('values.yaml')}:12`] });
        rev.inferredTotal = Math.max(Number(rev.inferredTotal ?? 0), (rev.inferred as unknown[]).length);
        rev.discoveryFindings = { total: 100, truncated: true, items: [
          { code: 'DISCOVERY_TRUNCATED', source: '.', message: 'discovery stopped after 5000 files; 4000 files were not read, so results are partial' },
          ...Array.from({ length: 19 }, (_, i) => ({ code: 'DISCOVERY_UNSUPPORTED_FILE', source: longSource(`helmfile-${i}.yaml`), message: longName(`helmfile-${i}.yaml is not read: a format Pacto does not read`) })),
        ] };
      }
    }
    if (route === '/api/fleet/entities/target') {
      const env = (doc.target as Record<string, unknown> | undefined)?.environment as Record<string, unknown> | undefined;
      if (env?.check) lengthenEnv(env.check as Record<string, unknown>);
      // A cluster name at its longest (63 characters); the UID and the keys stay the engine's.
      const cluster = (doc.target as Record<string, unknown> | undefined)?.cluster as Record<string, unknown> | undefined;
      if (cluster) cluster.name = `${cluster.name ?? 'cluster'}-${WORDS.join('-')}`.slice(0, 63);
    }
    if (route === '/api/delivery/history') {
      const tl = doc.timeline as { buckets: Record<string, number>[]; totals: Record<string, number> } | undefined;
      if (tl) {
        tl.buckets.forEach((b, i) => { b.released = 1200 + i * 37; b.failed = (i * 13) % 40; b.held = (i * 7) % 25; b.other = 300; });
        for (const k of ['released', 'failed', 'held', 'other']) tl.totals[k] = tl.buckets.reduce((n, b) => n + b[k], 0);
      }
      // The History columns at their longest: every change's cause and correlation id,
      // and undo offers with long reasons, every other one refused so both forms show.
      // Operation names, record ids and hrefs stay the engine's.
      for (const h of (doc.histories as Record<string, unknown>[] | undefined) ?? []) {
        for (const en of (h.entries as Record<string, unknown>[] | undefined) ?? []) {
          if (!en.operation) continue;
          const c = (en.cause as Record<string, unknown> | undefined) ?? {};
          const v = c.verdict as Record<string, unknown> | undefined;
          if (v) v.message = longName(`verdict-${en.operation}`);
          en.cause = { ...c, reason: longName(`cause-${en.operation}`) };
          en.correlationID = longName(`correlation-${en.operation}`);
        }
        Object.values((h.undoOffers as Record<string, Record<string, unknown>> | undefined) ?? {}).forEach((o, i) => {
          o.reason = longName(`undo-${o.operation}`);
          if (i % 2) o.available = false;
        });
      }
      // Every record line carries a pacto.impact verdict with a long message (#459), and
      // every digest a long fleet revision, so the impact line, its analyse link and the
      // revision links show at their longest. Digests stay the engine's.
      for (const h of (doc.histories as Record<string, unknown>[] | undefined) ?? []) {
        ((h.entries as Record<string, unknown>[] | undefined) ?? []).forEach((en, i) => {
          if (en.record) en.impact = { gate: 'pacto.impact', phase: 'pre', result: i % 2 ? 'Fail' : 'Pass', message: longName(`impact-${en.operation ?? i}`) };
        });
      }
      const revs = (doc.revisions as Record<string, string> | undefined) ?? {};
      for (const d of Object.keys((doc.versions as Record<string, string> | undefined) ?? {})) revs[d] ??= `${longName('registry.example/story/payments')}@${d}`;
      doc.revisions = revs;
      // As of at its longest: long Stage, service, owner, domain and reason names, and
      // namespaces that could not be read.
      const at = doc.at as { stages?: Record<string, unknown>[]; unreadable?: string[] } | undefined;
      if (at) {
        for (const st of at.stages ?? []) for (const k of ['stage', 'service', 'owner', 'businessDomain', 'reason', 'version']) if (typeof st[k] === 'string' && st[k]) st[k] = longName(st[k] as string);
        at.unreadable = [longName('staging-eu-west-partner-feed'), longName('compliance-archive')];
      }
    }
    // One record at its longest: many verdicts with long messages and citations.
    if (route === '/api/delivery/record') {
      const base = ((doc.verdicts as Record<string, unknown>[] | null) ?? [])[0] ?? { gate: 'pacto.diff', phase: 'pre', result: 'Pass' };
      doc.verdicts = Array.from({ length: 12 }, (_, i) => ({ ...base, message: longName(`verdict-${i}`),
        citation: Object.fromEntries(Array.from({ length: 6 }, (_, j) => [longName(`key-${i}-${j}`).slice(0, 40), longName(`value-${i}-${j}`)])) }));
      if (typeof doc.intentReason === 'string') doc.intentReason = longName(doc.intentReason || 'reason');
    }
    // Who put a version there at its longest: a long asker and approvers, each with how
    // they were classified and what let them decide.
    if (route === '/api/delivery/blame') {
      const c = doc.change as Record<string, unknown> | undefined;
      if (c) Object.assign(c, { actor: longName('https://idp.example.com/realms/payments#carol.long-surname'), actorClass: 'human', actorClassifiedBy: 'user' });
      doc.approvals = [0, 1, 2].map((i) => ({ by: longName(`https://idp.example.com/realms/payments#approver-${i}`), decision: 'approve', operation: 'u',
        note: longName(`note-${i}`), classifiedBy: 'user', access: `update deployments/${longName(`payments-settlement-worker-${i}`)} in ${longName('prod')}`,
        accessSource: i % 2 ? 'self-subject-access-review' : 'subject-access-review', accessAt: '2026-11-15T10:00:00Z' }));
    }
    // The publish figure at fleet scale: thousands of services and versions, and the
    // unsigned lane's four chips named 60 to 200 characters long. Only the chip names
    // are lengthened (no route lists them), and the list totals follow the lanes, so
    // "N more" still lands on N plus the chips.
    if (route === '/api/delivery/published') {
      const c = doc.counts as Record<string, unknown> | undefined;
      if (c) {
        const names = (c.firstUnsigned as string[] | undefined) ?? [];
        Object.assign(c, { services: 5000, unsignedServices: 1800, unknownServices: 700, versions: 41000, signed: 37000, unsigned: 3000, unknown: 1000,
          firstUnsigned: Array.from({ length: 4 }, (_, i) => longName(names[i] ?? `unsigned-${i}`)) });
        const pg = doc.page as { total: number; offset: number; limit: number; nextOffset?: number };
        pg.total = q.get('signed') === 'no' ? 1800 : q.get('signed') === 'unknown' ? 700 : q.get('signed') === 'yes' ? 2500 : 5000;
        if (pg.offset + pg.limit < pg.total) pg.nextOffset = pg.offset + pg.limit;
      }
      // Each version's source revision at its longest; digests stay the engine's.
      for (const svc of (doc.services as { versions?: Record<string, unknown>[] }[] | undefined) ?? []) {
        for (const v of svc.versions ?? []) if (typeof v.sourceRevision === 'string') v.sourceRevision = longName(v.sourceRevision);
      }
    }
    // A version's lifecycle at its longest: long environment, Stage and record names, a
    // long note and the newest 50 events on every Stage.
    if (route === '/api/delivery/lifecycle') {
      doc.note = longName(String(doc.note || 'note'));
      for (const k of ['sourceRevision', 'version']) if (typeof doc[k] === 'string') doc[k] = longName(doc[k] as string);
      for (const k of ['signedOn', 'unsignedOn']) if (Array.isArray(doc[k])) doc[k] = (doc[k] as string[]).map(longName);
      // Many Stages: the demo's, repeated under distinct long names.
      const base = (doc.stages as Record<string, unknown>[] | undefined) ?? [];
      if (base.length) doc.stages = Array.from({ length: 12 }, (_, i) => ({ ...base[i % base.length], stage: `${base[i % base.length].stage}-${i}` }));
      for (const st of (doc.stages as Record<string, unknown>[] | undefined) ?? []) {
        st.environment = longName(String(st.environment));
        st.stage = longName(String(st.stage));
        const evs = (st.events as Record<string, unknown>[] | undefined) ?? [];
        if (evs.length) st.events = Array.from({ length: 50 }, (_, i) => ({ ...evs[i % evs.length], record: i % 3 ? longName(`record-${i}`) : undefined }));
      }
    }
    if (route === '/api/delivery/autonomy') {
      doc.byLevel = [{ level: 'observe', stages: 1200 }, { level: 'advise', stages: 2300 }, { level: 'auto-nonbreaking', stages: 1100 }, { level: 'autonomous', stages: 380 }, { level: '', stages: 20 }];
    }
    if (method === 'POST' && route === '/api/fleet/impact') {
      const cs = doc.consumers as Record<string, unknown>;
      const names = [
        'fraud-scoring-realtime-event-stream-projection-read-model-consumer-for-partner-feed-compliance-archive-backfill-eu-west-1-canary-blue-legacy-bridge-settlement-reconciliation-batch-processor-us-east-2',
        'checkout-\u{1F6D2}-edge-adapter-europe-west',
        'orders-‮reverse‬-sync-bridge',
        ...Array.from({ length: 9 }, (_, i) => longName(`consumer-${i}`)),
      ];
      const V = ['incompatible', 'unknown', 'compatible'];
      const E = ['breaks', 'may-degrade', 'unknown'];
      let req: Record<string, unknown> = {};
      try { req = JSON.parse(body ?? '{}'); } catch { /* no body */ }
      const outcome = String(req.outcome ?? '');
      const event = String(req.event ?? '');
      // Every fact the table renders, long: via, evidence (one stale), more than 20
      // targets, resources with phases and an owner link, on items and highlights alike.
      const consumer = (name: string, i: number) => {
        const key = `domain-load/consumer-${i}`;
        const targets = Array.from({ length: 21 }, (_, t) => ({ kind: 'target', key: `prod/k8s/${key}-${t}`, label: longName(`prod-eu-west-1/${name}-target-${t}`), href: `/fleet/targets/${encodeURIComponent(`prod/k8s/${key}-${t}`)}` }));
        return {
          service: { kind: 'service', key, label: name, domain: 'domain-load', href: `/fleet/services/${encodeURIComponent(key)}` },
          // Every other consumer with an unknown verdict declares no range (compatibility:
          // unknown, pactoVersion 2.5), so the verdict tallies stay as they were.
          direct: true, depth: 1, confidence: 'declared',
          compatibilityVerdict: event ? 'unknown' : V[i % 3], compatibility: event ? undefined : i % 6 === 1 ? 'unknown' : '^1.0.0',
          blocking: !event && i === 0, owner: longName(`team-${i}`), path: [], pathTotal: 0, pathTruncated: false,
          ownerRef: { kind: 'owner', key: `team:team-${i}`, label: longName(`team-${i}`), href: `/fleet/owners/${encodeURIComponent(`team:team-${i}`)}` },
          provenance: 'declared+observed', via: ['dependency', 'configuration_reference', 'policy_reference'], required: i % 2 === 0,
          effect: event ? E[i % 3] : undefined, uncontracted: event && i === 2 ? true : undefined,
          targets: targets.slice(0, 20), targetsTotal: 37,
          evidence: [
            { kind: 'target', provenance: 'observed', source: targets[0].key, message: `runs on ${targets[0].label}`, at: '2026-11-10T08:00:00Z' },
            { kind: 'observed-call', provenance: 'observed', source: longName('mira-observation-source'), message: `${name} called it ${longName('through the mesh sidecar')}`, at: '2026-11-08T08:00:00Z', stale: true },
            { kind: 'unreachable', provenance: 'observed', message: longName('DEPENDENCY_UNREACHABLE reported on the consumer target'), at: '2026-11-10T07:00:00Z' },
            { kind: 'lifecycle', provenance: 'declared', message: 'the provider declares itself deprecated' },
          ],
          resources: event === 'removed' ? Array.from({ length: 3 }, (_, r) => ({ entry: longName(`database-${r}`), environment: longName(`production-${r}`), phase: ['Ready', 'Failed', 'Provisioning'][r], provenance: 'observed', verified: true })) : undefined,
          resourcesTotal: event === 'removed' ? 24 : undefined,
        };
      };
      const all = names.map(consumer);
      // An offering event names every provider it asked about: 25, the bound.
      if (req.offeringKey) {
        doc.providers = Array.from({ length: 25 }, (_, i) => {
          const key = `domain-load/${longName(`provider-${i}`)}`;
          return { kind: 'service', key, label: longName(`provider-${i}`), href: `/fleet/services/${encodeURIComponent(key)}` };
        });
      }
      // A leaf (the real answer lists no consumer) draws the clear answer: every lane
      // empty and the gate under them, the shape a complete snapshot gives it.
      const leaf = !!event && !cs.total;
      if (leaf) {
        doc.gate = 'clear';
        doc.gateReason = longName('no contract in the complete snapshot declares a dependency on it and no observation source saw a caller; a service with no contract can still call it');
      } else if (event) {
        // The event population: lanes by effect sum to the total, and each lane's list
        // (filtered by effect, never by verdict) totals its lane.
        const byOutcome = [{ key: 'breaks', count: 1900 }, { key: 'may-degrade', count: 1300 }, { key: 'unknown', count: 800 }];
        const listed = outcome ? all.filter((c) => c.effect === outcome) : all;
        Object.assign(cs, {
          items: listed.slice(0, 10), highlights: all.slice(0, 8), total: 4000, direct: 1400, count: Math.min(10, listed.length), offset: 0, limit: 10,
          nextOffset: 10, truncated: true, hidden: 3, refused: 0,
          outcome: outcome || undefined, matching: outcome ? byOutcome.find((b) => b.key === outcome)?.count ?? 0 : 4000,
          byVerdict: [{ key: 'unknown', count: 4000 }], byOutcome,
          byOwner: Array.from({ length: 5 }, (_, i) => ({ owner: longName(`team-${i}`), ownerKey: `team:team-${i}`, consumers: 200 - i * 20, incompatible: 40 - i * 9 })),
          beyondRanking: 3200, noOwner: 84, owners: 300,
        });
        if (event === 'regressed') {
          const digest = String(req.revision ?? '');
          const lim = doc.limitations as { items: Record<string, unknown>[]; total: number; count: number };
          if (digest.startsWith('sha256:c1ea')) {
            // Read in full and clean: no failure recorded, nothing unread.
            doc.regression = [];
            const kept = lim.items.filter((l) => l.code !== 'DELIVERY_EVIDENCE_UNAVAILABLE');
            lim.total -= lim.items.length - kept.length; lim.count = kept.length; lim.items = kept;
          } else if (digest.startsWith('sha256:')) {
            doc.regression = Array.from({ length: 6 }, (_, i) => ({
              kind: ['failed', 'check', 'rollback'][i % 3], digest, namespace: longName(`shop-namespace-${i}`), stage: longName(`production-eu-west-${i}`),
              gate: i % 3 === 1 ? longName('smoke-error-rate') : undefined, message: longName(`the check found ${i} errors in the last window of the canary analysis`),
              at: '2026-11-10T07:00:00Z', record: i % 2 ? longName(`record-${i}`) : undefined,
            }));
          } else {
            if (!lim.items.some((l) => l.code === 'DELIVERY_EVIDENCE_UNAVAILABLE')) {
              lim.items.unshift({ code: 'DELIVERY_EVIDENCE_UNAVAILABLE', message: longName('name the artifact digest (sha256:...) to read delivery evidence: a version does not identify one artifact') });
              lim.total += 1; lim.count += 1;
            }
          }
        }
      } else {
        // One consistent population: byVerdict judges the range alone (6 carry a verdict
        // this engine does not know); the 5 refused accept the range, so the outcome
        // tally counts them with the incompatible: 14 + 5 = 19.
        const byOutcome = [{ key: 'incompatible', count: 19 }, { key: 'unknown', count: 52 }, { key: 'compatible', count: 1207 }, { key: 'unclassified', count: 6 }];
        const listed = outcome ? all.filter((c) => c.compatibilityVerdict === outcome) : all;
        Object.assign(cs, {
          items: listed.slice(0, 10), highlights: all.slice(0, 8), total: 1284, direct: 412, count: Math.min(10, listed.length), offset: 0, limit: 10,
          nextOffset: 10, truncated: true, hidden: 3, refused: 5,
          outcome: outcome || undefined, matching: outcome ? byOutcome.find((b) => b.key === outcome)!.count : 1284,
          byVerdict: [{ key: 'incompatible', count: 14 }, { key: 'unknown', count: 52 }, { key: 'compatible', count: 1212 }, { key: 'novel', count: 6 }],
          byOutcome,
          byOwner: Array.from({ length: 5 }, (_, i) => ({ owner: longName(`team-${i}`), consumers: 200 - i * 20, incompatible: 9 - i })),
          beyondRanking: 400, noOwner: 84, owners: 300,
        });
        // Golden paths to review, with long names, past the bound.
        doc.templates = { total: 240, count: 3, truncated: true, items: Array.from({ length: 3 }, (_, i) => {
          const key = `domain-load/${longName(`golden-path-${i}`)}`;
          return { kind: 'service', key, label: longName(`golden-path-${i}`), href: `/fleet/services/${encodeURIComponent(key)}` };
        }) };
      }
      if (meta) meta.completeness = leaf ? 'complete' : 'partial';
    }
    // An external system at fleet scale (517-S2): the engine answers for the demo's
    // parcel-carrier and the page becomes `stripe` with 412 consumers (3 hidden), 300 of
    // 1,284 inferred sources and 40 observed ones, every identity and `where` long.
    if (route === '/api/fleet/entities/external' && q.get('key') === 'stripe') {
      const ent = doc.entity as Record<string, unknown>;
      const x = doc.external as Record<string, unknown>;
      const rec = x.external as Record<string, unknown>;
      Object.assign(ent, { key: 'stripe', label: longName('stripe'), href: '/fleet/externals/stripe' });
      const svc = (i: number) => `domain-load/${longName(`external-consumer-${i}`)}`;
      Object.assign(rec, { key: 'stripe', label: longName('stripe'), class: 'vendor-api', consumersTotal: 412, hiddenConsumers: 3,
        catalog: { origin: 'organization', digest: `sha256:${'3e9a'.repeat(16)}`, entry: longName('stripe-entry') } });
      x.consumers = Array.from({ length: 25 }, (_, i) => ({
        service: { kind: 'service', key: svc(i), label: longName(`external-consumer-${i}`), href: `/fleet/services/${encodeURIComponent(svc(i))}` },
        required: i % 2 === 0, versions: [longName(`api-version-${i}`)],
      }));
      const KINDS = ['env', 'configMap', 'secretKeyRef', 'secretRef', 'externalNameService', 'selectorlessService', 'serviceEntry',
        'helmValues', 'compose', 'kustomize', 'sdk', 'terraform', 'crossplane'];
      x.inferredSources = Array.from({ length: 300 }, (_, i) => ({
        service: svc(i % 25), source: longName(`k8s-source-${i % 4}`), kind: KINDS[i % KINDS.length],
        where: `deployment/${longName(`workload-${i}`)} env ${longName(`STRIPE_API_BASE_${i}`).toUpperCase()}`,
        host: i % 3 ? `${longName(`api-${i}`)}.stripe.com:443` : '', confidence: i % 3 ? 'likely' : 'possible',
        entry: i % 5 ? longName('stripe-entry') : undefined, catalog: `sha256:${'3e9a'.repeat(16)}`, observedAt: '2026-11-16T08:00:00Z',
        // Every fourth row is a repository source (508-S5): a long revision, read from an
        // SDK manifest or a Terraform resource, only possible.
        ...(i % 4 === 3 ? { kind: i % 8 === 3 ? 'sdk' : 'terraform', confidence: 'possible', host: '', observedAt: undefined,
          revision: `${svc(i % 25)}@sha256:${longName(`revision-${i}`)}` } : {}),
      }));
      x.inferredSourcesTotal = 1284;
      // Few observed rows listed (of 40), so the capped list reaches the inferred and repository rows.
      x.observedSources = Array.from({ length: 3 }, (_, i) => ({ service: svc(i), source: longName(`otel-${i}`), count: 1200 + i, firstSeen: '2026-11-01T08:00:00Z', lastSeen: '2026-11-16T08:00:00Z' }));
      x.observedSourcesTotal = 40;
    }
    // Inferred and observed edges to external systems beside the declared ones, so the
    // focused graph, its edge list and a service's relationships draw them.
    const extEdges = (edges: Record<string, unknown>[] | undefined, self: Record<string, unknown> | undefined) => {
      const tpl = edges?.[0];
      if (!edges || !tpl || !self) return [];
      // walk lengthens every label once per object it visits: each reference is its own
      // object, never `self` or a node's ref shared, or the page's own label grows three
      // times and the delivery lookups (engineName) can no longer map it back.
      const ext = (key: string) => ({ kind: 'external', key, label: key, href: `/fleet/externals/${encodeURIComponent(key)}` });
      const edge = (to: Record<string, unknown>, provenance: string, observed: boolean) => ({
        ...tpl, id: `${self.key}->${to.key}`, href: String(to.href), from: { ...self }, to, provenance, inferred: provenance === 'inferred',
        expected: false, observed, difference: observed ? 'observed-not-expected' : undefined, serviceCorroboration: undefined,
        declaredClaims: { items: [], total: 0, count: 0, truncated: false },
      });
      const added = [edge(ext('stripe'), 'inferred', false), edge(ext('host/corp-legacy.co.uk'), 'observed', true)];
      edges.push(...added);
      return added.map((e) => ({ ...e.to }));
    };
    if (route === '/api/fleet/neighborhood') {
      const nodes = doc.nodes as Record<string, unknown>[] | undefined;
      const focus = nodes?.find((n) => n.focus) ?? nodes?.[0];
      for (const ref of extEdges(doc.edges as Record<string, unknown>[] | undefined, focus?.ref as Record<string, unknown> | undefined)) {
        nodes?.push({ depth: 1, ref });
      }
      // An inferred end at its longest: a 63-character namespace with no break point.
      const ns = 'paymentsproductionnortheuropewestonealphabravocharliedeltaecho1';
      const lengthen = (r: Record<string, unknown> | undefined) => {
        if (r?.declaration === 'inferred' && typeof r.label === 'string') r.label = `k8s:${ns}/CronJob/${r.label.split('/').pop()}`;
      };
      for (const n of nodes ?? []) lengthen(n.ref as Record<string, unknown> | undefined);
      for (const e of (doc.edges as Record<string, unknown>[] | undefined) ?? []) {
        lengthen(e.from as Record<string, unknown> | undefined);
        lengthen(e.to as Record<string, unknown> | undefined);
      }
    }
    // An inferred service at its longest: a 63-character namespace, a 253-character
    // name, a digest image, 20 ports, three probes with long paths, a 200-character
    // `from` path and calls at the detail preview bound (200), truncated.
    if (route === '/api/fleet/entities/service' && (doc.entity as { declaration?: string } | undefined)?.declaration === 'inferred') {
      const svc = doc.service as Record<string, unknown>;
      const ns = 'paymentsproductionnortheuropewestonealphabravocharliedeltaecho1';
      const name = `legacy-billing-${'reconciliation-settlement-ledger-export-'.repeat(7)}`.slice(0, 253);
      const pod = `spec.template.spec.containers[name=${'billing-reconciliation-settlement-'.repeat(5)}]`.slice(0, 180);
      const from = (leaf: string) => `${pod}.${leaf}`.padEnd(200, '-');
      svc.workload = {
        ...(svc.workload as object), namespace: ns, name,
        image: { value: `registry.example.com/${'settlement-reconciliation/'.repeat(4)}legacy-billing@sha256:${'4f1c'.repeat(16)}`, from: from('image') },
        ports: Array.from({ length: 20 }, (_, i) => ({ port: 8080 + i, name: `http-${'metrics'.repeat(2)}-${i}`, protocol: 'TCP', from: from(`ports[${i}].containerPort`) })),
        probes: ['readiness', 'liveness', 'startup'].map((kind) => ({ kind, path: `/healthz/${'settlement-reconciliation/'.repeat(6)}${kind}`, port: 'http-metricsmetrics-0', from: from(`${kind}Probe`) })),
      };
      svc.calls = { total: 1200, count: 200, truncated: true,
        items: Array.from({ length: 200 }, (_, i) => ({ kind: 'service', key: `load/call-${i}`, label: `call-${i}`, href: `/fleet/services/load%2Fcall-${i}` })) };
    }
    // A replacement at its longest (509-S4): a cluster-qualified inferred key with a
    // 63-character namespace and a 253-character name, on the Replaces line and on the
    // notice a replaced key shows.
    if (route === '/api/fleet/entities/service') {
      const longKey = `k8s:cluster-${'a1b2'.repeat(9)}%2F${'paymentsproductionnortheuropewestonealphabravocharliedeltaecho1'}%2FDeployment%2F${'experiments-settlement-reconciliation-'.repeat(7)}`.slice(0, 340);
      const svc = doc.service as Record<string, unknown> | undefined;
      const reps = svc?.replaces as Record<string, unknown>[] | undefined;
      if (reps?.length) { reps[0] = { ...reps[0], from: longKey, target: `${'prod-'.repeat(12)}/Deployment/${'experiment-service-'.repeat(8)}` }; svc!.replacesTotal = 1200; }
      const rf = doc.replacedFrom as Record<string, unknown> | undefined;
      if (rf) doc.replacedFrom = { ...rf, from: longKey, target: `${'prod-'.repeat(12)}/Deployment/${'experiment-service-'.repeat(8)}` };
    }
    // Where the values come from at its bound (513-S5): 200 of 1200 field rows with
    // long fields and values, every provenance, 7 values from a workload the viewer
    // cannot see, 3 hidden replacements and 200 of 260 replacements, on the declared service
    // that has the fold.
    if (route === '/api/fleet/entities/service' && (doc.service as Record<string, unknown> | undefined)?.fields) {
      const svc = doc.service as Record<string, unknown>;
      const kinds = ['declared', 'accepted', 'attested', 'inferred'];
      svc.fields = { total: 1200, count: 200, truncated: true, hidden: 7,
        items: Array.from({ length: 200 }, (_, i) => ({
          field: i % 2 ? `dependencies[${'settlement-reconciliation-'.repeat(4)}${i}].compatibility` : `inputs.${'cluster-runtime-observation-'.repeat(5)}${i}`,
          value: `>=${i}.0.0 <${i + 1}.0.0 || ${'^1.2.3-rc.4+build.'.repeat(4)}${i}`, provenance: kinds[i % 4],
          by: i % 4 === 1 ? `${'ana.maria.'.repeat(6)}@acme.example` : undefined,
          source: `cluster:fleet reported versions ${'2.3.1, '.repeat(12)}${i}`, completeness: 'partial',
        })) };
      svc.replacesHidden = 3;
      // 200 of 260 replacements, the bound: one facts block, 199 single lines and "60 more".
      const reps = (svc.replaces as Record<string, unknown>[] | undefined) ?? [];
      if (reps.length) {
        svc.replaces = Array.from({ length: 200 }, (_, i) => (i ? { ...reps[0], from: `k8s:billing%2FDeployment%2F${'invoices-worker-'.repeat(4)}${i}`, workload: undefined } : reps[0]));
        svc.replacesTotal = 260;
      }
    }
    if (route === '/api/fleet/entities/service') {
      const rel = (doc.service as Record<string, unknown> | undefined)?.relationships as { items?: Record<string, unknown>[]; count?: number; total?: number } | undefined;
      const added = extEdges(rel?.items, doc.entity as Record<string, unknown>).length;
      if (rel && added) { rel.count = (rel.count ?? 0) + added; if (typeof rel.total === 'number') rel.total += added; }
    }
    // Every non-declared owner provenance beside the long owner names (516-S1): the
    // service owner, each offering provider's owner and each resource provider's owner
    // carry the owner-provenance mark, in turn.
    if (route === '/api/fleet/entities/service' || route === '/api/fleet/entities/offering') {
      const PROVENANCE = ['declared-via-target', 'attested', 'inferred'];
      let n = 0;
      const mark = (o: unknown) => { if (o && typeof o === 'object' && (o as Record<string, unknown>).declared) (o as Record<string, unknown>).provenance = PROVENANCE[n++ % PROVENANCE.length]; };
      const svc = doc.service as Record<string, unknown> | undefined;
      mark(svc?.ownership);
      for (const u of (svc?.consumedOfferings as { items?: Record<string, unknown>[] } | undefined)?.items ?? []) mark(u.providerOwner);
      for (const p of ((doc.offering as Record<string, unknown> | undefined)?.providers as { items?: Record<string, unknown>[] } | undefined)?.items ?? []) mark(p.ownership);
    }
    walk(doc);
  };

  // A service page asks delivery for its service by the page's (lengthened) label: ask
  // the engine for the name it knows, and the rows come back under the long name.
  // Under longPropose a panel also asks by a Stage's lengthened namespace and stage (the
  // undo panel's GET): those go back to the engine's names too.
  const engineName = (path: string): string => {
    const [route, query = ''] = path.split('?');
    if (!route.startsWith('/api/delivery/')) return path;
    const q = new URLSearchParams(query);
    const keys = flags.__pactoLongPropose ? ['service', 'namespace', 'stage'] : ['service'];
    let moved = false;
    for (const k of keys) {
      const asked = q.get(k);
      const short = asked ? [...memo].find(([, v]) => v === asked)?.[0] : undefined;
      if (short) { q.set(k, short); moved = true; }
    }
    return moved ? `${route}?${q}` : path;
  };
  // A route that opens the propose panel sets these flags on the page: longPropose
  // lengthens the Stage rows' namespace, stage and version (a plan or propose body is
  // asked of the engine under the short names again), stalePropose answers every
  // propose PlanStale with the fresh plan and no field that explains it.
  // ambiguousTarget answers the sentinel target key with the 409 a 3-part key that
  // names targets in more than one cluster gets: 6 candidates with long labels.
  // inferredUnreadable answers every service and revision as one whose inferred record
  // Pacto could not read: the service has no record field, the revision keeps only the
  // values a local read infers on its own (pactoVersion, service.name).
  // clusterFacts answers newsletter-service's revision as a signed declaration: 9 inferred
  // rows (cluster and folder sources, partial, a proposed range, 80-character values),
  // coverage, hidden counts and a long signer. 'dropped' answers it as a declaration by an
  // unverified signer whose namespace the viewer may not read: no rows, no namespace and
  // no hidden count (its cluster facts were already dropped). 'notAllowed' answers it as a
  // checked signature the installation signer policy refuses.
  // multiCluster (514-S2) answers every entities list as a view over three clusters with
  // 63-character names: the demo's own cluster, one the reader may not read and one whose
  // review failed, so the Cluster filter, its caption and the Clusters block render.
  const flags = window as unknown as { __pactoNewSigner?: boolean; __pactoLongPropose?: boolean; __pactoStalePropose?: boolean; __pactoAmbiguousTarget?: boolean; __pactoInferredUnreadable?: boolean; __pactoAccept?: 'plan' | 'refused' | 'breaks' | 'forbidden'; __pactoClusterFacts?: boolean | 'dropped' | 'notAllowed'; __pactoMultiCluster?: boolean };
  const shortOf = (v: unknown) => [...memo].find(([, l]) => l === v)?.[0] ?? v;
  const engineBody = (path: string, body: string | null): string | null => {
    if (!flags.__pactoLongPropose || !body || !/^\/api\/delivery\/(plan|operations)$/.test(path)) return body;
    const b = JSON.parse(body) as Record<string, unknown>;
    b.namespace = shortOf(b.namespace);
    b.stage = shortOf(b.stage);
    return JSON.stringify(b);
  };
  // The Metrics page's answers (#493). The fixture replaces the demo's own run records, so
  // `sessionStorage['fixture.stats']` picks the state, read on every call: `off` serves
  // none, `none` a window without run records, unset a recorded one, and `engine` leaves
  // /api/stats, /api/stats/records and the stats capability to the engine (the browser
  // demo's own run records, #493 S5).
  const statsMode = () => { try { return sessionStorage.getItem('fixture.stats') ?? ''; } catch { return ''; } };
  const mcpMode = () => { try { return sessionStorage.getItem('fixture.mcp') ?? ''; } catch { return ''; } };
  // An issuer-only /mcp at its longest: a 200-character fixed URL and issuer.
  const ISSUER_MCP_SOURCE = `this dashboard, at https://${'pacto-'.repeat(31)}example/mcp; your MCP client signs in with https://${'dex-'.repeat(45)}example`;
  // `restricted` serves what a signed-in reader with a narrow view gets: the hidden line,
  // no coverage history. Unset serves a recorded window at the size
  // `sessionStorage['fixture.stats.envs']` picks, read on every call (#597): `5` a small
  // ladder, `200` about 30 independent Stage chains, unset (`60`) the scale case: 60
  // environments on a four-deep ladder with forks, a cycle and an unordered one, 2,000
  // services paged, names of 60 to 200 characters and 3 skipped records. Topology
  // fields follow the API's rules (upstream is the transitive closure, parents its
  // reduction, depth the longest path, chain end a fed leaf). Every count the page links
  // says how many records it opens, and /api/stats/records answers exactly that many
  // (figureLinkTotals checks it).
  const statsSize = () => { try { return sessionStorage.getItem('fixture.stats.envs') ?? '60'; } catch { return '60'; } };
  const STATS_DAYS: Record<string, number> = { '7d': 7, '30d': 30, '90d': 90 };
  type Ratio = { count: number; of: number; unknown?: boolean };
  type Lead = { p50Seconds?: number; p90Seconds?: number; joined: number; unjoined: number; unknown?: boolean };
  type EnvMetrics = { environment: string; source: string; operations: number; held: Ratio; failed: Ratio; rolledBack: Ratio;
    verificationFailed: Ratio; acceptedUnknown: number; leadTime: Lead };
  type StatsEnv = EnvMetrics & { upstream: string[]; parents?: string[]; depth?: number; chainEnd?: boolean; cycle?: boolean; unordered?: boolean };
  // A fleet: environments in Stage order, each one's direct upstream, and the owners and
  // domains whose services it delivers.
  type StatsFleet = { envs: StatsEnv[]; owners: Record<string, string[]>; domains: Record<string, string[]>; attributionPartial: boolean };
  const RATES = ['held', 'failed', 'rolledBack', 'verificationFailed'] as const;
  // A rate with nothing to take it over is unknown and keeps its count: a rollback with no
  // succeeded promote in the window.
  const ratio = (count: number, of: number): Ratio => (of ? { count, of } : { count, of: 0, unknown: true });
  const lead = (p50h: number, p90h: number | null, joined: number, unjoined: number): Lead => (joined
    ? { p50Seconds: Math.round(p50h * 3_600), ...(p90h == null ? {} : { p90Seconds: Math.round(p90h * 3_600) }), joined, unjoined }
    : { joined: 0, unjoined, unknown: true });
  const metricsOf = (environment: string, r: Record<(typeof RATES)[number], [number, number]>, acceptedUnknown: number, leadTime: Lead): EnvMetrics => ({
    environment, source: 'context', operations: Math.max(...RATES.flatMap((k) => r[k])) + r.rolledBack[0] + 3, acceptedUnknown, leadTime,
    held: ratio(...r.held), failed: ratio(...r.failed), rolledBack: ratio(...r.rolledBack), verificationFailed: ratio(...r.verificationFailed),
  });
  // place sets upstream (the transitive closure of each environment's direct upstream),
  // depth, parents and chain end with the API's rules (pkg/dashboard placeDepths).
  const place = (envs: (EnvMetrics & { cycle?: boolean; unordered?: boolean })[], direct: Record<string, string[]>): StatsEnv[] => {
    const up = new Map<string, string[]>();
    const closure = (e: string): string[] => {
      if (!up.has(e)) up.set(e, [...new Set((direct[e] ?? []).flatMap((p) => [p, ...closure(p)]))]);
      return up.get(e)!;
    };
    const by = new Map(envs.map((e) => [e.environment, e]));
    const fed = new Set(envs.flatMap((e) => closure(e.environment)));
    const depth = new Map<string, number | undefined>();
    const depthOf = (e: string): number | undefined => {
      if (!depth.has(e)) {
        const r = by.get(e)!;
        const ds = closure(e).map(depthOf);
        depth.set(e, r.cycle || r.unordered || ds.includes(undefined) ? undefined : Math.max(0, ...ds.map((d) => d! + 1)));
      }
      return depth.get(e);
    };
    return envs.map((e) => {
      const upstream = closure(e.environment), d = depthOf(e.environment);
      if (d === undefined) return { ...e, upstream };
      const parents = upstream.filter((u) => !upstream.some((v) => closure(v).includes(u)));
      return { ...e, upstream, depth: d, ...(parents.length ? { parents } : {}), ...(upstream.length && !fed.has(e.environment) ? { chainEnd: true } : {}) };
    });
  };
  // 5: test feeds prod, prod feeds prod-eu and prod-us, and a preview environment stands
  // alone. One rate counts a rollback with nothing to take it over, one is over 50%, one
  // lead time joined no publish record, one has no p90 and one is 0s.
  const smallFleet = (): StatsFleet => {
    const rows = [
      metricsOf('test', { held: [0, 6], failed: [1, 6], rolledBack: [0, 5], verificationFailed: [1, 5] }, 1, lead(0, 0, 0, 3)),
      metricsOf('prod', { held: [1, 5], failed: [1, 5], rolledBack: [1, 4], verificationFailed: [1, 4] }, 0, lead(25, 54, 4, 1)),
      metricsOf('prod-eu', { held: [1, 3], failed: [2, 3], rolledBack: [1, 3], verificationFailed: [1, 3] }, 0, lead(180, 216, 3, 0)),
      metricsOf('prod-us', { held: [0, 2], failed: [0, 2], rolledBack: [1, 0], verificationFailed: [0, 2] }, 0, lead(181, null, 2, 0)),
      metricsOf('preview-checkout', { held: [0, 2], failed: [0, 2], rolledBack: [0, 2], verificationFailed: [0, 2] }, 0, lead(0, 0, 2, 0)),
    ];
    return { envs: place(rows, { prod: ['test'], 'prod-eu': ['prod'], 'prod-us': ['prod'] }), attributionPartial: false,
      owners: { test: ['payments'], prod: ['payments', 'checkout'], 'prod-eu': ['checkout'], 'prod-us': ['checkout'], 'preview-checkout': ['checkout'] },
      domains: { test: ['commerce'], prod: ['commerce'], 'prod-eu': ['commerce'], 'prod-us': ['commerce'], 'preview-checkout': ['(none)'] } };
  };
  // 60: the long names, the `hot` pattern (only a few environments have something to
  // list, so the link sweep stays short) and six rates with nothing to count, on a ladder
  // of 8, 12, 16 and 22 environments where every tenth one has two parents; 58 is on a
  // cycle and 59 is ordered both ways.
  const statsEnvName = (i: number) => longName(`stats-env-${i}`);
  const LADDER = [8, 12, 16, 22];
  const NOTHING_TO_COUNT: [number, (typeof RATES)[number]][] = [[1, 'rolledBack'], [17, 'verificationFailed'], [29, 'rolledBack'], [41, 'verificationFailed'], [44, 'held'], [57, 'rolledBack']];
  const scaleFleet = (): StatsFleet => {
    const rows = Array.from({ length: 60 }, (_, i) => {
      const hot = i % 15 === 0, r = NOTHING_TO_COUNT.find(([j]) => j === i)?.[1];
      const m = metricsOf(statsEnvName(i), { held: [hot ? 3 : 0, 20], failed: [hot ? 2 : 0, 20], rolledBack: [hot ? 1 : 0, 18], verificationFailed: [hot ? 1 : 0, 16] },
        i % 9, i % 3 ? lead(i + 1, 2.5 * (i + 1), 4, 1) : lead(0, 0, 0, 2));
      if (r) m[r] = ratio(0, 0);
      return { ...m, operations: 40 + i, ...(i === 58 ? { cycle: true } : {}), ...(i === 59 ? { unordered: true } : {}) };
    });
    const direct: Record<string, string[]> = {};
    for (let d = 1, start = LADDER[0]; d < LADDER.length; start += LADDER[d], d++) {
      const prev = start - LADDER[d - 1];
      for (let j = 0; j < LADDER[d]; j++) {
        const i = start + j, p = prev + (j % LADDER[d - 1]);
        direct[statsEnvName(i)] = i % 10 === 5 ? [statsEnvName(p), statsEnvName(prev + ((j + 1) % LADDER[d - 1]))] : [statsEnvName(p)];
      }
    }
    direct[statsEnvName(58)] = [statsEnvName(35)];
    direct[statsEnvName(59)] = [statsEnvName(58)];
    const TEAMS = ['payments', 'checkout', 'identity', 'search', 'ledger', 'notifications', 'platform', longName('stats-team')];
    const DOMAINS = ['commerce', 'identity', 'discovery', 'finance', 'messaging', 'platform'];
    const owners: Record<string, string[]> = {}, domains: Record<string, string[]> = {};
    rows.forEach((e, i) => {
      owners[e.environment] = i === 59 ? ['(unknown)'] : i % 7 === 2 ? [TEAMS[i % 8], TEAMS[(i + 3) % 8]] : [TEAMS[i % 8]];
      domains[e.environment] = i === 58 ? ['(none)'] : i % 11 === 4 ? [DOMAINS[Math.floor(i / 10)], DOMAINS[(Math.floor(i / 10) + 1) % 6]] : [DOMAINS[Math.floor(i / 10)]];
    });
    return { envs: place(rows, direct), owners, domains, attributionPartial: false };
  };
  // 200: the mockup's generator, seeded: independent Stage chains of depth 2 to 5, each
  // one team in one domain, the last hop fanning out to cells; a few environments also
  // deliver a second team's services. The snapshot owners come from is partial.
  const largeFleet = (): StatsFleet => {
    let seed = 7;
    const rnd = () => (seed = (seed * 1664525 + 1013904223) % 4294967296) / 4294967296;
    const ri = (a: number, b: number) => a + Math.floor(rnd() * (b - a + 1));
    const teams = ['payments', 'checkout', 'identity', 'search', 'ledger', 'notifications', 'platform-observability', 'ml-features', 'catalog', 'fraud-signals', 'partner-gateway', 'data-lake-ingest'];
    const doms = ['eu-central-1', 'eu-west-1', 'us-east-1', 'us-west-2', 'ap-southeast-1', 'internal-tools'];
    const svc = ['api', 'web', 'worker', 'sso', 'risk-engine', 'settlement', 'indexer', 'scheduler', 'feature-store', 'webhooks'];
    const stageNames = [['sandbox', 'dev'], ['integration', 'test', 'qa'], ['staging', 'preprod', 'perf'], ['canary', 'prod-shadow']];
    const lead2 = [[0.5, 4], [3, 20], [12, 60], [40, 150], [100, 300]];
    const rows: EnvMetrics[] = [], direct: Record<string, string[]> = {}, owners: Record<string, string[]> = {}, domains: Record<string, string[]> = {};
    for (let c = 0; rows.length < 200; c++) {
      const team = teams[c % 12], dom = doms[(c * 5) % 6], depth = 2 + ((c * 7) % 4), name = `${team}-${svc[c % 10]}`;
      let prev = '';
      for (let d = 0; d < depth; d++) {
        const end = d === depth - 1, stage = end ? 'prod' : stageNames[d][c % stageNames[d].length], fan = end ? ri(1, 9) : 1;
        for (let f = 0; f < fan && rows.length < 200; f++) {
          const env = `${name}-${stage}-${dom}${fan > 1 ? `-cell-${String(f + 1).padStart(2, '0')}-${['blue', 'green'][f % 2]}` : ''}`;
          const n = d === 0 ? ri(20, 80) : ri(3, 30), risk = rnd() * (end ? 0.35 : 0.22);
          const rate = (): [number, number] => { const of = ri(Math.max(1, n - 5), n); return [Math.round(of * risk * rnd() * 1.6), of]; };
          const [a, b] = lead2[d], p50 = a + rnd() * (b - a);
          const i = rows.length;
          rows.push(metricsOf(env, { held: rate(), failed: rate(), rolledBack: rate(), verificationFailed: rate() }, ri(0, 2),
            lead(p50, p50 * (1.3 + rnd() * 1.4), i % 31 === 11 ? 0 : ri(1, 8), ri(0, 1))));
          if (prev) direct[env] = [prev];
          owners[env] = i % 25 === 3 ? [team, teams[(c + 1) % 12]] : [team];
          domains[env] = [dom];
          if (!end) prev = env;
        }
      }
    }
    for (let i = 7; i < 200; i += 17) rows[i][RATES[i % 4]] = ratio(0, 0); // nothing to count
    return { envs: place(rows, direct), owners, domains, attributionPartial: true };
  };
  const FLEETS: Record<string, () => StatsFleet> = { '5': smallFleet, '60': scaleFleet, '200': largeFleet };
  const fleetMemo = new Map<string, StatsFleet>();
  const statsFleet = () => {
    const size = FLEETS[statsSize()] ? statsSize() : '60';
    if (!fleetMemo.has(size)) fleetMemo.set(size, FLEETS[size]());
    return fleetMemo.get(size)!;
  };
  // Part k of n of a count, the remainder on the first parts, so the parts add up and a
  // part of a rate never counts more than it is taken over.
  const part = (v: number, k: number, n: number) => Math.floor(v / n) + (k < v % n ? 1 : 0);
  const partOf = (e: EnvMetrics, k: number, n: number): EnvMetrics => {
    const l = e.leadTime, joined = part(l.joined, k, n), unjoined = part(l.unjoined, k, n);
    const out = { environment: e.environment, source: e.source, operations: part(e.operations, k, n), acceptedUnknown: part(e.acceptedUnknown, k, n),
      leadTime: joined && l.p50Seconds !== undefined ? { ...l, joined, unjoined } : { joined: 0, unjoined, unknown: true } } as EnvMetrics;
    for (const r of RATES) out[r] = ratio(part(e[r].count, k, n), part(e[r].of, k, n));
    return out;
  };
  // ponytail: a fixture's pooled percentile weighs each environment's p50 and p90 by its
  // joined promotions; the engine pools the real durations (stats.LeadTimesIn).
  const pool = (ls: Lead[]): Lead => {
    const known = ls.filter((l) => l.joined && l.p50Seconds !== undefined);
    const unjoined = ls.reduce((n, l) => n + l.unjoined, 0), joined = known.reduce((n, l) => n + l.joined, 0);
    if (!joined) return { joined: 0, unjoined, unknown: true };
    const rank = (pick: (l: Lead) => number, p: number) => {
      const v = known.flatMap((l) => Array<number>(l.joined).fill(pick(l))).sort((a, b) => a - b);
      return v[Math.ceil(p * v.length) - 1];
    };
    return { p50Seconds: rank((l) => l.p50Seconds!, 0.5), p90Seconds: rank((l) => l.p90Seconds ?? l.p50Seconds!, 0.9), joined, unjoined };
  };
  const depthLabel = (d: number) => (d === 0 ? 'entry' : `hop ${d + 1}`);
  const leadTimeGroups = (envs: StatsEnv[], buckets: Record<string, unknown>[]) => {
    const ends = envs.filter((e) => e.chainEnd).map((e) => e.environment);
    const groups: { key: string; label: string; depth?: number; members: string[] }[] = ends.length ? [{ key: 'chain-end', label: 'chain end', members: ends }] : [];
    for (const d of [...new Set(envs.flatMap((e) => (e.depth === undefined ? [] : [e.depth])))].sort((a, b) => a - b)) {
      groups.push({ key: `depth-${d}`, label: depthLabel(d), depth: d, members: envs.filter((e) => e.depth === d).map((e) => e.environment) });
    }
    if (!ends.length && envs.length) groups.push({ key: 'all', label: 'every environment', members: envs.map((e) => e.environment) });
    return groups.map(({ members, ...g }) => {
      const of = (es: EnvMetrics[]) => pool(es.filter((e) => members.includes(e.environment)).map((e) => e.leadTime));
      return { ...g, environments: members.length, leadTime: of(envs),
        buckets: buckets.map((b) => of(((b.metrics as { environments?: EnvMetrics[] } | undefined)?.environments) ?? [])) };
    });
  };
  // environmentsBy: each owner's or domain's numbers per environment. An environment that
  // delivers two groups' services splits its records between them.
  const environmentsBy = (f: StatsFleet, of: Record<string, string[]>) => {
    const groups = new Map<string, EnvMetrics[]>();
    for (const e of f.envs) {
      const keys = of[e.environment] ?? [];
      keys.forEach((k, i) => { if (!groups.has(k)) groups.set(k, []); groups.get(k)!.push(partOf(e, i, keys.length)); });
    }
    const named = (k: string) => k.startsWith('(');
    return [...groups].sort(([a], [b]) => Number(named(a)) - Number(named(b)) || a.localeCompare(b))
      .map(([key, envs]) => ({ key, label: named(key) ? key.slice(1, -1) : key, environments: envs.sort((a, b) => a.environment.localeCompare(b.environment)) }));
  };
  const coverageOf = (asOf: Date, partial: boolean) => ({
    services: 2000, uncontracted: 14, knowledge: { services: 2000, described: 1240, withGaps: 560, unknown: 200, notDeployed: 90 },
    links: { exact: 4100, inferred: 900, ambiguous: 40, unresolved: 12 }, snapshotAsOf: asOf.toISOString(), completeness: partial ? 'partial' : 'complete',
  });
  const statsEnvelope = (mode: string, query: string) => {
    const empty = mode === 'none', restricted = mode === 'restricted';
    const days = STATS_DAYS[new URLSearchParams(query).get('window') ?? ''] ?? 30;
    const until = new Date(), since = new Date(until.getTime() - days * 86_400_000);
    const limitations = empty
      ? [{ code: 'NO_RUN_RECORDS', message: 'no run records in this window: recording may be off for these jobs, or the window holds none' }]
      : [{ code: 'RUN_RECORDS_SKIPPED', message: '3 run records could not be counted: 1 malformed and 2 unreadable' },
          ...(restricted ? [{ code: 'OUTSIDE_YOUR_VIEW', message: 'some run records name services outside your view of the fleet and are left out of every number' },
            { code: 'COVERAGE_OUTSIDE_YOUR_VIEW', message: 'coverage records count every service, so only a reader who can see every service sees them' }] : []),
          ...(statsFleet().attributionPartial ? [{ code: 'ATTRIBUTION_PARTIAL', message: 'the fleet snapshot owner and domain come from is partial; a service it misses groups as unknown' }] : [])];
    return {
      asOf: until.toISOString(), window: { since: since.toISOString(), until: until.toISOString() }, registry: 'ghcr.io/acme/pacto-stats',
      recordsRead: empty ? 0 : 1240, completeness: empty ? 'empty' : 'partial', provenance: 'unsigned statistics', truncated: '',
      skipped: empty ? { malformed: 0, oversized: 0, unreadable: 0 } : { malformed: 1, oversized: 0, unreadable: 2 }, hidden: { present: restricted }, limitations,
    };
  };
  // The next-to-newest bucket holds no record (a gap: never 0); every other one holds
  // its share of each environment's window numbers, so the buckets add up to the window.
  const statsBuckets = (since: Date, until: Date, restricted: boolean) => {
    const size = (until.getTime() - since.getTime()) <= 7 * 86_400_000 ? 86_400_000 : 7 * 86_400_000;
    const out: Record<string, unknown>[] = [];
    const n = Math.ceil((until.getTime() - since.getTime()) / size) - 1, envs = statsFleet().envs;
    for (let end = until.getTime(), i = 0; end > since.getTime(); end -= size, i++) {
      const start = Math.max(since.getTime(), end - size);
      const b: Record<string, unknown> = { since: new Date(start).toISOString(), until: new Date(end).toISOString(), days: Math.ceil((end - start) / 86_400_000) };
      if (end - start < size) b.partial = true;
      if (i !== 1) {
        const k = i ? i - 1 : 0;
        b.metrics = { breakingCaught: { distinct: 2 + (i % 5), runs: 5 + i }, consumersProtected: { count: 3 * (i % 4), unknown: i % 2, ...(i === 2 ? { partial: 1 } : {}) },
          gates: { clear: 30 + i, blocked: 2 + (i % 3), unverified: i % 2 },
          environments: envs.map((e) => partOf(e, k, n)).filter((e) => e.operations) };
      }
      // The gap week holds no coverage record either, so the coverage line breaks there.
      if (!restricted && i !== 1) b.coverage = coverageOf(new Date(end - 3_600_000), i === 2);
      out.push(b);
    }
    return out.reverse();
  };
  // The window totals. One breaking change ran in two buckets and its gate cleared in
  // both, so it counts once in the window and once in each bucket.
  const windowTotals = (buckets: Record<string, unknown>[]) => {
    const ms = buckets.flatMap((b) => (b.metrics ? [b.metrics as { breakingCaught: { distinct: number; runs: number }; gates: Record<string, number> }] : []));
    const sum = (f: (m: (typeof ms)[number]) => number) => ms.reduce((n, m) => n + f(m), 0);
    return { breakingCaught: { distinct: sum((m) => m.breakingCaught.distinct) - 1, runs: sum((m) => m.breakingCaught.runs) },
      gates: { clear: sum((m) => m.gates.clear) - 1, blocked: sum((m) => m.gates.blocked), unverified: sum((m) => m.gates.unverified) } };
  };
  const statsTotal = (metric: string, env: string, query: string): number => {
    const rate = (RATES as readonly string[]).includes(metric) ? metric as (typeof RATES)[number] : '';
    if (rate) return statsFleet().envs.filter((e) => !env || e.environment === env).reduce((n, e) => n + e[rate].count, 0);
    const e = statsEnvelope('', query), t = windowTotals(statsBuckets(new Date(e.window.since), new Date(e.window.until), false));
    return ({ breaking: t.breakingCaught.distinct, protected: t.gates.blocked, ...t.gates } as Record<string, number>)[metric] ?? 8;
  };
  const statsServiceRow = (i: number) => {
    const metrics = { breakingCaught: { distinct: i % 4, runs: i % 9 }, consumersProtected: { count: i % 13, unknown: 0 }, gates: { clear: i % 20, blocked: i % 3, unverified: i % 2 } };
    if (i === 0) return { label: 'payments-service', href: '/fleet/services/payments-service', metrics };
    return { label: longName(`stats-svc-${i}`), note: i % 7 === 3 ? 'ambiguous: more than one service has this name' : 'not in the current snapshot', metrics };
  };
  const statsRecord = (metric: string, env: string, n: number) => {
    const digest = (k: string) => `sha256:${(hash(k).toString(16).padStart(8, '0')).repeat(8)}`;
    const recordedAt = new Date(Date.now() - (n + 1) * 3_600_000).toISOString();
    const id = `${metric}-${env}-${n}`;
    if ([...RATES, 'leadTime'].includes(metric)) {
      const environment = env || statsFleet().envs[0].environment;
      return { record: { id, version: '1', pactoVersion: '4', kind: 'operation', type: metric === 'rolledBack' ? 'rollback' : 'promote', outcome: metric === 'held' ? 'Held' : 'Failed',
        service: n === 0 ? 'payments-service' : longName(`stats-svc-${n}`), environment, recordedAt },
        stage: { namespace: longName('stats-team-namespace'), name: longName(`stats-stage-${n}`), environment },
        ...(n === 0 ? { service: { label: 'payments-service', href: '/fleet/services/payments-service' } } : { note: 'not in the current snapshot' }) };
    }
    if (metric === 'coverage') return { record: { id, version: '1', pactoVersion: '4', kind: 'coverage', service: '_fleet', recordedAt } };
    const verdict = ['clear', 'unverified'].includes(metric) ? metric : 'blocked';
    // The two newest blocked records name payments-service, so each opens its change analysis.
    const placed = n === 0 || (n === 1 && verdict === 'blocked');
    const [oldRevision, newRevision] = [digest(`${id}-old`), digest(`${id}-new`)];
    const change = placed && verdict === 'blocked'
      ? { change: { href: `/fleet/changes/payments-service?old=${encodeURIComponent(oldRevision)}&new=${encodeURIComponent(newRevision)}` } } : {};
    return { record: { id, version: '1', pactoVersion: '4', kind: 'impact', service: placed ? 'payments-service' : longName(`stats-svc-${n}`), classification: 'BREAKING',
      oldRevision, newRevision, gate: { verdict }, recordedAt },
      ...(placed ? { service: { label: 'payments-service', href: '/fleet/services/payments-service' }, ...change } : { note: 'not in the current snapshot' }) };
  };
  const statsAnswer = (route: string, query: string): Record<string, unknown> => {
    const mode = statsMode(), q = new URLSearchParams(query);
    const env = statsEnvelope(mode, query);
    const offset = Math.max(0, Number(q.get('offset') ?? q.get('servicesOffset') ?? 0) || 0);
    if (route === '/api/stats/records') {
      const metric = q.get('metric') ?? '', environment = q.get('environment') ?? '';
      const total = mode === 'none' ? 0 : statsTotal(metric, environment, query);
      const limit = Math.min(100, Number(q.get('limit') ?? 25) || 25);
      return { ...env, total, items: Array.from({ length: Math.max(0, Math.min(limit, total - offset)) }, (_, j) => statsRecord(metric, environment, offset + j)) };
    }
    if (mode === 'none') return { ...env, runs: 0, distinctChanges: 0, environmentOrder: 'name', environments: [], services: [], servicesTotal: 0, buckets: [], environmentsBy: { owner: [], domain: [] }, leadTimeGroups: [] };
    const sOffset = Math.max(0, Number(q.get('servicesOffset') ?? 0) || 0), sLimit = Math.min(100, Number(q.get('servicesLimit') ?? 25) || 25);
    const restricted = mode === 'restricted', f = statsFleet();
    const buckets = statsBuckets(new Date(env.window.since), new Date(env.window.until), restricted);
    const totals = windowTotals(buckets);
    const attribution = { provenance: 'inferred', asOf: env.asOf, snapshotId: 'fixture-snapshot', ...(f.attributionPartial ? { partial: true } : {}) };
    return {
      ...env, runs: 1180, distinctChanges: 860, environmentOrder: 'stages', environments: f.envs,
      totals: { ...totals, consumersProtected: { count: 120, unknown: 4, partial: 1 }, environments: f.envs.map(({ upstream: _u, parents: _p, depth: _d, chainEnd: _c, cycle: _y, unordered: _o, ...m }) => m) },
      services: Array.from({ length: Math.max(0, Math.min(sLimit, 2000 - sOffset)) }, (_, j) => statsServiceRow(sOffset + j)), servicesTotal: 2000,
      environmentsBy: { owner: environmentsBy(f, f.owners), domain: environmentsBy(f, f.domains), attribution },
      leadTimeGroups: leadTimeGroups(f.envs, buckets), buckets,
      coverageNow: restricted ? { ...coverageOf(new Date(), false), services: 300, knowledge: { services: 300, described: 200, withGaps: 60, unknown: 40, notDeployed: 12 } } : coverageOf(new Date(), true),
    };
  };
  const unreadableRecord = (route: string, doc: Record<string, unknown>) => {
    const svc = doc.service as Record<string, unknown> | undefined;
    if (route === '/api/fleet/entities/service' && svc) svc.inferredFields = { items: [], count: 0, total: 0, truncated: false, unreadable: true };
    const rev = doc.revision as Record<string, unknown> | undefined;
    if (route === '/api/fleet/entities/revision' && rev) {
      rev.inferred = ((rev.inferred as { field: string }[] | undefined) ?? []).filter((f) => f.field === 'pactoVersion' || f.field === 'service.name');
      rev.inferredTotal = (rev.inferred as unknown[]).length;
      rev.inferredUnreadable = true;
    }
  };
  // Read before amend renames the service; applied after amend scales the rows.
  const declaredRevision = (route: string, doc: Record<string, unknown>) => {
    const rev = doc.revision as Record<string, unknown> | undefined;
    return route === '/api/fleet/entities/revision' && !!rev && String((rev.service as { key?: string } | undefined)?.key ?? '').endsWith('newsletter-service');
  };
  const clusterFacts = (doc: Record<string, unknown>) => {
    const rev = doc.revision as Record<string, unknown>;
    if (flags.__pactoClusterFacts === 'dropped') {
      Object.assign(rev, { inferred: [], inferredTotal: 0,
        declaration: { state: 'declared', signatureChecked: false, claimedSubject: `${'mallory'.repeat(10)}@unverified.example.com`, inferredFromHidden: true } });
      return;
    }
    if (flags.__pactoClusterFacts === 'notAllowed') {
      Object.assign(rev, { inferred: [], inferredTotal: 0,
        declaration: { state: 'declared', signatureChecked: true, claimedSubject: 'mallory@gmail.com', baseline: '1.4.0',
          notAccepted: 'the installation signer policy lists no issuer https://accounts.google.com', inferredFrom: 'k8s:newsletter/Deployment/newsletter-service' } });
      return;
    }
    const wide = (s: string) => `${s}-${'x'.repeat(Math.max(0, 79 - s.length))}`;
    const workload = `cluster:${wide('newsletter')}/Deployment/newsletter-service`;
    rev.inferred = Array.from({ length: 9 }, (_, i) => (i % 3 === 2
      ? { field: `configuration.${wide(`KEY_${i}`)}`, value: wide(`value-${i}`), source: `deploy/deployment.yaml:${i + 1}`, confidence: 'likely', provenance: 'inferred' }
      : { field: `dependencies[${wide(`callee-${i}`)}]`, value: wide(`callee-${i}`), source: workload, provenance: 'inferred',
        asOf: '2026-11-17T10:00:00Z', completeness: i % 2 ? 'partial' : 'complete', ...(i === 0 ? { proposed: '^2.0.0' } : {}) }));
    rev.inferredTotal = 9;
    rev.inferredCoverage = [{ source: workload, state: 'complete' }, { source: 'cluster:runtime-calls', state: 'partial', reason: 'a node went unobserved' }];
    rev.inferredHidden = { services: 2, callers: 5 };
    rev.declaration = { state: 'declared', signatureChecked: true, signer: { subject: `${'ana'.repeat(25)}@acme.example.com`, issuer: 'https://accounts.google.com', class: 'human' },
      inferredFrom: `k8s:${wide('newsletter')}/Deployment/newsletter-service`, baseline: '1.4.0', accepted: [`dependencies[${wide('callee-0')}] ^2.0.0`] };
  };
  // Range proposals, the accept plan and announced breaks (508-S7): the published demo
  // bundles propose nothing and stay immutable, so the fixture puts them on the page.
  const SNAPSHOT_SRC = `snapshot:ghcr.io/trianalab/${longName('pacto-target-snapshots')}@sha256:${'5e'.repeat(32)}`;
  const acceptProposals = (route: string, doc: Record<string, unknown>) => {
    const svc = doc.service as Record<string, unknown> | undefined;
    if (route !== '/api/fleet/entities/service' || !svc) return;
    const items = Array.from({ length: 3 }, (_, i) => ({ field: `dependencies[${longName(`provider-${i}`)}].compatibility`, value: 'compatibility unknown',
      proposed: `^${i + 2}.${i}.0`, source: SNAPSHOT_SRC, confidence: 'likely', provenance: 'inferred' }));
    svc.inferredFields = { items, count: items.length, total: items.length, truncated: false };
  };
  const acceptAnswer = () => {
    if (flags.__pactoAccept === 'refused') {
      return { refusal: { code: 'AcceptNothingProposed', message: `the newest published version of ${longName('payments-service')} proposes no dependency range to accept`,
        command: `pacto ci --publish ./services/${longName('payments-service')}` } };
    }
    const repo = `ghcr.io/trianalab/${longName('payments-service')}`;
    const intent = `sha256:${'a7'.repeat(32)}`;
    return {
      command: `pacto accept oci://${repo} --proposed --intent ${intent}`,
      plan: { service: `trianalab/${longName('payments-service')}`, version: '2.0.1', subject: `${repo}@sha256:${'c0'.repeat(32)}`, intentDigest: intent,
        ranges: Array.from({ length: 3 }, (_, i) => ({ field: `dependencies[${longName(`provider-${i}`)}].compatibility`, value: `^${i + 2}.${i}.0`, proposed: `^${i + 2}.${i}.0`, source: SNAPSHOT_SRC })),
        whoMayAccept: `${longName('ana')}@acme.example (the DRI of ${longName('payments-service')})`,
        existing: [{ state: 'routed', by: `${longName('bob')}@acme.example`, issuer: 'https://oauth2.sigstore.dev/auth', record: `${repo}@sha256:${'d1'.repeat(32)}`,
          reason: `waiting for the owner of ${longName('payments-service')}` }] },
    };
  };
  const breakStates = (method: string, route: string, doc: Record<string, unknown>) => {
    const consumers = doc.consumers as { items?: Record<string, unknown>[] } | undefined;
    if (method !== 'POST' || route !== '/api/fleet/impact' || !consumers?.items) return;
    const states = [
      { state: 'accepted', by: `${longName('ana')}@acme.example`, record: `ghcr.io/trianalab/${longName('reports')}@sha256:${'e2'.repeat(32)}`, expires: '2026-12-20' },
      { state: 'announced', expires: '2026-12-20', route: `${longName('ana')}@acme.example (the DRI of ${longName('reports')})` },
      { state: 'expired', reason: `the announcement expired on 2026-10-01 at ${longName('ledger')}` },
    ];
    let n = 0;
    for (const c of consumers.items) if (c.direct) c.break = states[n++ % states.length];
    doc.ranges = { scope: `consumers of ${longName('payments-service')}`, edges: 58, declared: 41, accepted: 6, unknownTotal: 11,
      unknown: Array.from({ length: 11 }, (_, i) => ({ from: longName(`consumer-${i}`), to: longName('payments-service'), proposed: `^2.${i}.0` })) };
    doc.notes = [`POLICY_WARNING: ${longName('payments-service')} -> ${longName('reports')} range unknown, 1 consumer (policy unknown-range=warn)`];
    // A possible break on an inferred interface, with long values: one muted span in What changed.
    doc.advisories = [{ interface: 'api', kind: 'openapi', source: `services/${longName('payments-service')}/openapi/${longName('v1')}.yaml:1`,
      declare: `interfaces:\n  - name: api\n    type: openapi\n    ref: openapi/${longName('v1')}.yaml\n`,
      changes: Array.from({ length: 5 }, (_, i) => ({ path: `openapi.paths[/${longName('payments')}].methods[GET].parameters[${longName(`currency-${i}`)}]`,
        type: 'added', classification: 'BREAKING', reason: `parameter ${longName(`currency-${i}`)} added as required` })),
      consumers: Array.from({ length: 12 }, (_, i) => ({ service: longName(`consumer-${i}`), confidence: 'inferred', compatibility: 'unknown' })) }];
  };
  const wrapped: Serve = async (method, path, body) => {
    body = engineBody(path, body);
    const route = path.split('?')[0];
    if (method === 'GET' && (route === '/api/stats' || route === '/api/stats/records') && statsMode() !== 'engine') {
      if (statsMode() === 'off') return { status: 404, contentType: 'application/problem+json', body: JSON.stringify({ status: 404, title: 'Not Found' }) };
      return { status: 200, contentType: 'application/json', body: JSON.stringify(statsAnswer(route, path.split('?')[1] ?? '')) };
    }
    if (flags.__pactoStalePropose && method === 'POST' && path === '/api/delivery/operations' && body) {
      const plan = await (real as Serve)('POST', '/api/delivery/plan', body);
      return { status: 409, contentType: 'application/json', body: JSON.stringify({ status: 409, code: 'PlanStale',
        detail: 'the Stage moved since you planned; review the fresh plan and propose again', plan: JSON.parse(plan.body) }) };
    }
    if (flags.__pactoAccept && method === 'POST' && route === '/api/fleet/accept') {
      return { status: 200, contentType: 'application/json', body: JSON.stringify(acceptAnswer()) };
    }
    // Planning reads acceptances, so only a local viewer or a signed-in person is
    // offered it: the plan routes run as a local viewer, 'forbidden' as an open
    // dashboard where nobody signs in.
    if (flags.__pactoAccept && flags.__pactoAccept !== 'breaks' && method === 'GET' && route === '/api/session') {
      const local = flags.__pactoAccept !== 'forbidden';
      return { status: 200, contentType: 'application/json', body: JSON.stringify({ mode: local ? 'local' : 'open', login: false, signedIn: local }) };
    }
    const ambiguousQuery = new URLSearchParams(path.split('?')[1] ?? '');
    if (flags.__pactoAmbiguousTarget && method === 'GET' && ambiguousQuery.get('key') === 'scale-ambiguous/kubernetes/payments'
      && (route === '/api/fleet/entities/target' || (route === '/api/fleet/neighborhood' && ambiguousQuery.get('kind') === 'target'))) {
      const errors = Array.from({ length: 6 }, (_, i) => {
        const uid = `${(0x9f1c4e2a + i * 7919).toString(16)}-7f1a-4c55-9d1e-2b8a4e6f0a1${i}`;
        return { location: 'candidates', value: `${uid}/scale-ambiguous/kubernetes/payments`, message: `${longName(`cluster-${i}`)} / scale-ambiguous / payments` };
      });
      // A target with no known cluster keeps a key of its own under the `_` component.
      errors.push({ location: 'candidates', value: '_/scale-ambiguous/kubernetes/payments', message: 'scale-ambiguous/kubernetes/payments (cluster unknown)' });
      return { status: 409, contentType: 'application/problem+json', body: JSON.stringify({ status: 409, title: 'Conflict',
        detail: 'this target key names targets in more than one cluster; choose one', errors }) };
    }
    // The demo's `stripe` has only inferred sources: its declared parcel-carrier answers,
    // and amend renames it.
    const asked = route === '/api/fleet/entities/external' && new URLSearchParams(path.split('?')[1] ?? '').get('key') === 'stripe'
      ? path.replace('key=stripe', 'key=parcel-carrier') : engineName(path);
    const res = await (real as Serve)(method, asked, body);
    if (res.status !== 200 || !(res.contentType || '').includes('json')) return res;
    let doc: Record<string, unknown>;
    try { doc = JSON.parse(res.body); } catch { return res; }
    if (flags.__pactoInferredUnreadable) unreadableRecord(route, doc);
    const declared = !!flags.__pactoClusterFacts && declaredRevision(route, doc);
    try { amend(method, path, doc, body); } catch (e) {
      // An override that throws would leave the route unscaled and the gate green:
      // layoutProblems reports it.
      console.error('scale fixture', path, e);
      const w = window as unknown as { __pactoScaleErrors?: string[] };
      (w.__pactoScaleErrors ??= []).push(`scale fixture ${path}: ${e}`);
      return res;
    }
    if (declared) clusterFacts(doc);
    if (flags.__pactoMultiCluster && method === 'GET' && route === '/api/fleet/entities' && doc.meta) {
      const n63 = (s: string) => `${s}-${'x'.repeat(63)}`.slice(0, 63);
      (doc.meta as Record<string, unknown>).clusters = [
        { id: '5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11', name: n63('production-eu-west-1'), nameSource: 'context', status: 'available', targets: 12 },
        { id: '9f1c4e2a-7f1a-4c55-9d1e-2b8a4e6f0a12', name: n63('production-us-east-2'), nameSource: 'context', status: 'available', access: 'no-access', targets: 0 },
        { id: '9f1c4e2a-7f1a-4c55-9d1e-2b8a4e6f0a13', name: n63('production-ap-south-1'), nameSource: 'context', status: 'stale', access: 'review-failed', targets: 0 },
        // Every other state, with the long values a 320px sweep must hold: a partial and a
        // stale cluster with no mark (warn tone), one nobody can review, and a context
        // whose identity read failed (no id, named by its source id, err tone).
        { id: '9f1c4e2a-7f1a-4c55-9d1e-2b8a4e6f0a14', name: n63('production-sa-east-1'), nameSource: 'context', status: 'partial', targets: 4 },
        { id: '9f1c4e2a-7f1a-4c55-9d1e-2b8a4e6f0a15', name: n63('production-me-central-1'), nameSource: 'context', status: 'available', access: 'not-reviewable', targets: 0 },
        { id: '', name: n63('context-staging-unidentified'), status: 'unavailable', targets: 0 },
        { id: '9f1c4e2a-7f1a-4c55-9d1e-2b8a4e6f0a16', name: n63('production-ca-central-1'), nameSource: 'context', status: 'stale', targets: 2 },
      ];
      // More clusters exist than meta.clusters lists (MaxMetaClusters).
      (doc.meta as Record<string, unknown>).clustersTruncated = true;
    }
    if (flags.__pactoAccept === 'breaks') breakStates(method, route, doc);
    else if (flags.__pactoAccept) acceptProposals(route, doc);
    return { ...res, body: JSON.stringify(doc) };
  };
  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

/** Forces the wide font CI renders (DejaVu stands behind Verdana) and the final frame. */
export async function gateMode(page: Page): Promise<void> {
  await page.addStyleTag({ content: '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}' });
  await page.evaluate(() => { document.documentElement.dataset.narrate = 'final'; });
}

/**
 * layoutProblems runs every G1 check on the page as it is and returns one line per
 * problem. Nothing is excluded.
 */
export async function layoutProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = [...((window as unknown as { __pactoScaleErrors?: string[] }).__pactoScaleErrors ?? [])];
    const root = document.querySelector('#app') ?? document.body;
    const say = (el: Element | null) => {
      if (!el) return '?';
      const id = el.id ? `#${el.id}` : '';
      const cls = typeof el.className === 'string' && el.className ? '.' + el.className.trim().split(/\s+/).slice(0, 2).join('.') : '';
      const t = (el.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 32);
      return `<${el.tagName.toLowerCase()}${id}${cls}> "${t}"`;
    };
    const hiddenByA11y = (el: Element) => {
      for (let p: Element | null = el; p && p !== document.body; p = p.parentElement) {
        const cs = getComputedStyle(p);
        if (cs.display === 'none' || cs.visibility === 'hidden' || Number(cs.opacity) === 0) return true;
        if (cs.position === 'absolute' && (cs.clip === 'rect(0px, 0px, 0px, 0px)' || cs.clipPath === 'inset(50%)')) return true;
        // A closed disclosure shows only its own summary: a nested disclosure's summary inside it is hidden too.
        if (p.tagName === 'DETAILS' && !(p as HTMLDetailsElement).open && !p.querySelector(':scope > summary')?.contains(el)) return true;
      }
      return false;
    };

    // Horizontal page scroll.
    const doc = document.documentElement;
    // An open note (HelpTip) is clipped by html's overflow-x: clip, so one that runs past
    // the viewport is cut with no way to read the rest.
    for (const n of root.querySelectorAll('[role="note"]:not([hidden])')) {
      const r = n.getBoundingClientRect();
      if (r.width && (r.left < -1 || r.right > doc.clientWidth + 1)) out.push(`${say(n)} runs past the viewport (${Math.round(r.left)} to ${Math.round(r.right)} of ${doc.clientWidth}px)`);
    }
    if (doc.scrollWidth > doc.clientWidth + 1) out.push(`the page scrolls sideways by ${doc.scrollWidth - doc.clientWidth}px`);

    // Text runs: one box per line of each visible text node, clipped by every ancestor
    // that clips, so text scrolled or cut out of view does not count as on screen.
    type Box = { node: Text; el: Element; l: number; t: number; r: number; b: number };
    const boxes: Box[] = [];
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const fonts = new Set<Element>();
    let lone = '';
    for (let n = walker.nextNode() as Text | null; n; n = walker.nextNode() as Text | null) {
      const el = n.parentElement;
      if (!el || !(n.textContent ?? '').trim()) continue;
      if (el.closest('script, style, noscript, template, option, canvas, [popover]:not(:popover-open), dialog:not([open])')) continue;
      if (hiddenByA11y(el)) continue;
      const range = document.createRange();
      range.selectNodeContents(n);
      const rects = [...range.getClientRects()].filter((r) => r.width > 0.5 && r.height > 0.5);
      if (!rects.length) continue;
      if (/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(n.textContent ?? '')) lone = say(el);
      // SPEC 22 r2, squeezed: a word broken down to one or two characters per line
      // (a column squeezed to a sliver renders its header one letter per line).
      if (rects.length > 1) {
        const txt = n.textContent ?? '';
        let best = { at: 0, len: 0 };
        for (const m of txt.matchAll(/\S+/g)) if (m[0].length > best.len) best = { at: m.index, len: m[0].length };
        if (best.len >= 4) {
          const wr = document.createRange();
          wr.setStart(n, best.at);
          wr.setEnd(n, best.at + best.len);
          const lines = new Set([...wr.getClientRects()].filter((r) => r.width > 0.5).map((r) => Math.round(r.top))).size;
          if (lines > 1 && best.len / lines < 3) out.push(`squeezed text, ${lines} lines for a ${best.len}-character word: ${say(el)}`);
        }
      }
      fonts.add(el);
      let clip = { l: -1e9, t: -1e9, r: 1e9, b: 1e9 };
      // The tightest horizontal cut by a box that does not scroll: text past it is lost.
      let cut = { l: -1e9, r: 1e9 };
      let ellipsis = false;
      // Text inside a box that scrolls sideways is reachable, whatever clips above it.
      let scrolls = false;
      for (let p: Element | null = el; p && p !== document.documentElement; p = p.parentElement) {
        const cs = getComputedStyle(p);
        if (cs.overflowX !== 'visible' || cs.overflowY !== 'visible') {
          const q = p.getBoundingClientRect();
          clip = { l: Math.max(clip.l, q.left), t: Math.max(clip.t, q.top), r: Math.min(clip.r, q.right), b: Math.min(clip.b, q.bottom) };
          if (cs.overflowX === 'auto' || cs.overflowX === 'scroll') scrolls = true;
          else if (!scrolls && (cs.overflowX === 'hidden' || cs.overflowX === 'clip')) {
            const pad = (v: string) => parseFloat(v) || 0;
            cut = { l: Math.max(cut.l, q.left + pad(cs.borderLeftWidth)), r: Math.min(cut.r, q.right - pad(cs.borderRightWidth)) };
            if (cs.textOverflow === 'ellipsis') ellipsis = true;
          }
        }
      }
      if (rects.some((r) => (r.right > cut.r + 1 || r.left < cut.l - 1) && Math.min(r.right, cut.r) - Math.max(r.left, cut.l) > 0.5)) {
        if (!ellipsis) out.push(`text cut with no ellipsis: ${say(el)}`);
        else if (!el.closest('[title]')) out.push(`ellipsized text with no title: ${say(el)}`);
      }
      for (const r of rects) {
        const b = { node: n, el, l: Math.max(r.left, clip.l), t: Math.max(r.top, clip.t), r: Math.min(r.right, clip.r), b: Math.min(r.bottom, clip.b) };
        if (b.r - b.l > 0.5 && b.b - b.t > 0.5) boxes.push(b);
      }
    }
    if (lone) out.push(`a lone surrogate (a cut emoji) in ${lone}`);

    // SPEC 22 r2: no table column holding words is narrower than 6ch.
    for (const cell of root.querySelectorAll('th, td')) {
      if (hiddenByA11y(cell) || !cell.getClientRects().length) continue;
      if ((cell.textContent ?? '').trim().length < 3) continue;
      const cs = getComputedStyle(cell);
      const ctx = document.createElement('canvas').getContext('2d');
      if (!ctx) continue;
      ctx.font = `${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
      const ch = ctx.measureText('0').width;
      const w = cell.getBoundingClientRect().width - (parseFloat(cs.paddingLeft) || 0) - (parseFloat(cs.paddingRight) || 0);
      if (w < 6 * ch - 0.5) out.push(`a table column ${w.toFixed(0)}px wide, under 6ch (${(6 * ch).toFixed(0)}px): ${say(cell)}`);
    }

    // Nothing renders below --text-xs (12.19px).
    for (const el of fonts) {
      const fs = parseFloat(getComputedStyle(el).fontSize);
      if (fs < 12.1) out.push(`text under 12.19px (${fs}px): ${say(el)}`);
    }

    // No two text runs intersect. Rows are bucketed so a 500-row page stays fast.
    const rows = new Map<number, Box[]>();
    for (const x of boxes) {
      for (let k = Math.floor(x.t / 40); k <= Math.floor(x.b / 40); k++) {
        if (!rows.has(k)) rows.set(k, []);
        rows.get(k)!.push(x);
      }
    }
    const seen = new Set<string>();
    const idx = new Map(boxes.map((b, i) => [b, i]));
    // A text box is the nearest box that is not inline: runs inside one inline
    // formatting context are one sentence, not two boxes.
    const boxOf = new Map<Element, Element>();
    // An atomic inline box (an inline-flex link, an inline-block badge) placed in running
    // text is a word of that sentence too, spaced by the sentence's own word space; a
    // flex or grid item is blockified and stays a box of its own.
    const flows = (p: Element) => {
      const d = getComputedStyle(p).display;
      if (/^(inline|contents)$/.test(d)) return true;
      if (!/^inline-/.test(d) || !p.parentElement) return false;
      return !/(flex|grid)$/.test(getComputedStyle(p.parentElement).display);
    };
    const textBox = (el: Element): Element => {
      if (boxOf.has(el)) return boxOf.get(el)!;
      let p: Element = el;
      while (p.parentElement && flows(p)) p = p.parentElement;
      boxOf.set(el, p);
      return p;
    };
    for (const list of rows.values()) {
      for (let i = 0; i < list.length; i++) {
        for (let j = i + 1; j < list.length; j++) {
          const a = list[i], b = list[j];
          if (a.node === b.node) continue;
          const key = `${idx.get(a)}:${idx.get(b)}`;
          if (seen.has(key)) continue;
          seen.add(key);
          const ix = Math.min(a.r, b.r) - Math.max(a.l, b.l);
          const iy = Math.min(a.b, b.b) - Math.max(a.t, b.t);
          if (ix > 1 && iy > 1) out.push(`text overlaps text: ${say(a.el)} x ${say(b.el)}`);
          else if (iy > 4 && textBox(a.el) !== textBox(b.el)) {
            // C4: two text boxes side by side on one line keep a clear 8px gap.
            const gap = Math.max(a.l, b.l) - Math.min(a.r, b.r);
            if (gap < 7.5 && gap > -1) out.push(`a ${gap.toFixed(1)}px gap, under 8px, between text boxes: ${say(a.el)} / ${say(b.el)}`);
          }
        }
      }
    }

    // Innermost first, so a spill is reported once, on the box it leaves.
    const spilt = new Set<Text>();
    for (const el of [...root.querySelectorAll('*')].reverse()) {
      if (el instanceof SVGElement) continue;
      const cs = getComputedStyle(el);
      if (cs.display === 'inline' || cs.display === 'none' || el.clientWidth === 0) continue;
      const over = el.scrollWidth - el.clientWidth;
      if (over <= 1) continue;
      if (hiddenByA11y(el)) continue;
      const ownText = [...el.childNodes].some((c) => c.nodeType === 3 && (c.textContent ?? '').trim()) || el.querySelector(':scope > bdi');
      if (cs.overflowX === 'visible') {
        // Text spilling out of a box that does not clip it. A touch target widened by
        // a negative margin overflows too, but carries no text past the edge.
        const q = el.getBoundingClientRect();
        const spill = boxes.find((x) => el.contains(x.el) && (x.r > q.right + 1 || x.l < q.left - 1));
        if (spill && !spilt.has(spill.node)) {
          spilt.add(spill.node);
          out.push(`${say(el)} lets text spill out of its box: ${say(spill.el)}`);
        }
      } else if (cs.overflowX === 'hidden' || cs.overflowX === 'clip') {
        if (!ownText) continue;
        // Ellipsized text says where its full value is.
        if (cs.textOverflow === 'ellipsis' && !el.closest('[title]')) out.push(`ellipsized text with no title: ${say(el)}`);
      }
    }

    // A shown identity is its title, or an elision of it (head and tail are its ends).
    // A bdi marked data-format is a deliberate rendering (a short digest, an account
    // as namespace/name), not a cut name.
    for (const bdi of root.querySelectorAll('bdi:not([data-format])')) {
      if (hiddenByA11y(bdi)) continue;
      const t = bdi.textContent ?? '';
      const parts = t.split('…');
      const elided = parts.length > 1;
      // The title that holds an identity's full value sits on an element around it. A
      // title on a box that also holds other words speaks for the identity only when the
      // identity is elided; otherwise it may describe the whole box.
      const owner = bdi.closest('[title]') as HTMLElement | null;
      const own = !!owner && (owner.textContent ?? '').trim() === t.trim();
      if (!owner || (!own && !elided)) {
        if (elided) out.push(`elided identity "${t.slice(0, 40)}" has no title with its full value`);
        continue;
      }
      const title = owner.title;
      // C6: whole, or head + … + tail (with the siblings' distinguishing middle between two
      // ellipses): the head is the title's start (4 or more characters), the tail its end
      // (5 or more), each middle piece sits in order between them, and a name of 12 code
      // points or fewer is never elided.
      let ok = t === title;
      if (elided) {
        const cp = (x: string) => [...x].length;
        const head = parts[0], tail = parts[parts.length - 1];
        let at = head.length;
        ok = cp(title) > 12 && cp(head) >= 4 && cp(tail) >= 5 && title.startsWith(head) && title.endsWith(tail)
          && parts.slice(1, -1).every((m) => { const i = title.indexOf(m, at); if (i < 0 || !m) return false; at = i + m.length; return true; })
          && at < title.length - tail.length;
      }
      if (!ok) out.push(`identity "${t.slice(0, 40)}" is neither its title nor a head, middle and tail elision of it: "${title.slice(0, 40)}"`);
    }

    // L1 (r2), at 1280: a data figure's drawing and aside together fill at least 60% of
    // its body; a fixed-width drawing that left the right half of the card empty failed.
    // A row of steps turned vertical (S1 r3, data-w="col") is narrow by that rule.
    if (window.innerWidth >= 1200 && window.innerWidth <= 1440) {
      for (const f of root.querySelectorAll('[data-fig].fig--data:not([data-compact]):not([data-w="col"])')) {
        const body = f.querySelector('.fig-body');
        if (!body || !body.getClientRects().length) continue;
        const q = body.getBoundingClientRect();
        let l = Infinity, t = Infinity, r = -Infinity, b = -Infinity;
        const take = (x: DOMRect) => { if (x.width < 0.5 || x.height < 0.5) return; l = Math.min(l, x.left); t = Math.min(t, x.top); r = Math.max(r, x.right); b = Math.max(b, x.bottom); };
        for (const m of body.querySelectorAll('.mk')) take(m.getBoundingClientRect());
        const tw = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);
        for (let n = tw.nextNode(); n; n = tw.nextNode()) {
          if (!(n.textContent ?? '').trim() || !n.parentElement || hiddenByA11y(n.parentElement)) continue;
          const rg = document.createRange(); rg.selectNodeContents(n);
          for (const x of rg.getClientRects()) take(x);
        }
        if (r < l || q.width * q.height === 0) continue;
        const filled = ((Math.min(r, q.right) - Math.max(l, q.left)) * (Math.min(b, q.bottom) - Math.max(t, q.top))) / (q.width * q.height);
        if (filled < 0.6) out.push(`${say(f)} leaves ${Math.round((1 - filled) * 100)}% of its card empty, more than 40% (L1)`);
      }
    }

    // Figures (the [data-fig] drawings): at most 20 marks, and every number in a lane,
    // station or aside is a filter link or says it has none.
    for (const f of root.querySelectorAll('[data-fig]')) {
      const marks = [...f.querySelectorAll('.mk')].filter((m) => m.getClientRects().length);
      if (marks.length > 20) out.push(`${say(f)} draws ${marks.length} marks, more than 20`);
      // A mark never covers text (a "+N" count chip is itself the mark).
      const words = boxes.filter((x) => f.contains(x.el) && !x.el.closest('.mk, .on-mark'));
      for (const m of marks) {
        // A mark is what it draws: its own box and every child that overflows it (a
        // stacked segment taller than its column spills out of a fixed-height box).
        const r = [m, ...m.querySelectorAll('*')].map((e) => e.getBoundingClientRect()).filter((q) => q.width > 0.5 && q.height > 0.5)
          .reduce((a, q) => ({ left: Math.min(a.left, q.left), top: Math.min(a.top, q.top), right: Math.max(a.right, q.right), bottom: Math.max(a.bottom, q.bottom) }),
            { left: 1e9, top: 1e9, right: -1e9, bottom: -1e9 });
        if (r.right < r.left) continue;
        const hit = words.find((x) => Math.min(x.r, r.right) - Math.max(x.l, r.left) > 1 && Math.min(x.b, r.bottom) - Math.max(x.t, r.top) > 1);
        if (hit) { out.push(`a mark covers text in a figure: ${say(hit.el)}`); continue; }
        // C4: a mark and the text straight above or below it keep a clear 8px gap.
        const near = words.find((x) => Math.min(x.r, r.right) - Math.max(x.l, r.left) > 1 && Math.max(x.t - r.bottom, r.top - x.b) < 7.5);
        if (near) out.push(`a ${Math.max(near.t - r.bottom, r.top - near.b).toFixed(1)}px gap, under 8px, between a mark and text: ${say(near.el)}`);
      }
      for (const n of f.querySelectorAll('.lane, .station, .fig-aside')) {
        const tw = document.createTreeWalker(n, NodeFilter.SHOW_TEXT);
        for (let t = tw.nextNode(); t; t = tw.nextNode()) {
          if (/^\s*\+?\d[\d,]*\s*$/.test(t.textContent ?? '') && !t.parentElement?.closest('a, [data-nofilter]')) {
            out.push(`a figure number is neither a link nor data-nofilter: ${say(t.parentElement)}`);
          }
        }
      }
    }

    // One err ring per page at most (the alarm, or a figure's single ring). A ring is
    // any element (or its ::before / ::after) whose outer box-shadow is drawn in the
    // err colour, whatever its class, plus the ring classes themselves.
    // Colours come back as rgb()/rgba(), or as color(srgb …) when a color-mix() is
    // involved; both reduce to "r, g, b" in 0..255.
    const rgbOf = (str: string): string => {
      const m = str.match(/rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)/);
      if (m) return [m[1], m[2], m[3]].map((v) => Math.round(Number(v))).join(', ');
      const c = str.match(/color\(srgb\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)/);
      return c ? [c[1], c[2], c[3]].map((v) => Math.round(Number(v) * 255)).join(', ') : '';
    };
    const probe = document.createElement('span');
    probe.style.color = 'var(--c-err)';
    document.body.appendChild(probe);
    const errRgb = rgbOf(getComputedStyle(probe).color);
    probe.remove();
    const errRing = (shadow: string) => !!errRgb && shadow !== 'none' && shadow.split(/,(?![^(]*\))/)
      .some((one) => !/\binset\b/.test(one) && rgbOf(one) === errRgb);
    const rings = [...root.querySelectorAll('*')].filter((el) =>
      (el.matches('.is-alarm, .ring, [data-ring]') || [null, '::before', '::after'].some((pe) => errRing(getComputedStyle(el, pe).boxShadow)))
      && el.getClientRects().length > 0 && !hiddenByA11y(el));
    if (rings.length > 1) out.push(`${rings.length} err rings on one page, more than 1: ${rings.slice(0, 3).map(say).join(', ')}`);

    return [...new Set(out)];
  });
}

/**
 * densityProblems is L3 (r5), for list views at 1280x900: a data figure above the list
 * either leaves the list's first row (a table header or a list row) within 600px of the
 * page top or offers a Compact toggle (aria-pressed) that folds the drawing away.
 */
export async function densityProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const fig = document.querySelector('main [data-fig].fig--data');
    if (!fig) return [];
    const after = (el: Element) => !!(fig.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING) && !fig.contains(el);
    const first = [...document.querySelectorAll('main thead, main .pl-row')].find((el) => after(el) && el.getClientRects().length);
    if (!first) return [];
    const top = Math.round(first.getBoundingClientRect().top + window.scrollY);
    const toggle = [...fig.querySelectorAll('.fig-head button[aria-pressed]')].some((b) => (b.textContent ?? '').trim() === 'Compact');
    return top > 600 && !toggle ? [`the list starts ${top}px from the page top, under a figure with no Compact toggle (L3)`] : [];
  });
}

/** figureLinks are the links figureLinkTotals follows, each with the total it must land on. */
export async function figureLinks(page: Page): Promise<{ href: string; text: string; want: number }[]> {
  return page.evaluate(() => [...document.querySelectorAll<HTMLAnchorElement>('[data-fig] a')]
    .filter((a) => a.getClientRects().length && (a.hasAttribute('data-lists') || /^(List all [\d,]+|[\d,]+ more)$/.test((a.textContent ?? '').trim())))
    .map((a) => {
      const t = (a.textContent ?? '').trim(), n = Number(t.replace(/[^\d]/g, ''));
      const lists = a.getAttribute('data-lists');
      if (lists != null) return { href: a.getAttribute('href') ?? '', text: t, want: Number(lists) };
      const lane = a.closest('.lane, .station');
      const shown = lane ? [...lane.querySelectorAll('.chip')].filter((c) => c.getClientRects().length).length : 0;
      return { href: a.getAttribute('href') ?? '', text: t, want: t.startsWith('List all') ? n : n + shown };
    }));
}

/**
 * figureLinkTotals follows every "List all N" and "N more" link of a figure (T3, G1 r5)
 * and reads the total of the list it lands on, from the `data-view-total` its pager
 * carries: "List all N" must land on N, "N more" on N plus the chips its lane names,
 * and a link that says how many it lists in `data-lists` (a phase station's "N
 * consumers", where the station's own count is of requests) on that N. A link that lands on no paged list (prose) fails too. It navigates, so call it last.
 */
export async function figureLinkTotals(page: Page): Promise<string[]> {
  const links = await figureLinks(page);
  const out: string[] = [];
  for (const l of links) {
    await page.goto(`/${l.href}`);
    const total = async () => page.evaluate(() => {
      const v = document.querySelector('[data-view-total]')?.getAttribute('data-view-total');
      return v == null ? null : Number(v);
    });
    try {
      await expect.poll(total, { timeout: 20_000 }).toBe(l.want);
    } catch {
      const got = await total();
      out.push(got == null ? `"${l.text}" lands on no paged list: ${l.href}` : `"${l.text}" lands on a list of ${got}, want ${l.want}: ${l.href}`);
    }
  }
  return out;
}

/**
 * sweepOverflow checks horizontal overflow at every 20px from 760 to 1440 (SPEC 19),
 * then every 40px to 1920 (SPEC 22):
 * a layout clean at 1024 and 1280 spilled at 840 to 900, 1040 to 1100 and 1140 to
 * 1160. Only overflow is checked here; the full gate runs at the fixed widths.
 */
export async function sweepOverflow(page: Page): Promise<string[]> {
  const out: string[] = [];
  for (let w = 760; w <= 1920; w += w < 1440 ? 20 : 40) {
    await page.setViewportSize({ width: w, height: 900 });
    await page.evaluate(() => new Promise((res) => requestAnimationFrame(() => requestAnimationFrame(res))));
    const found = await page.evaluate(() => {
      const doc = document.documentElement;
      const msgs: string[] = [];
      if (doc.scrollWidth > doc.clientWidth + 1) msgs.push(`the page scrolls sideways by ${doc.scrollWidth - doc.clientWidth}px`);
      // html and body clip sideways (overflow-x: clip), so a spill never scrolls the
      // page: it is cut silently. Look for any box that runs past the main column and
      // is not inside a box of its own that scrolls or clips.
      const main = document.querySelector('main');
      if (main) {
        const edge = Math.min(main.getBoundingClientRect().right, doc.clientWidth);
        const contained = (el: Element) => {
          for (let p = el.parentElement; p && p !== main; p = p.parentElement) {
            if (getComputedStyle(p).overflowX !== 'visible') return true;
          }
          return false;
        };
        for (const el of main.querySelectorAll('*')) {
          const r = el.getBoundingClientRect();
          if (!r.width || r.right <= edge + 1) continue;
          const cs = getComputedStyle(el);
          if (cs.position === 'fixed' || cs.visibility === 'hidden' || cs.display === 'none' || contained(el)) continue;
          const t = (el.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 32);
          msgs.push(`<${el.tagName.toLowerCase()}> "${t}" runs ${Math.round(r.right - edge)}px past the main column`);
          break;
        }
      }
      return msgs;
    });
    for (const m of found) out.push(`${w}px: ${m}`);
  }
  return out;
}

// The routes the layout specs walk (G1 and the SPEC 24.6 matrix), and the waits they share.

export async function chooseRevision(page: Page, sel: string, version: string) {
  const read = () => page.locator(`${sel} option`).evaluateAll(
    (os, v) => (os as HTMLOptionElement[]).find((o) => (o.textContent ?? '').startsWith(`${v} `))?.value ?? '', version);
  await expect.poll(read, { message: `no ${version} revision in ${sel}`, timeout: 20_000 }).not.toBe('');
  await page.locator(sel).selectOption(await read());
}

/** Opens the first entity link of a kind on a list route: keys come from the engine. */
const firstLink = (list: string, href: string) => async (page: Page) => {
  await page.goto(`/${list}`);
  const a = page.locator(`main a[href*="${href}"]`).first();
  await expect(a).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await a.getAttribute('href')}`);
};

/** Opens payments-service with range proposals in its Inferred block (the fixture puts
 *  them there), presses "Accept all proposed ranges" and waits for the plan or refusal;
 *  'forbidden' is a viewer who is not signed in, offered only the CLI dry run. The page
 *  reloads so the session is read with the flag set. */
export const acceptPlanRoute = (mode: 'plan' | 'refused' | 'forbidden') => async (page: Page) => {
  const flag = (m: string) => { (window as unknown as { __pactoAccept?: string }).__pactoAccept = m; };
  await page.addInitScript(flag, mode);
  await page.goto('/#/fleet/services/payments-service');
  await page.reload();
  const block = page.getByTestId('service-inferred');
  await expect(block).toBeVisible({ timeout: 30_000 });
  await block.locator('summary').first().click();
  if (mode === 'forbidden') {
    await expect(block.getByTestId('accept-sign-in')).toBeVisible({ timeout: 30_000 });
  } else {
    await block.getByRole('button', { name: /Accept all proposed ranges/ }).click();
    await expect(block.getByTestId(mode === 'plan' ? 'accept-waiting' : 'accept-refusal')).toBeVisible({ timeout: 30_000 });
  }
  await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
};

/** Opens a service page and waits for its Delivery rows, so an error page fails the route. */
const serviceWithStages = (hash: string) => async (page: Page) => {
  await page.goto(`/${hash}`);
  await expect(page.locator('[data-testid="service-delivery-row"]').first()).toBeVisible({ timeout: 30_000 });
};

/** Opens a service page and waits for its Environments section. */
const serviceEnvironments = (hash: string) => async (page: Page) => {
  await page.goto(`/${hash}`);
  await expect(page.locator('#sec-environments [data-testid="service-environment"]').first()).toBeVisible({ timeout: 30_000 });
};

/**
 * changesRoute opens a route that scrolls to the Changes section, with reduced motion
 * so the scroll is instant, and returns to the top once it has run: the gate reads
 * from the top.
 */
const changesRoute = async (page: Page, hash: string, ready: () => Promise<void>) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(`/${hash}`);
  await ready();
  await page.waitForFunction(() => window.scrollY > 0, null, { timeout: 5_000 }).catch(() => {});
  // A wheel is the reader driving: it cancels the route's scroll restore.
  await page.mouse.wheel(0, -1);
  await page.evaluate(() => window.scrollTo(0, 0));
};

/** Opens a service page at its Changes section and waits for one change row. */
const serviceChanges = (hash: string) => async (page: Page) =>
  changesRoute(page, hash, () => expect(page.locator('[data-testid="service-change-row"]').first()).toBeVisible({ timeout: 30_000 }));

/** Opens a service page with its Changes filtered to one kind and waits for the count. */
const serviceChangesFiltered = (hash: string) => async (page: Page) =>
  changesRoute(page, hash, () => expect(page.locator('[data-testid="service-changes-filtered"]')).toBeVisible({ timeout: 30_000 }));

/** Opens a service page whose change times are hidden and waits for that text. */
const serviceChangesHidden = (hash: string) => async (page: Page) =>
  changesRoute(page, hash, () => expect(page.locator('[data-testid="service-change-reached"]', { hasText: 'time hidden' }).first()).toBeVisible({ timeout: 30_000 }));

/** Opens a service page and waits for its Resources section (#468). */
const serviceResources = (hash: string) => async (page: Page) => {
  await page.goto(`/${hash}`);
  await expect(page.locator('[data-testid="service-resources"]')).toBeVisible({ timeout: 30_000 });
};

/** Opens a delivery list narrowed to one service and waits for its filter chip, so an error page fails the route. */
const deliveryOfService = (hash: string, landmark: string) => async (page: Page) => {
  await page.goto(`/${hash}`);
  await expect(page.locator('[data-testid="delivery-service-filter"]')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(landmark).first()).toBeVisible({ timeout: 30_000 });
};
// What each list shows once its answer is in.
const INBOX_LOADED = '#inbox-held, [data-testid="inbox-empty"]';
const PUBLISH_LOADED = 'li.pv-row';
const AUTONOMY_LOADED = '[data-testid="autonomy-contracted"]';

/**
 * Opens the propose panel from the environments board as the default persona
 * (pay-agent, an agent), with the Stage rows' names at their longest, and waits for
 * the plan and the on-behalf-of field an agent is asked for.
 */
const openPropose = async (page: Page, stale = false) => {
  await page.evaluate((s) => {
    const w = window as unknown as { __pactoLongPropose?: boolean; __pactoStalePropose?: boolean };
    w.__pactoLongPropose = true;
    w.__pactoStalePropose = s;
  }, stale);
  await page.goto('/#/delivery/environments');
  await page.getByRole('button', { name: /^Promote payments in prod to / }).first().click({ timeout: 30_000 });
  const panel = page.locator('[data-testid="propose-panel"]');
  await expect(panel.locator('[data-testid="plan-prediction"]')).toBeVisible({ timeout: 30_000 });
  await expect(panel.getByTestId('pp-on-behalf-of')).toBeVisible();
  return panel;
};

// The story's payments service and its 3.0.0 revision (examples/story): prod offers the
// promote of 3.0.0's artifact, and test runs that artifact with an undo on offer.
const STORY_PAYMENTS = 'registry.example%252Fstory%2Fpayments';
const PAYMENTS_300 = 'sha256:a16380137bcba704971cd4258237e24fb8822d93f66bd3f280bd829005665323';
const PAYMENTS_300_ARTIFACT = 'sha256:d08642a39559182a2b9bc69a8f7ad32e87fa058ebd250087dbc071bf3df809a4';
const PLAN = /^Plan /;

/**
 * Opens an investigation page with the Stage rows' names at their longest (the panels
 * ask the engine under the short names again) and waits for its first "Plan" offer, so
 * a page that renders none fails instead of passing.
 */
const planOffer = async (page: Page, hash: string, before?: (page: Page) => Promise<void>) => {
  await page.evaluate(() => { (window as unknown as { __pactoLongPropose?: boolean }).__pactoLongPropose = true; });
  await page.goto(`/${hash}`);
  if (before) await before(page);
  const offer = page.getByRole('button', { name: PLAN }).first();
  await expect(offer).toBeVisible({ timeout: 30_000 });
  return offer;
};
const comparePayments = async (page: Page) => { await page.getByRole('button', { name: 'Compare revisions' }).click({ timeout: 30_000 }); };
const planOpened = async (page: Page) => {
  await expect(page.locator('[data-testid="propose-panel"] [data-testid="plan-prediction"]')).toBeVisible({ timeout: 30_000 });
  await page.evaluate(() => window.scrollTo(0, 0)); // the click scrolled the panel into view; the gate reads from the top
};

/**
 * Opens Metrics in one fixture state. A real navigation (the path changes), so the app
 * asks for its capabilities again: a bare hash change keeps the ones it already has.
 * The state's own heading must show, so a variant that fell back to another fails.
 */
const METRICS_SHOWS: Record<string, (page: Page) => Locator> = {
  off: (page) => page.getByRole('heading', { name: 'Metrics are off' }),
  none: (page) => page.getByRole('heading', { name: 'No run records in this window' }),
  restricted: (page) => page.getByTestId('metrics-hidden'),
};
const openMetrics = (mode: string) => async (page: Page) => {
  await page.evaluate((m) => sessionStorage.setItem('fixture.stats', m), mode);
  await page.goto('/index.html?fixture=1#/metrics');
  await page.evaluate(() => (window as unknown as { __pactoReady?: Promise<unknown> }).__pactoReady);
  await expect(METRICS_SHOWS[mode](page)).toBeVisible({ timeout: 30_000 });
};

/**
 * Opens Metrics at one fixture size (#597): the recorded window over 5 or 200
 * environments, a real navigation like openMetrics. The 60 case is the `metrics` route.
 */
export const openMetricsAt = (envs: '5' | '200') => async (page: Page) => {
  await page.evaluate((n) => { sessionStorage.removeItem('fixture.stats'); sessionStorage.setItem('fixture.stats.envs', n); }, envs);
  // The size in the path too, so a second size after a first one is a real navigation.
  await page.goto(`/index.html?fixture=1&envs=${envs}#/metrics`);
  await page.evaluate(() => (window as unknown as { __pactoReady?: Promise<unknown> }).__pactoReady);
  await expect(page.getByTestId('metrics-tiles')).toBeVisible({ timeout: 30_000 });
  const health = page.getByTestId('metrics-health');
  if (envs === '5') {
    await expect(health.getByTestId('metrics-env-panel')).toHaveCount(5);
    return;
  }
  // 200: the depth filter row and collapsed groups with their pooled rows.
  await expect(page.getByRole('group', { name: 'Filter by promotion depth' })).toBeVisible();
  await expect(health.locator('tr.rung button[aria-expanded="false"]').first()).toBeVisible();
};

/**
 * Opens Metrics at the default 60 environments: the heatmap, its groups collapsed, then
 * the first group opened so its rows (long names) are laid out and checked too.
 */
const metricsSixty = async (page: Page) => {
  await page.goto('/index.html?fixture=1#/metrics');
  await page.evaluate(() => (window as unknown as { __pactoReady?: Promise<unknown> }).__pactoReady);
  const health = page.getByTestId('metrics-health');
  await expect(health.locator('.hm-scroll')).toBeVisible({ timeout: 30_000 });
  await health.locator('tr.rung button').first().click();
  await expect(health.locator('.hm tbody th[scope="row"]').first()).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0)); // the click scrolled the card into view; the gate reads from the top
};

/**
 * Opens Metrics (60 environments) with every chart card shown as its table (#597), so the
 * layout checks and figureLinkTotals run over the table views and their narrow form.
 */
const metricsTables = async (page: Page) => {
  await page.goto('/index.html?fixture=1#/metrics');
  await page.evaluate(() => (window as unknown as { __pactoReady?: Promise<unknown> }).__pactoReady);
  const toggles = page.getByRole('button', { name: 'Show as table' });
  await expect(toggles.first()).toBeVisible({ timeout: 30_000 });
  const n = await toggles.count();
  for (let i = 0; i < n; i++) await toggles.first().click();
  // Every card shows its table; lead time shows two (per environment and per week).
  await expect(toggles).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Show as chart' })).toHaveCount(n);
  await expect(page.getByTestId('metrics-health').locator('.cc-table')).toBeVisible();
  await expect(page.getByTestId('metrics-lead').locator('.cc-table')).toHaveCount(2);
  await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
};

/**
 * Opens Metrics over the browser demo's own run records (the fixture's `engine` mode),
 * a real navigation like openMetrics, and waits for the populated tiles and checkout's
 * prod-eu row, so an error page or the explainer cannot pass for the page.
 */
const demoMetrics = (hash: string) => async (page: Page) => {
  await page.evaluate(() => sessionStorage.setItem('fixture.stats', 'engine'));
  await page.goto(`/index.html?fixture=1${hash}`);
  await page.evaluate(() => (window as unknown as { __pactoReady?: Promise<unknown> }).__pactoReady);
  await expect(page.getByTestId('metrics-tiles').locator('.mx-tile').first()).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId('metrics-health').getByText('prod-eu', { exact: true }).first()).toBeVisible({ timeout: 30_000 });
};

/** Opens a service created from a golden path, through the path's own created count. */
const createdService = async (page: Page) => {
  await page.goto('/#/fleet/golden-paths');
  const created = page.locator('[data-testid="path-created"]').first();
  await expect(created).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await created.getAttribute('href')}`);
  const a = page.locator('[data-testid="service-list"] a[href*="/fleet/services/"]').first();
  await expect(a).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await a.getAttribute('href')}`);
};

// `list` marks list views, where L3 holds the list within 600px or the figure folds.
export type Route = { name: string; hash?: string; list?: boolean; open?: (page: Page) => Promise<void> };
// 509-S3: the grouped inferred service billing-worker, reached through its real href.
async function openGroupedInferred(page: Page): Promise<void> {
  await page.goto('/#/fleet/services?declaration=inferred&group=argocd%2Fshop%2Fshop');
  const a = page.locator('main a[href*="/fleet/services/"][href*="billing-worker"]').first();
  await expect(a).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await a.getAttribute('href')}`);
  await expect(page.locator('[data-testid="inferred-workload"]')).toBeVisible({ timeout: 30_000 });
}

// 514-S2: a list under the three-cluster fixture, waiting for what it adds.
function openMultiCluster(hash: string, ready = '[data-testid="cluster-caption"]'): (page: Page) => Promise<void> {
  return async (page) => {
    await page.evaluate(() => { (window as unknown as { __pactoMultiCluster?: boolean }).__pactoMultiCluster = true; });
    await page.goto(`/${hash}`);
    await expect(page.locator(ready)).toBeVisible({ timeout: 30_000 });
  };
}

export const ROUTES: Route[] = [
  { name: 'overview', hash: '#/fleet' },
  { name: 'overview watched namespaces', open: async (page) => { await page.addInitScript(() => { (window as unknown as { __scoped?: boolean }).__scoped = true; }); await page.goto('/#/fleet'); await page.reload(); } }, // reload: the init script only runs on a new document
  { name: 'services', list: true, hash: '#/fleet/services' },
  { name: 'owners', list: true, hash: '#/fleet/owners' },
  { name: 'domains', list: true, hash: '#/fleet/domains' },
  { name: 'offerings', list: true, hash: '#/fleet/offerings' },
  { name: 'offerings failed', list: true, hash: '#/fleet/offerings?failed=1' },
  { name: 'offerings deprecated', list: true, hash: '#/fleet/offerings?lifecycle=deprecated' },
  { name: 'sources', list: true, hash: '#/fleet/sources' },
  { name: 'attention', list: true, hash: '#/fleet/attention' },
  { name: 'attention health category', list: true, hash: '#/fleet/attention?category=health' },
  { name: 'attention knowledge category', list: true, hash: '#/fleet/attention?category=knowledge' },
  { name: 'services with a knowledge gap', list: true, hash: '#/fleet/services?gap=no-summary' },
  { name: 'services whose owner is unknown', list: true, hash: '#/fleet/services?ownership=unknown' },
  { name: 'incomplete sources', list: true, hash: '#/fleet/sources?sourceHealth=incomplete' },
  { name: 'sources, detected providers expanded', list: true, open: async (page) => { await page.goto('/#/fleet/sources'); await page.locator('[data-testid=detected-providers]').getByRole('button', { name: /Show all/ }).click(); await page.locator('[data-testid=detected-providers] details').first().evaluate((d) => { (d as HTMLDetailsElement).open = true; }); await page.evaluate(() => window.scrollTo(0, 0)); } }, // the click scrolled the block into view; the gate reads from the top
  {
    // Every inferred row's More details open (510-S5): the long details, the hint and its
    // copy button are measured where the gate looks.
    name: 'sources, detected details open', list: true,
    open: async (page) => {
      await page.goto('/#/fleet/sources');
      const block = page.locator('[data-testid=detected-providers]');
      await block.getByRole('button', { name: /Show all/ }).click();
      // Every row's More details, the declared Installation row's included.
      const more = block.locator('.dp-row details');
      await expect(more.first()).toBeAttached({ timeout: 30_000 });
      await more.evaluateAll((ds) => { for (const d of ds) (d as HTMLDetailsElement).open = true; });
      await expect(block.locator('.dp-row details:not([open])')).toHaveCount(0);
      await page.evaluate(() => window.scrollTo(0, 0)); // the click scrolled the block into view; the gate reads from the top
    },
  },
  { name: 'domains by health', list: true, hash: '#/fleet/domains?sort=health' },
  { name: 'owners by gaps', list: true, hash: '#/fleet/owners?sort=gaps' },
  { name: 'attention lifecycle', list: true, hash: '#/fleet/attention?category=lifecycle' },
  { name: 'graph discovery', hash: '#/fleet/graph' },
  { name: 'graph focused', hash: '#/fleet/graph/service/payments-service' },
  { name: 'change analysis idle', hash: '#/fleet/changes/payments-service' },
  {
    name: 'change analysis answered',
    open: async (page) => {
      await page.goto('/#/fleet/changes/payments-service');
      await chooseRevision(page, '#impact-old-rev', '1.0.0');
      await chooseRevision(page, '#impact-new-rev', '2.0.1');
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.locator('#sec-affected-consumers')).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    // Announced, accepted and expired consumers of a BREAKING change, the Ranges line
    // and its note: one mark per announced consumer.
    name: 'change analysis breaks',
    open: async (page) => {
      await page.addInitScript(() => { (window as unknown as { __pactoAccept?: string }).__pactoAccept = 'breaks'; });
      await page.goto('/#/fleet/changes/payments-service');
      await page.evaluate(() => { (window as unknown as { __pactoAccept?: string }).__pactoAccept = 'breaks'; });
      await chooseRevision(page, '#impact-old-rev', '1.0.0');
      await chooseRevision(page, '#impact-new-rev', '2.0.1');
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.getByTestId('changes-ranges')).toBeVisible({ timeout: 30_000 });
      await expect(page.getByTestId('changes-inferred-advisory')).toBeVisible();
      // The marks sit in the Affected consumers fold, closed by default: open it so
      // the gate measures them in the table and the stacked cards.
      await page.locator('#sec-affected-consumers > summary').click();
      await expect(page.getByTestId('consumer-break').first()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
    },
  },
  { name: 'change analysis event idle', hash: '#/fleet/changes/redis?mode=event' },
  {
    name: 'change analysis event answered',
    open: async (page) => {
      await page.goto('/#/fleet/changes/redis?mode=event');
      await page.locator('#impact-event').selectOption('unavailable');
      await page.getByRole('button', { name: /^Ask$/ }).click();
      await expect(page.locator('#sec-affected-consumers')).toBeVisible({ timeout: 30_000 });
    },
  },
  { name: 'change analysis event removed', hash: '#/fleet/changes/postgresql?event=removed' },
  { name: 'change analysis event regressed', hash: `#/fleet/changes/redis?event=regressed&rev=sha256:${'ab12'.repeat(16)}` },
  { name: 'change analysis event regressed unread', hash: '#/fleet/changes/redis?event=regressed&rev=1.0.0' },
  { name: 'change analysis event clear', hash: '#/fleet/changes/storefront-web?event=unavailable' },
  { name: 'change analysis event regressed clean', hash: `#/fleet/changes/redis?event=regressed&rev=sha256:${'c1ea'.repeat(16)}` },
  { name: 'service entity', hash: '#/fleet/services/payments-service' },
  { name: 'service inferred accept plan', open: acceptPlanRoute('plan') },
  { name: 'service inferred accept refused', open: acceptPlanRoute('refused') },
  { name: 'service inferred accept forbidden', open: acceptPlanRoute('forbidden') },
  { name: 'revision list', list: true, hash: '#/fleet/revisions?service=payments-service' },
  { name: 'revision entity', open: firstLink('#/fleet/revisions?service=payments-service', '/fleet/revisions/') },
  { name: 'revision entity, implicit contract', open: firstLink('#/fleet/revisions?service=newsletter-service', '/fleet/revisions/') },
  {
    // A signed declaration: the declaration line and the cluster rows, the "N more" open
    // so the 80-character values are measured. Reload: the flag is read per answer.
    name: 'revision entity, cluster facts',
    open: async (page) => {
      await page.addInitScript(() => { (window as unknown as { __pactoClusterFacts?: boolean }).__pactoClusterFacts = true; });
      await firstLink('#/fleet/revisions?service=newsletter-service', '/fleet/revisions/')(page);
      await page.reload();
      const block = page.getByTestId('revision-inferred');
      await expect(block.getByTestId('inferred-declaration')).toBeVisible({ timeout: 30_000 });
      await block.locator('.ifl-more > summary').click();
      await expect(block.locator('.ifl-more [data-testid="inferred-field"]').first()).toBeVisible();
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  {
    // pacto ci raised the authored pactoVersion in memory: muted text, behind badge and
    // the provenance mark that ends the row. Reload: the init script runs on a new document.
    name: 'revision entity, migrated contract',
    open: async (page) => {
      await page.addInitScript(() => { (window as unknown as { __migrated?: string }).__migrated = 'layout'; });
      await firstLink('#/fleet/revisions?service=api-gateway', '/fleet/revisions/')(page);
      await page.reload();
      await expect(page.locator('.re-fact').filter({ hasText: 'migrated from 2.4' })).toBeVisible({ timeout: 30_000 });
    },
  },
  { name: 'service entity, implicit contract', hash: '#/fleet/services/newsletter-service' },
  {
    // A collapsed PreviewSection mounts nothing and the gate skips closed disclosures:
    // open the inferred block and its "N more" rows so the long key is measured.
    name: 'service entity, implicit contract, inferred open',
    open: async (page) => {
      await page.goto('/#/fleet/services/newsletter-service');
      const block = page.getByTestId('service-inferred');
      await expect(block).toBeVisible({ timeout: 30_000 });
      await block.locator('summary').first().click();
      await block.locator('.ifl-more > summary').click();
      await expect(block.locator('.ifl-more [data-testid="inferred-field"]').first()).toBeVisible();
      await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
      // The pointer stays where the last click was: once the page scrolls back, a help
      // tip can sit under it and hover opens it. Park it in the corner.
      await page.mouse.move(0, 0);
    },
  },
  {
    name: 'revision entity, implicit contract, inferred more',
    open: async (page) => {
      await firstLink('#/fleet/revisions?service=newsletter-service', '/fleet/revisions/')(page);
      const more = page.getByTestId('revision-inferred').locator('.ifl-more');
      await expect(more).toBeAttached({ timeout: 30_000 });
      await more.locator('> summary').click();
      await expect(more.getByTestId('inferred-field').first()).toBeVisible();
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  {
    // A record Pacto could not read: the folded block names it, and open it says so
    // in one line, beside no record row.
    name: 'service entity, implicit contract, inferred unreadable',
    open: async (page) => {
      await page.evaluate(() => { (window as unknown as { __pactoInferredUnreadable?: boolean }).__pactoInferredUnreadable = true; });
      await page.goto('/#/fleet/services/newsletter-service');
      const block = page.getByTestId('service-inferred');
      await expect(block).toContainText('Could not be read', { timeout: 30_000 });
      await block.locator('summary').first().click();
      await expect(block).toContainText('Pacto could not read the inferred record');
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.mouse.move(0, 0); // as above: no help tip left open under the pointer
    },
  },
  {
    // The revision keeps the values a local read infers under the unreadable line.
    name: 'revision entity, implicit contract, inferred unreadable',
    open: async (page) => {
      await page.evaluate(() => { (window as unknown as { __pactoInferredUnreadable?: boolean }).__pactoInferredUnreadable = true; });
      await firstLink('#/fleet/revisions?service=newsletter-service', '/fleet/revisions/')(page);
      await expect(page.getByTestId('revision-inferred')).toContainText('Pacto could not read the inferred record', { timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  {
    // newsletter-service consumes notification-worker only through its inferred record.
    // ponytail: the consumers fold stays closed, as on every change-analysis route: open, the
    // scale fixture's long names spill from the Consumer and Runs on cells (a table defect
    // older than the inferred mark). inferred-record.spec.ts checks the Why cell's one mark.
    name: 'change analysis, implicit contract consumer',
    open: async (page) => {
      await page.goto('/#/fleet/changes/notification-worker?event=removed');
      await expect(page.getByTestId('changes-answer')).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    // The scale fixture's 2.5 rows (announced breaks, ports, a ref-named dependency
    // with no range) live in closed disclosures, which mount nothing: open them all.
    name: 'revision entity, 2.5 sections open',
    open: async (page) => {
      await firstLink('#/fleet/revisions?service=payments-service', '/fleet/revisions/')(page);
      for (const title of ['Interfaces', 'Announced breaks', 'Declared dependencies']) {
        const sec = page.locator('details').filter({ has: page.locator('summary.ps-head', { hasText: title }) }).first();
        await expect(sec, `${title} is on the page`).toBeAttached({ timeout: 30_000 });
        await sec.evaluate((d) => { (d as HTMLDetailsElement).open = true; });
        const all = sec.getByTestId('preview-show-all');
        if (await all.count()) await all.first().click();
      }
      await expect(page.locator('[data-testid="revision-break"]').first()).toBeVisible({ timeout: 30_000 });
      await expect(page.getByTestId('name-from-ref').first()).toBeAttached();
      await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
    },
  },
  { name: 'target list', list: true, hash: '#/fleet/targets' },
  { name: 'target entity', open: firstLink('#/fleet/targets', '/fleet/targets/') },
  {
    // A cluster-qualified key in the single-cluster demo lands on the 3-part canonical href.
    name: 'target entity with a cluster',
    open: async (page) => {
      await page.goto('/#/fleet/targets/5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11%2Fprod%2FDeployment%2Fpayments-service');
      await expect(page).toHaveURL(/#\/fleet\/targets\/prod%2FDeployment%2Fpayments-service$/, { timeout: 30_000 });
      await expect(page.locator('[data-testid="target-cluster"]')).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'target key names more than one cluster',
    open: async (page) => {
      await page.evaluate(() => { (window as unknown as { __pactoAmbiguousTarget?: boolean }).__pactoAmbiguousTarget = true; });
      await page.goto('/#/fleet/targets/scale-ambiguous%2Fkubernetes%2Fpayments');
      await expect(page.locator('[data-testid="target-key-chooser"] a')).toHaveCount(7, { timeout: 30_000 });
    },
  },
  {
    name: 'graph focus target key names more than one cluster',
    open: async (page) => {
      await page.evaluate(() => { (window as unknown as { __pactoAmbiguousTarget?: boolean }).__pactoAmbiguousTarget = true; });
      await page.goto('/#/fleet/graph/target/scale-ambiguous%2Fkubernetes%2Fpayments');
      await expect(page.locator('[data-testid="target-key-chooser"] a')).toHaveCount(7, { timeout: 30_000 });
    },
  },
  { name: 'owner entity', hash: '#/fleet/owners/team:catalog-team' },
  { name: 'domain entity', hash: '#/fleet/domains/commerce' },
  { name: 'offering entity', hash: '#/fleet/offerings/messaging' },
  { name: 'offering entity postgresql unsigned provisioning', hash: '#/fleet/offerings/postgresql' },
  { name: 'offering entity adapters', hash: '#/fleet/offerings/security-scanning' },
  { name: 'offering entity unresolved uses', hash: '#/fleet/offerings/shared-config' },
  { name: 'change analysis offering event', hash: '#/fleet/changes?offering=postgresql&event=unavailable' },
  {
    // The successor range line is component state, never in the URL: type the range.
    name: 'offering entity postgresql range',
    open: async (page) => {
      await page.goto('/#/fleet/offerings/postgresql');
      const input = page.locator('[data-testid="offering-provider"]').first().getByRole('textbox');
      await input.fill('^16.0.0');
      await expect(page.locator('[data-testid="versions-successor"]').first()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the fill scrolled the input into view; the gate reads from the top
    },
  },
  { name: 'externals', list: true, hash: '#/fleet/externals' },
  { name: 'externals vendor', list: true, hash: '#/fleet/externals?class=vendor-api' },
  { name: 'external entity', hash: '#/fleet/externals/parcel-carrier' },
  { name: 'external entity name only, hidden, folded and truncated', open: async (page) => { await page.addInitScript(() => { (window as unknown as { __externalStates?: boolean }).__externalStates = true; }); await page.goto('/#/fleet/externals/parcel-carrier'); await page.reload(); await expect(page.getByTestId('external-folded-into')).toBeVisible({ timeout: 30_000 }); } }, // reload: the init script only runs on a new document
  {
    name: 'external entity, inferred contract',
    open: async (page) => {
      await page.addInitScript(() => { (window as unknown as { __inferredContract?: boolean }).__inferredContract = true; });
      await page.goto('/#/fleet/externals/parcel-carrier');
      await page.reload(); // the init script only runs on a new document
      const block = page.getByTestId('external-inferred-contract');
      await block.locator('summary').first().click();
      await block.getByText("What this installation's operator saw").click();
      // G1 measures only what is visible: both summaries are open before it does.
      await expect(block.locator('.xe-surface li').last()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  { name: 'graph external focus', hash: '#/fleet/graph/external/parcel-carrier' },
  { name: 'source entity', hash: '#/fleet/sources/edge-cluster' },
  {
    name: 'external inferred sources',
    open: async (page) => {
      await page.goto('/#/fleet/externals/stripe');
      await page.getByTestId('external-more-details').click();
      // A repository row (508-S5) with its long revision detail is laid out, not just the first rows.
      await expect(page.locator('[data-provenance=inferred] [data-cell=details]', { hasText: 'revision ' }).first()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  { name: 'golden paths', list: true, hash: '#/fleet/golden-paths' },
  { name: 'golden path entity', open: firstLink('#/fleet/golden-paths?sort=name', '/fleet/services/') },
  { name: 'service created from a golden path', open: createdService },
  { name: 'delivery inbox', list: true, hash: '#/delivery/inbox' },
  {
    name: 'delivery inbox, new signer',
    open: async (page) => {
      await page.evaluate(() => { (window as unknown as { __pactoNewSigner?: boolean }).__pactoNewSigner = true; });
      await page.goto('/#/delivery/inbox');
      await expect(page.locator('a.dl-decide', { hasText: 'Confirm identity' }).first()).toBeVisible({ timeout: 30_000 });
    },
  },
  { name: 'delivery environments', list: true, hash: '#/delivery/environments' },
  { name: 'delivery environments of one service', hash: '#/delivery/environments?service=payments' },
  { name: 'delivery history', list: true, hash: '#/delivery/history' },
  { name: 'delivery stage history', hash: '#/delivery/history?namespace=prod&stage=payments' },
  {
    name: 'history, one Stage, with the new columns and filters',
    open: async (page) => {
      await page.goto('/#/delivery/history?namespace=prod&stage=payments');
      await expect(page.locator('section.hv-stage tbody tr').first()).toBeVisible({ timeout: 30_000 });
      const [cause, corr] = await page.locator('[data-testid="history-filters"] input').all();
      await cause.fill('reconciliation-settlement-ledger-export-worker-eu-west-partner-feed-compliance-archive');
      await corr.fill('incident-2026-11-08-payments-prod-error-budget-burn-settlement-reconciliation-ledger-export');
    },
  },
  {
    name: 'delivery history with impact',
    open: async (page) => {
      await page.goto('/#/delivery/history?namespace=prod&stage=payments');
      await expect(page.locator('[data-testid="history-impact"]').first()).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'delivery history as of',
    open: async (page) => {
      await page.goto('/#/delivery/history?at=1m');
      await expect(page.locator('[data-testid="history-at-row"]').first()).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'delivery history as of, one Stage',
    open: async (page) => {
      await page.goto('/#/delivery/history?namespace=prod&stage=payments&at=1m');
      await expect(page.locator('[data-testid="history-at-row"]').first()).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'delivery history as of unknown',
    open: async (page) => {
      await page.goto('/#/delivery/history?at=2000-01-01T00%3A00%3A00Z');
      await expect(page.locator('[data-testid="history-at-row"]', { hasText: 'no record before this time' }).first()).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'history, every Stage, filtered by correlation id',
    hash: '#/delivery/history?correlationID=CHG-2041',
  },
  {
    name: 'history, record panel open',
    open: async (page) => {
      await page.goto('/#/delivery/history?namespace=prod&stage=payments');
      await page.locator('button[aria-label^="Read record"]').first().click();
      await expect(page.locator('[data-testid="record-verdicts"]')).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the click scrolled the panel into view; the gate reads from the top
    },
  },
  {
    name: 'history, blame panel open',
    open: async (page) => {
      await page.goto('/#/delivery/history?namespace=prod&stage=payments');
      await page.locator('button', { hasText: /^Who put .+ here\?$/ }).first().click();
      await expect(page.locator('[data-testid="blame-panel"]')).toBeVisible({ timeout: 30_000 });
      await expect(page.locator('[data-testid="approval-access"]').first()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the click scrolled the panel into view; the gate reads from the top
    },
  },
  { name: 'delivery autonomy', list: true, hash: '#/delivery/autonomy' },
  { name: 'delivery publish', list: true, hash: '#/delivery/publish' },
  {
    name: 'delivery publish, publisher line',
    open: async (page) => {
      await page.evaluate(() => { (window as unknown as { __pactoNewSigner?: boolean }).__pactoNewSigner = true; });
      await page.goto('/#/delivery/publish');
      const fold = page.getByTestId('published-publisher-checks').first();
      await fold.locator('summary').click();
      await expect(fold.locator('li')).toHaveCount(5, { timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  { name: 'delivery change', hash: '#/delivery/changes/prod/payments-promote-2' },
  { name: 'delivery change held', hash: '#/delivery/changes/test/pay-agent-promote-17' },
  {
    name: 'delivery change, new signer',
    open: async (page) => {
      await page.evaluate(() => { (window as unknown as { __pactoNewSigner?: boolean }).__pactoNewSigner = true; });
      await page.goto('/#/delivery/changes/test/pay-agent-promote-17');
      await expect(page.getByTestId('change-publisher')).toBeVisible({ timeout: 30_000 });
    },
  },
  { name: 'service page with Stages', open: serviceWithStages('#/fleet/services/registry.example%252Fstory%2Fpayments') },
  { name: 'service page, Stage waiting on a new signer', open: serviceWithStages('#/fleet/services/audit-log') },
  {
    name: 'service page, Why card open',
    open: async (page) => {
      await serviceWithStages('#/fleet/services/registry.example%252Fstory%2Fpayments')(page);
      await page.locator('details.sd-why summary').first().click();
      await expect(page.locator('[data-testid="service-delivery-why"]').first()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the click scrolled the summary into view; the gate reads from the top
    },
  },
  { name: 'service page, environments without Stages', open: serviceEnvironments('#/fleet/services/storefront-web') },
  { name: 'service page, environments beside Stages', open: serviceEnvironments('#/fleet/services/notification-worker') },
  { name: 'service changes', open: serviceChanges('#/fleet/services/storefront-web?section=changes') },
  { name: 'service changes filtered', open: serviceChangesFiltered('#/fleet/services/storefront-web?changesKind=owner') },
  { name: 'service changes times hidden', open: serviceChangesHidden('#/fleet/services/notification-worker?section=changes') },
  { name: 'service page, resources', open: serviceResources('#/fleet/services/catalog-service') },
  {
    // Expanded, so every card (usage figures included) is laid out where the gate looks,
    // never below the preview's clip.
    name: 'service page, resources at scale',
    open: async (page) => {
      await serviceResources('#/fleet/services/orders-service')(page);
      await page.locator('#sec-resources [data-testid="preview-show-all"]').click();
      await expect(page.locator('#sec-resources .ps-clipped')).toHaveCount(0);
      await page.evaluate(() => window.scrollTo(0, 0)); // the click scrolled the section into view; the gate reads from the top
    },
  },
  {
    name: 'service page, resources empty',
    open: async (page) => {
      // postgresql provides an offering and consumes none.
      await page.goto('/#/fleet/services/postgresql');
      await expect(page.locator('#sec-resources').getByText('No resource you can see.', { exact: false })).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'service page, resource rail open',
    open: async (page) => {
      await serviceResources('#/fleet/services/catalog-service')(page);
      await page.locator('[data-testid="service-resources"] button[data-testid="provisioning-badge"]').first().click();
      await expect(page.locator('[data-testid="offering-use-figure"]')).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the click scrolled the rail into view; the gate reads from the top
    },
  },
  { name: 'service environments', hash: '#/fleet/services/storefront-web' },
  {
    name: 'service environments with Stage cells',
    open: async (page) => {
      await serviceWithStages('#/fleet/services/registry.example%252Fstory%2Fpayments')(page);
      await expect(page.locator('#sec-environments [data-cell="stage"]').first()).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'revision entity with environments',
    open: async (page) => {
      // storefront-web 1.3.0 declares lifetimes, an expiry and an owner.
      await page.goto('/#/fleet/revisions?service=storefront-web');
      const a = page.locator('main a[href*="/fleet/revisions/"]').filter({ hasText: /\b1\.3\.0\b/ }).first();
      await expect(a).toBeVisible({ timeout: 20_000 });
      await page.goto(`/${await a.getAttribute('href')}`);
      // The section starts closed when every value set passed: open it either way.
      const envs = page.locator('details').filter({ has: page.locator('summary.ps-head', { hasText: 'Environments' }) }).first();
      await expect(envs).toBeAttached({ timeout: 30_000 });
      await envs.evaluate((d) => { (d as HTMLDetailsElement).open = true; });
      await expect(page.locator('[data-testid="environment-declared"]').first()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
    },
  },
  {
    name: 'target entity with environment',
    open: async (page) => {
      await page.goto('/#/fleet/targets/prod%2FDeployment%2Fstorefront-web');
      await expect(page.locator('[data-testid="target-environment"]')).toBeVisible({ timeout: 30_000 });
    },
  },
  { name: 'inbox filtered by service', open: deliveryOfService('#/delivery/inbox?service=payments', INBOX_LOADED) },
  { name: 'publish filtered by service', open: deliveryOfService('#/delivery/publish?service=payments', PUBLISH_LOADED) },
  { name: 'autonomy filtered by service', open: deliveryOfService('#/delivery/autonomy?service=payments', AUTONOMY_LOADED) },
  {
    name: 'publish, version lifecycle open',
    open: async (page) => {
      await deliveryOfService('#/delivery/publish?service=payments', PUBLISH_LOADED)(page);
      await page.locator('button[aria-label^="Lifecycle of"]').first().click();
      await expect(page.locator('[data-testid="delivery-lifecycle"] li').first()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
    },
  },
  {
    name: 'revision page, Delivery lifecycle list',
    open: async (page) => {
      await page.goto('/#/fleet/revisions/registry.example%252Fstory%2Fpayments@sha256:8c4f595146e5154efc1fe8d58960e0632e2d3668eb390fa75da0db6390a6494a');
      await expect(page.locator('[data-testid="revision-delivery"] .lc-stage').first()).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'target page with a Delivery link',
    open: async (page) => {
      await page.goto('/#/fleet/targets/prod%2FCronJob%2Faudit-log');
      await expect(page.locator('[data-testid="target-delivery"] a').first()).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'revision page, no Stage names it',
    open: async (page) => {
      await firstLink('#/fleet/revisions?service=storefront-web', '/fleet/revisions/')(page);
      await expect(page.locator('[data-testid="lifecycle-unmatched"]')).toBeVisible({ timeout: 30_000 });
    },
  },
  {
    name: 'target page, no Stage delivers it here',
    open: async (page) => {
      await page.goto('/#/fleet/targets/staging%2FDeployment%2Fnotification-worker');
      await expect(page.locator('[data-testid="target-delivery"]')).toContainText('No Stage you can see', { timeout: 30_000 });
    },
  },
  {
    name: 'propose panel open, agent (on-behalf-of field)',
    open: async (page) => {
      await openPropose(page);
      await page.evaluate(() => window.scrollTo(0, 0)); // the click scrolled the panel into view; the gate reads from the top
    },
  },
  {
    name: 'propose panel, fresh plan banner',
    open: async (page) => {
      const panel = await openPropose(page, true);
      await panel.getByTestId('pp-on-behalf-of').fill('dana@example.com');
      await panel.getByRole('button', { name: /^Propose/ }).click();
      await expect(panel.getByRole('alert')).toContainText('Something changed since the plan', { timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
    },
  },
  { name: 'target page with Plan offers', open: async (page) => { await planOffer(page, '#/fleet/targets/test%2FDeployment%2Fcheckout'); } },
  { name: 'revision page with a promote offer', open: async (page) => { await planOffer(page, `#/fleet/revisions/${STORY_PAYMENTS}@${PAYMENTS_300}`); } },
  { name: 'change analysis answered with Plan promote', open: async (page) => { await planOffer(page, `#/fleet/changes/${STORY_PAYMENTS}`, comparePayments); } },
  { name: 'change analysis regressed with Plan undo', open: async (page) => { await planOffer(page, `#/fleet/changes/${STORY_PAYMENTS}?event=regressed&rev=${PAYMENTS_300_ARTIFACT}`); } },
  {
    name: 'target page, Plan undo panel open',
    open: async (page) => {
      await (await planOffer(page, '#/fleet/targets/test%2FDeployment%2Fcheckout')).click();
      await page.getByRole('button', { name: /^Plan this / }).click({ timeout: 30_000 });
      await planOpened(page);
    },
  },
  {
    name: 'change analysis answered, Plan promote panel open',
    open: async (page) => {
      await (await planOffer(page, `#/fleet/changes/${STORY_PAYMENTS}`, comparePayments)).click();
      await planOpened(page);
    },
  },
  {
    name: 'change page with service and revision links',
    open: async (page) => {
      await page.goto('/#/delivery/changes/prod/payments-promote-6');
      await page.locator('[data-testid="change-details"] > summary').click();
      await expect(page.locator('[data-testid="change-service"] a')).toBeVisible({ timeout: 30_000 });
      await expect(page.locator('[data-testid="change-revision"]').first()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
    },
  },
  { name: 'metrics off', open: openMetrics('off') },
  { name: 'metrics no records', open: openMetrics('none') },
  { name: 'metrics', open: metricsSixty },
  { name: 'metrics records', hash: '#/metrics?metric=blocked' },
  { name: 'metrics restricted', open: openMetrics('restricted') },
  { name: 'metrics 5 environments', open: openMetricsAt('5') },
  { name: 'metrics 200 environments', open: openMetricsAt('200') },
  { name: 'metrics tables', open: metricsTables },
  { name: 'metrics (demo records)', open: demoMetrics('#/metrics') },
  { name: 'metrics (demo records, 90 days)', open: demoMetrics('#/metrics?window=90d') },
  { name: 'services inferred filter', list: true, hash: '#/fleet/services?declaration=inferred' },
  {
    name: 'inferred entity legacy-billing',
    open: async (page) => {
      // The real href, so the double-encoded key is never written by hand.
      await page.goto('/#/fleet/services?declaration=inferred');
      const a = page.locator('main a[href*="/fleet/services/"][href*="legacy-billing"]').first();
      await expect(a).toBeVisible({ timeout: 20_000 });
      await page.goto(`/${await a.getAttribute('href')}`);
      await expect(page.locator('[data-testid="inferred-workload"]')).toBeVisible({ timeout: 30_000 });
    },
  },
  // 509-S3: the Services list filtered to one Argo CD Application, a grouped inferred
  // service and its Group and Repository facts at their longest.
  { name: 'services group filter', list: true, hash: '#/fleet/services?declaration=inferred&group=argocd%2Fshop%2Fshop' },
  { name: 'inferred entity grouped', open: openGroupedInferred },
  {
    name: 'inferred entity long values',
    open: async (page) => {
      await page.addInitScript(() => { (window as unknown as { __groupLongValues?: boolean }).__groupLongValues = true; });
      await openGroupedInferred(page);
      await page.reload(); // the init script only runs on a new document
      await expect(page.locator('[data-testid="inferred-repository"]')).toBeVisible({ timeout: 30_000 });
    },
  },
  // 509-S4: a runtime-inferred edge on the graph, the Replaces line and a replaced key.
  {
    name: 'graph inferred edges',
    open: async (page) => {
      // The text alternative and the drawer of the runtime-inferred edge, whose
      // uncontracted end carries no break point.
      await page.goto('/#/fleet/graph/service/search-indexer');
      await page.getByTestId('graph-textalt').locator('summary').click();
      await expect(page.getByTestId('graph-edge').filter({ hasText: 'nightly-export' }).first()).toBeVisible({ timeout: 30_000 });
      await page.evaluate(() => window.scrollTo(0, 0)); // the gate reads from the top
    },
  },
  { name: 'service replaced inferred', hash: '#/fleet/services/experiment-service' },
  { name: 'replaced inferred key', hash: `#/fleet/services/${encodeURIComponent('k8s:prod%2FDeployment%2Fexperiments')}` },
  {
    name: 'service handover fields',
    open: async (page) => {
      // The fold opened, so the gates read its rows; a closed fold hides them.
      await page.goto('/#/fleet/services/invoices');
      const d = page.locator('[data-testid=field-provenance-fold]');
      await d.waitFor();
      await d.evaluate((el) => { (el as HTMLDetailsElement).open = true; });
      await d.locator('dl dt').first().waitFor();
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  { name: 'service handover fields closed', hash: '#/fleet/services/invoices' },
  {
    name: 'revision entity, discovery findings open',
    open: async (page) => {
      await firstLink('#/fleet/revisions?service=newsletter-service', '/fleet/revisions/')(page);
      const d = page.getByTestId('revision-discovery-findings');
      await expect(d).toBeAttached({ timeout: 30_000 });
      await d.evaluate((el) => { (el as HTMLDetailsElement).open = true; });
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  // 514-S2: three clusters (one hidden by access, one by a failed review) behind the
  // __pactoMultiCluster flag: the Cluster filter and caption, and the Clusters block.
  { name: 'services list, multi-cluster', list: true, open: openMultiCluster('#/fleet/services') },
  { name: 'targets list, multi-cluster filter', list: true, open: openMultiCluster('#/fleet/targets?cluster=5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11') },
  { name: 'sources, multi-cluster scope', list: true, open: openMultiCluster('#/fleet/sources', '[data-testid="source-clusters"]') },
  // 510-S9: the Connect an agent line open, as an issuer-only dashboard answers it.
  {
    name: 'sources, connect-agent issuer',
    list: true,
    open: async (page) => {
      await page.goto('/#/fleet/sources');
      await page.evaluate(() => sessionStorage.setItem('fixture.mcp', 'issuer'));
      await page.reload();
      const d = page.getByTestId('connect-agent');
      await expect(d).toBeAttached({ timeout: 30_000 });
      await d.evaluate((el) => { (el as HTMLDetailsElement).open = true; });
      await expect(d.getByTestId('connect-agent-source')).toContainText('your MCP client signs in with');
      await expect(d).not.toContainText('kubectl');
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
  // 510-S9: the Connect an agent line open, as a dashboard that serves /mcp answers it.
  {
    name: 'sources, connect-agent open',
    list: true,
    open: async (page) => {
      await page.goto('/#/fleet/sources');
      await page.evaluate(() => sessionStorage.setItem('fixture.mcp', 'served'));
      await page.reload();
      const d = page.getByTestId('connect-agent');
      await expect(d.locator('summary bdi')).toBeAttached({ timeout: 30_000 });
      await d.evaluate((el) => { (el as HTMLDetailsElement).open = true; });
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
];

/** Flips the sidebar with its "[" shortcut and returns the state it lands in. */
export async function toggleSidebar(page: Page): Promise<string> {
  const side = page.locator('[data-testid="sidebar"]');
  const before = await side.getAttribute('data-rail');
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur?.()); // "[" is ignored in a field
  await page.keyboard.press('[');
  await expect(side).not.toHaveAttribute('data-rail', before ?? '');
  await settle(page);
  return (await side.getAttribute('data-rail')) ?? 'none';
}

/** The route has drawn its answer: a heading, and nothing still loading. */
export async function loaded(page: Page) {
  await expect(page.locator('h1').first()).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => page.evaluate(() =>
    !document.querySelector('[aria-busy="true"]') &&
    ![...document.querySelectorAll('main *')].some((el) => /^Loading\b/.test((el.textContent ?? '').trim()) && (el as HTMLElement).offsetParent !== null && el.children.length === 0)),
  { timeout: 30_000 }).toBe(true);
}

/**
 * shellProblems checks the shell at the current viewport (SPEC 22.3, 22.4, 23, 24.4):
 * the content stops at its capped width; the sidebar and its background run to the
 * window bottom at scroll top and at the foot of the page; under 768px the Menu drawer
 * does. It scrolls, then returns to the top.
 */
export async function shellProblems(page: Page): Promise<string[]> {
  const out: string[] = [];
  // SPEC 22.3: the content stops at its capped width, however wide the window.
  const cap = await page.evaluate(() => {
    const v = getComputedStyle(document.documentElement).getPropertyValue('--container-max').trim();
    const max = v.endsWith('px') ? parseFloat(v) : 0; // under 768px the cap is 100%
    const w = document.querySelector('main')?.getBoundingClientRect().width ?? 0;
    return { max, w: Math.round(w) };
  });
  if (cap.max > 0 && cap.w > cap.max + 1) out.push(`the content is ${cap.w}px wide, past its ${cap.max}px cap`);
  const side = page.locator('[data-testid="sidebar"]');
  const bottom = (sel: string) => page.evaluate((s) => {
    const el = document.querySelector(s);
    const r = el?.getBoundingClientRect();
    return r && r.height ? { b: Math.round(r.bottom), h: window.innerHeight } : null;
  }, sel);
  if (await side.isVisible()) {
    for (const where of ['top', 'foot'] as const) {
      await page.evaluate((w) => window.scrollTo(0, w === 'top' ? 0 : document.documentElement.scrollHeight), where);
      await page.evaluate(() => new Promise((res) => requestAnimationFrame(() => requestAnimationFrame(res))));
      const got = await bottom('[data-testid="sidebar"] .side-in');
      if (got && Math.abs(got.b - got.h) > 1) out.push(`the sidebar ends at ${got.b}px, not at the window bottom (${got.h}px), at the page ${where}`);
    }
    await page.evaluate(() => window.scrollTo(0, 0));
    return out;
  }
  const menu = page.getByRole('button', { name: 'Menu' });
  if (await menu.isVisible()) {
    await menu.click();
    await expect(page.locator('#mobile-drawer')).toBeVisible();
    const got = await bottom('#mobile-drawer');
    if (got && got.b < got.h - 1) out.push(`the menu drawer ends at ${got.b}px, short of the window bottom (${got.h}px)`);
    await page.keyboard.press('Escape');
    await expect(page.locator('#mobile-drawer')).toBeHidden();
  }
  return out;
}

/**
 * touchProblems is the SPEC 23 touch rule: on a coarse pointer every visible control and
 * every link outside running text is at least 44x44px. A link inside a sentence keeps its
 * line (WCAG 2.5.8 exempts inline targets). It reports nothing on a fine pointer.
 */
export async function touchProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    if (!matchMedia('(pointer: coarse)').matches) return [];
    const out: string[] = [];
    const hidden = (el: Element) => {
      for (let p: Element | null = el; p && p !== document.body; p = p.parentElement) {
        const cs = getComputedStyle(p);
        if (cs.display === 'none' || cs.visibility === 'hidden') return true;
        if (cs.position === 'absolute' && (cs.clip === 'rect(0px, 0px, 0px, 0px)' || cs.clipPath === 'inset(50%)')) return true;
        if (p.tagName === 'DETAILS' && !(p as HTMLDetailsElement).open && !el.closest('summary')) return true;
      }
      return false;
    };
    // In a sentence: an inline link whose nearest block also holds words of its own, or
    // a link in running text (a paragraph that is not a .go row of links, a caption).
    const inSentence = (a: Element) => {
      if (a.closest('p:not(.go), figcaption, .t-meta, .lead, .dist-scope')) return true;
      if (!/^inline$/.test(getComputedStyle(a).display)) return false;
      let blk = a.parentElement;
      while (blk && /^inline/.test(getComputedStyle(blk).display)) blk = blk.parentElement;
      return !!blk && (blk.textContent ?? '').replace(/\s+/g, '').length > (a.textContent ?? '').replace(/\s+/g, '').length;
    };
    const sel = 'a[href], button, select, input:not([type=hidden]):not([type=checkbox]):not([type=radio]), summary, [role=button]';
    for (const el of document.querySelectorAll(`#app :is(${sel})`)) {
      const r = el.getBoundingClientRect();
      if (!r.width || !r.height || hidden(el) || el.closest('[popover]:not(:popover-open), dialog:not([open])')) continue;
      if (el.tagName === 'A' && inSentence(el)) continue;
      if (r.width < 43.5 || r.height < 43.5) {
        const t = (el.textContent || el.getAttribute('aria-label') || '').trim().replace(/\s+/g, ' ').slice(0, 32);
        out.push(`a ${Math.round(r.width)}x${Math.round(r.height)}px touch target, under 44x44: <${el.tagName.toLowerCase()}> "${t}"`);
      }
    }
    return [...new Set(out)];
  });
}
