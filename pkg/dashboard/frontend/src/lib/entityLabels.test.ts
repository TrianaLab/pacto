import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  kindLabel, kindLabelPlural, linkStateLabel, linkStateTone, retrievabilityLabel, retrievabilityTone,
  knowledgeLabel, knowledgeTone, sourceHealthLabel, sourceHealthTone,
  attentionCategoryLabel, GAP_WORDS,
  provenanceLabel, provenanceIsImplied,
  sourceHealthTallyParts, sourceHealthSentence, sourceMessageIsNote,
  collectorLabel, seenBy, splitEntryPoints, acceptsLabel, RANGE_NOT_DECLARED, serviceKeyName, replacesTip, linkTip, ownerProvenanceText, inferredFactText,
  attentionCause, nextActionWords,
  completenessText,
} from './entityLabels.ts';

describe('acceptsLabel', () => {
  it('names an undeclared range one way and never as a range', () => {
    expect(acceptsLabel('unknown')).toBe(RANGE_NOT_DECLARED);
    expect(acceptsLabel('^1.0.0')).toBe('Accepts ^1.0.0');
    expect(acceptsLabel('')).toBe('');
  });
});
import { statusLabel } from './format.ts';

describe('kindLabel', () => {
  it('maps kinds to product language', () => {
    expect(kindLabel('service')).toBe('Service');
    expect(kindLabel('revision')).toBe('Revision');
    expect(kindLabel('owner')).toBe('Owner');
    expect(kindLabel('external')).toBe('External system');
  });

  // Pacto observes where a revision runs; it never deploys anything. Calling a target a
  // "Deployment" told a first-time user this was a deployment tool, and it also collided
  // with the Kubernetes object of that name.
  it('never calls a target a Deployment', () => {
    expect(kindLabel('target')).toBe('Operational target');
    expect(kindLabelPlural('target')).toBe('Operational targets');
  });

  // A source is the ingestion seam a snapshot was built from. A collector observes a real
  // environment and emits evidence; OCI, local and cache observe nothing, so the product
  // word is deliberately "Data source" and never "Collector".
  it('calls a source a data source, never a collector', () => {
    expect(kindLabel('source')).toBe('Data source');
    expect(kindLabelPlural('source')).toBe('Data sources');
  });
});

describe('attentionCategoryLabel', () => {
  // The TS union in schema.ts is type-only, so the drift check reads the enum the
  // backend publishes: a new backend category fails here until it gets a chip.
  const spec = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'generated/openapi.json'), 'utf8'));
  const backend: string[] = spec.components.schemas.ProductAttentionItem.properties.category.enum;

  it('gives every backend category a plain-language name', () => {
    for (const c of backend) {
      expect(attentionCategoryLabel(c)).not.toBe(c);
      expect(attentionCategoryLabel(c)).toMatch(/^[A-Z]/);
    }
    expect(attentionCategoryLabel('non-compliant')).toBe('Not compliant');
    expect(attentionCategoryLabel('health')).toBe('Health check failing');
    expect(attentionCategoryLabel('readiness')).toBe('Readiness gate');
    expect(attentionCategoryLabel('undeclared')).toBe('Undeclared dependency');
    expect(attentionCategoryLabel('lifecycle')).toBe('Deprecated provider');
    expect(attentionCategoryLabel('provisioning')).toBe('Resource provisioning');
    expect(attentionCategoryLabel('cluster')).toBe('Member cluster');
    expect(backend).toContain('undeclared');
    expect(backend).toContain('readiness');
  });

  it('passes an unknown category through rather than inventing one', () => {
    expect(attentionCategoryLabel('brand-new')).toBe('brand-new');
    expect(attentionCategoryLabel(undefined)).toBe('Other');
  });
});

describe('the two identity dimensions are labeled separately', () => {
  it('linkState is revision-match certainty', () => {
    expect(linkStateLabel('exact')).toBe('Exact revision match');
    expect(linkStateLabel('inferred')).toBe('Inferred revision match');
    expect(linkStateLabel('ambiguous')).toBe('Ambiguous revision match');
    expect(linkStateLabel('unresolved')).toBe('Unresolved revision');
    expect(linkStateTone('exact')).toBe('ok');
    expect(linkStateTone('ambiguous')).toBe('warn');
  });

  it('retrievability is a distinct dimension: exact match can be non-retrievable', () => {
    // Retrievable content, regardless of the identity class.
    expect(retrievabilityLabel('exact', true)).toBe('Retrievable content');
    expect(retrievabilityLabel('missing-digest', true)).toBe('Retrievable content');
    expect(retrievabilityTone('exact', true)).toBe('ok');
    // Not retrievable: each class explains why.
    expect(retrievabilityLabel('no-ref', false)).toBe('No canonical reference');
    expect(retrievabilityLabel('local', false)).toBe('Local reference (not retrievable)');
    expect(retrievabilityLabel('mutable', false)).toBe('Mutable reference (not retrievable)');
    // A genuine inconsistency is an error tone; a consistent-but-unfetchable ref is neutral.
    expect(retrievabilityTone('digest-mismatch', false)).toBe('err');
    expect(retrievabilityTone('malformed', false)).toBe('err');
    expect(retrievabilityTone('no-ref', false)).toBe('neutral');
  });
});

describe('knowledge + source-health labels/tones', () => {
  it('labels the knowledge levels', () => {
    expect(knowledgeLabel('complete')).toBe('Complete knowledge');
    expect(knowledgeLabel('partial')).toBe('Partial knowledge');
    expect(knowledgeLabel('unavailable')).toBe('Source unavailable');
    expect(knowledgeTone('complete')).toBe('ok');
    expect(knowledgeTone('unavailable')).toBe('err');
    // Partial is the unknown family: neutral, never a status tone.
    expect(knowledgeTone('partial')).toBe('neutral');
  });
  it('labels source health', () => {
    expect(sourceHealthLabel('available')).toBe('Available');
    expect(sourceHealthLabel('stale')).toBe('Stale');
    expect(sourceHealthLabel('incomplete')).toBe('Incomplete');
    expect(sourceHealthTone('available')).toBe('ok');
    expect(sourceHealthTone('unavailable')).toBe('err');
    expect(sourceHealthTone('partial')).toBe('neutral');
  });
});

describe('relationship provenance', () => {
  it('has a human word for every value the backend can send', () => {
    // pkg/fleet/neighborhood.go declares enum:"declared,observed,declared+observed";
    // the merged value used to fall through and print as the raw wire token.
    expect(provenanceLabel('declared')).toBe('Expected');
    expect(provenanceLabel('observed')).toBe('Observed');
    expect(provenanceLabel('declared+observed')).toBe('Expected and observed');
    expect(provenanceLabel('inferred')).toBe('Inferred');
    // An inferred-only edge already reads as inferred in its difference: no second chip.
    expect(provenanceIsImplied(undefined, 'inferred')).toBe(true);
    expect(provenanceIsImplied(undefined, 'declared')).toBe(false);
    expect(provenanceLabel('declared+observed')).not.toMatch(/\+/);
  });

  it('knows when the reconciliation badge has already said it', () => {
    expect(provenanceIsImplied('matched', 'declared+observed')).toBe(true);
    expect(provenanceIsImplied('expected-not-observed', 'declared')).toBe(true);
    expect(provenanceIsImplied('observed-not-expected', 'observed')).toBe(true);
  });

  it('keeps provenance that still carries news', () => {
    // "Insufficient evidence" does not tell you the edge was declared -- that is new.
    expect(provenanceIsImplied('insufficient', 'declared')).toBe(false);
    // An unexpected pairing is surfaced, never silently dropped.
    expect(provenanceIsImplied('matched', 'observed')).toBe(false);
    expect(provenanceIsImplied(undefined, 'declared')).toBe(false);
    expect(provenanceIsImplied('matched', undefined)).toBe(false);
  });
});

describe('one word per state across the product vocabularies', () => {
  it('a compliance badge and the triage category that describes it agree', () => {
    // These two tables are read side by side on the overview: the category chip filters
    // the list, the badge labels the rows. "Not compliant" next to "Non-Compliant" was
    // two words for one state on one screen.
    expect(statusLabel('NonCompliant')).toBe(attentionCategoryLabel('non-compliant'));
    expect(statusLabel('Unknown')).toBe('Unknown');
  });

  it('spells status labels as words, not as the wire enum with hyphens', () => {
    for (const s of ['Compliant', 'NonCompliant', 'Invalid', 'Unknown', 'NotEvaluated']) {
      expect(statusLabel(s)).not.toMatch(/-/);
      expect(statusLabel(s)).toMatch(/^[A-Z][a-z]*( [a-z]+)*$/);
    }
  });
});

describe('the fleet-wide source health tally', () => {
  it('names only the buckets that exist, least healthy first', () => {
    // Least-healthy first is the point: the number that changes what a reader does
    // must not be the last one they reach.
    const parts = sourceHealthTallyParts({ total: 9, available: 5, partial: 1, stale: 2, unavailable: 1 });
    expect(parts.map((p) => p.status)).toEqual(['unavailable', 'stale', 'partial', 'available']);
    expect(parts.map((p) => p.text)).toEqual(['1 unavailable', '2 stale', '1 partial', '5 available']);
    // Empty buckets are absent, not printed as "0 stale".
    expect(sourceHealthTallyParts({ total: 5, available: 5 }).map((p) => p.status)).toEqual(['available']);
  });

  it('never invents a bucket from a missing count', () => {
    expect(sourceHealthTallyParts(undefined)).toEqual([]);
    expect(sourceHealthTallyParts(null)).toEqual([]);
    expect(sourceHealthTallyParts({})).toEqual([]);
    // A total with no breakdown is not silently dropped either: it is all unclassified.
    expect(sourceHealthTallyParts({ total: 3 })).toEqual([{ status: '', count: 3, text: '3 unclassified' }]);
  });

  // THE COUNTERexAMPLE: the backend leaves `total` above the bucket sum when a source
  // reports a status the product has no bucket for. Folding the remainder into
  // "available" would produce a tally that adds up perfectly and lies about health.
  it('surfaces a status it does not recognize instead of absorbing it', () => {
    const parts = sourceHealthTallyParts({ total: 4, available: 3 });
    expect(parts).toEqual([
      { status: 'available', count: 3, text: '3 available' },
      { status: '', count: 1, text: '1 unclassified' },
    ]);
    // '' is not a filter value -- the caller cannot render it as a working filter link
    // for a state the backend cannot be asked about.
    expect(parts[1].status).toBe('');
  });

  it('states health as a consequence for the records, not as a second badge', () => {
    // A source page already badges the status in its header. Repeating the word beside
    // the snapshot's own knowledge caveat gives a reader two badges and no way to tell
    // which is about which -- so this says what the status COSTS.
    for (const s of ['available', 'partial', 'stale', 'unavailable', undefined]) {
      const sentence = sourceHealthSentence(s);
      expect(sentence.startsWith('This data source ')).toBe(true);
      expect(sentence.endsWith('.')).toBe(true);
      expect(sentence).toContain('snapshot');
      // Never the bare badge word standing in for an explanation.
      expect(sentence).not.toBe(sourceHealthLabel(s));
    }
    expect(sourceHealthSentence('unavailable')).toContain('nothing it holds reached the snapshot');
    // Partial is the one that is easiest to misread as "the fleet has less in it".
    expect(sourceHealthSentence('partial')).toContain('missing from the snapshot and may still exist in the fleet');
    expect(sourceHealthSentence('nonsense')).toContain('does not recognize');
  });
});

describe('evidence collector in plain words', () => {
  it('maps each known collector and keeps an unknown one verbatim', () => {
    expect(collectorLabel('k8s-observer')).toBe('Kubernetes');
    expect(collectorLabel('k8s-observer+ebpf')).toBe('Kubernetes + kernel');
    expect(collectorLabel('ebpf')).toBe('kernel');
    expect(collectorLabel('pacto-agent+ebpf')).toBe('kernel (host agent)');
    expect(collectorLabel('pacto-run+ebpf')).toBe('kernel (pacto run)');
    expect(collectorLabel('otel')).toBe('otel');
    expect(collectorLabel(undefined)).toBe('');
  });

  it('joins distinct collectors and says nothing when none was recorded', () => {
    expect(seenBy([{ collector: 'ebpf' }, { collector: 'k8s-observer' }, { collector: 'ebpf' }, {}])).toBe('kernel, Kubernetes');
    expect(seenBy([{}])).toBe('');
    expect(seenBy(undefined)).toBe('');
  });
});

describe('GAP_WORDS', () => {
  // Every gap filter value the backend publishes has words, so a chip never shows a slug.
  it('names every backend gap value', () => {
    const spec = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'generated/openapi.json'), 'utf8'));
    const gap = spec.paths['/api/fleet/entities'].get.parameters.find((p: { name: string }) => p.name === 'gap');
    expect(Object.keys(GAP_WORDS)).toEqual(gap.schema.enum);
  });
});

describe('splitEntryPoints', () => {
  it('leads with a failing health check whatever its grade, and keeps knowledge gaps secondary', () => {
    const eps = [
      { view: 'attention', severity: 'warning' },
      { view: 'attention', category: 'non-compliant', severity: 'error' },
      { view: 'attention', category: 'health', severity: 'warning' },
      { view: 'attention', category: 'stale', severity: 'warning' },
      { view: 'attention', category: 'knowledge', severity: 'info' },
    ];
    const { lead, secondary } = splitEntryPoints(eps);
    expect(lead.map((e) => e.category)).toEqual(['non-compliant', 'health']);
    expect(secondary.map((e) => e.category)).toEqual(['stale', 'knowledge']);
  });
});

describe('serviceKeyName', () => {
  it('reads the name inside a service key the way the backend parses it', () => {
    expect(serviceKeyName('k8s:prod%2FDeployment%2Fexperiments')).toBe('k8s:prod/Deployment/experiments');
    expect(serviceKeyName('acme/k8s:shop%2FCronJob%2Fa%252Fb')).toBe('k8s:shop/CronJob/a%2Fb');
    expect(serviceKeyName('payments-service')).toBe('payments-service');
  });
});

describe('replacesTip', () => {
  it('names every replacement the line folds into "+N more"', () => {
    const at = '2026-11-17T11:56:00Z';
    const r = (n: number) => ({ from: `k8s:c${n}%2Fprod%2FDeployment%2Fexperiments`, target: `prod/kubernetes/t${n}`, at });
    expect(replacesTip([r(0)], 1)).toMatch(/^Linked by the Pacto target prod\/kubernetes\/t0, which runs as the workload\. The Pacto was created .+\.$/);
    const two = replacesTip([r(0), r(1)], 2);
    expect(two).toContain('Also replaces k8s:c1/prod/Deployment/experiments (Pacto target prod/kubernetes/t1, ');
    expect(two).not.toContain('more');
    expect(replacesTip(Array.from({ length: 12 }, (_, i) => r(i)), 40)).toMatch(/ and 29 more, listed by pacto fleet get\.$/);
    expect(replacesTip([], 0)).toBe('');
  });
});

describe('replacesTip for an inferred-from link', () => {
  it('names the signer and the revision, and says when Pacto read no workload', () => {
    const r = { from: 'k8s:billing%2FDeployment%2Finvoices', link: 'inferred-from', by: 'ana@acme.example', revision: 'invoices@0.1.0' };
    expect(replacesTip([{ ...r, workload: { name: 'invoices' } }], 1)).toBe('Declared by ana@acme.example from this inferred service in revision invoices@0.1.0.');
    expect(replacesTip([r], 1)).toBe('Declared by ana@acme.example from this inferred service in revision invoices@0.1.0. Pacto read no workload for it here.');
    const pt = { from: 'k8s:prod%2FDeployment%2Fa', target: 'prod/kubernetes/a', at: '2026-11-17T11:56:00Z' };
    expect(replacesTip([pt, r], 2)).toContain('Also replaces k8s:billing/Deployment/invoices (declared by ana@acme.example in revision invoices@0.1.0).');
  });
});

describe('ownerProvenanceText', () => {
  it('says nothing for a declared or absent owner and a sentence for the rest', () => {
    expect(ownerProvenanceText('declared')).toBe('');
    expect(ownerProvenanceText(undefined)).toBe('');
    expect(ownerProvenanceText('declared-via-target')).toBe('Declared by the contract a running target reports; no revision Pacto read declares an owner');
    expect(ownerProvenanceText('attested')).toBe('Read from the repository at the commit a verified CI identity signed; routes reviews, never clears a gate');
    expect(ownerProvenanceText('inferred')).toBe('Inferred; Pacto never routes approvals or clears a gate on it');
  });
});

describe('inferred provider attention words', () => {
  it('words the declare next action and the INFERRED_PROVIDER cause', () => {
    expect(nextActionWords({ verb: 'declare', cli: 'pacto declare cart --namespace shop' })).toBe('Fix: declare the workload with pacto declare cart --namespace shop');
    expect(attentionCause('INFERRED_PROVIDER')).not.toBe('INFERRED_PROVIDER');
  });
});

describe('completenessText', () => {
  it('says a section\'s state, why and what else the fleet knows', () => {
    expect(completenessText({ state: 'declared', reason: 'pacto.yaml declares it' })).toBe('Declared: pacto.yaml declares it');
    expect(completenessText({ state: 'inferred', reason: 'read from runtime calls', note: 'it runs nowhere' })).toBe('Inferred only: read from runtime calls; it runs nowhere');
    expect(completenessText({ state: 'unknown' })).toBe('Unknown');
  });
});

describe('nextActionWords', () => {
  it('words each verb with its CLI command and never a tool', () => {
    expect(nextActionWords({ verb: 'edit-contract', cli: 'pacto ci' })).toBe('Fix: edit the contract, then run pacto ci');
    expect(nextActionWords({ verb: 'accept', cli: 'pacto accept reports' })).toBe('Fix: accept the proposed ranges with pacto accept reports');
    expect(nextActionWords({ verb: 'claim', cli: 'pacto claim stripe' })).toBe('Fix: claim this external system with pacto claim stripe');
    expect(nextActionWords({ verb: 'approve-cluster', cli: 'kubectl apply -f c.yaml' })).toBe('Fix: approve the cluster with kubectl apply -f c.yaml');
    expect(nextActionWords({ verb: 'declare' })).toBe('Fix: declare the workload');
    expect(nextActionWords({ verb: 'future-verb', cli: 'pacto x' })).toBe('Fix: future-verb with pacto x');
    expect(nextActionWords({ verb: 'edit-contract', cli: 'pacto ci', tool: 'pacto_edit' } as never)).not.toContain('pacto_edit');
    expect(nextActionWords(null)).toBe('');
    expect(nextActionWords({})).toBe('');
  });
});

describe('linkTip', () => {
  const DIGEST = `sha256:ab12${'0'.repeat(60)}`;
  it('names the full digest, since when and that the images record is unverified', () => {
    const tip = linkTip({ signal: 'image-digest', value: DIGEST, since: '2026-11-20T11:57:00Z' }, '2026-11-20T12:00:00Z');
    expect(tip).toBe(`Linked with no Pacto by the running image digest ${DIGEST} · observed · since 3 minutes ago. The images record carries a signature the operator does not verify.`);
  });
  it('never shortens a long annotation value', () => {
    const ref = `oci://ghcr.io/acme/${'a'.repeat(170)}/payments:2.3.0`;
    expect(ref.length).toBeGreaterThanOrEqual(200);
    expect(linkTip({ signal: 'annotation', value: ref })).toBe(`Linked with no Pacto by the pacto.trianalab.io/contract annotation ${ref} · observed.`);
  });
  it('says the version may be wrong when other release versions record the digest', () => {
    expect(linkTip({ signal: 'image-digest', value: DIGEST, otherVersions: ['2.2.0', '2.1.0'] }))
      .toContain('Other release versions also record its running images (2.2.0, 2.1.0), so the linked version is the newest and may be wrong.');
  });
  it('names at most five other versions and counts the rest', () => {
    const many = Array.from({ length: 149 }, (_, i) => `1.${148 - i}.0`);
    const tip = linkTip({ signal: 'image-digest', value: DIGEST, otherVersions: many });
    expect(tip).toContain('(1.148.0, 1.147.0, 1.146.0, 1.145.0, 1.144.0 and 144 more, listed by pacto fleet get)');
    expect(tip).not.toContain('1.143.0');
    expect(tip).toContain('the operator does not verify');
  });
});

describe('replacesTip for an observed link', () => {
  it('says how the operator linked the workload with no Pacto', () => {
    const at = '2026-11-17T11:56:00Z';
    const digest = { from: 'k8s:shop%2FDeployment%2Fpayments', link: 'observed', signal: 'image-digest', value: 'sha256:ab12', at };
    expect(replacesTip([digest], 1)).toMatch(/^Linked with no Pacto: the workload runs image sha256:ab12, which this service published\. The operator first linked it .+\.$/);
    const ann = { from: 'k8s:shop%2FDeployment%2Fcart', link: 'observed', signal: 'annotation', value: 'oci://ghcr.io/acme/cart:1.0.0', at };
    expect(replacesTip([ann], 1)).toContain('carries pacto.trianalab.io/contract naming oci://ghcr.io/acme/cart:1.0.0');
    expect(replacesTip([digest, ann], 2)).toContain('Also replaces k8s:shop/Deployment/cart (observed: annotation, ');
  });
});

describe('sourceMessageIsNote', () => {
  it('keeps an informational code a note on a stale or partial source', () => {
    expect(sourceMessageIsNote('stale', 'LINK_DIGEST_OFF')).toBe(true);
    expect(sourceMessageIsNote('partial', 'LINK_DIGEST_OFF')).toBe(true);
    expect(sourceMessageIsNote('available', undefined)).toBe(true);
    expect(sourceMessageIsNote('unavailable', 'NOT_FOUND')).toBe(false);
    expect(sourceMessageIsNote('stale', undefined)).toBe(false);
  });
});

describe('member cluster attention words', () => {
  it('words the CLUSTER_ISSUER_DIFFERS cause', () => {
    expect(attentionCause('CLUSTER_ISSUER_DIFFERS')).toMatch(/group mapping/);
  });
});

describe('inferredFactText', () => {
  it('names the source, the entity it read and the time, and says it never decides', () => {
    const t = inferredFactText({ value: 'team:ledger', source: 'backstage-catalog', where: 'component:default/ledger', asOf: '2026-11-14T12:00:00Z' });
    expect(t).toMatch(/^Inferred from backstage-catalog \(component:default\/ledger\) as of Nov 14, 2026, \d\d:\d\d\. /);
    expect(t).toContain('Pacto never routes approvals or clears a gate on it.');
  });
  it('leaves out what the source did not report', () => {
    expect(inferredFactText({ value: 'billing', source: 'idp' })).toBe('Inferred from idp. Pacto never routes approvals or clears a gate on it.');
  });
});
