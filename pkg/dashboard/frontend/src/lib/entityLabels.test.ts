import { describe, it, expect } from 'vitest';
import {
  kindLabel, kindLabelPlural, linkStateLabel, linkStateTone, retrievabilityLabel, retrievabilityTone,
  knowledgeLabel, knowledgeTone, sourceHealthLabel, sourceHealthTone,
  attentionCategoryLabel, ATTENTION_CATEGORIES,
  provenanceLabel, provenanceIsImplied,
  sourceHealthTallyParts, sourceHealthTally, sourceHealthSentence,
} from './entityLabels.ts';
import { statusLabel } from './format.ts';

describe('kindLabel', () => {
  it('maps kinds to product language', () => {
    expect(kindLabel('service')).toBe('Service');
    expect(kindLabel('revision')).toBe('Revision');
    expect(kindLabel('owner')).toBe('Owner');
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
  it('gives every backend category a plain-language name', () => {
    for (const c of ATTENTION_CATEGORIES) {
      expect(attentionCategoryLabel(c)).not.toBe(c);
      expect(attentionCategoryLabel(c)).toMatch(/^[A-Z]/);
    }
    expect(attentionCategoryLabel('non-compliant')).toBe('Not compliant');
    expect(attentionCategoryLabel('readiness')).toBe('Readiness gate');
  });

  // Readiness is triaged as a DIMENSION of attention, not a separate workspace, so it
  // must stay in the shared category list the overview and the triage view both read.
  it('keeps readiness in the shared category list', () => {
    expect(ATTENTION_CATEGORIES).toContain('readiness');
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
  });
  it('labels source health', () => {
    expect(sourceHealthLabel('available')).toBe('Available');
    expect(sourceHealthLabel('stale')).toBe('Stale');
    expect(sourceHealthTone('available')).toBe('ok');
    expect(sourceHealthTone('unavailable')).toBe('err');
  });
});

describe('relationship provenance', () => {
  it('has a human word for every value the backend can send', () => {
    // pkg/fleet/neighborhood.go declares enum:"declared,observed,declared+observed";
    // the merged value used to fall through and print as the raw wire token.
    expect(provenanceLabel('declared')).toBe('Expected');
    expect(provenanceLabel('observed')).toBe('Observed');
    expect(provenanceLabel('declared+observed')).toBe('Expected and observed');
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
    expect(sourceHealthTally({ total: 4, available: 3 })).toBe('4 data sources — 3 available, 1 unclassified.');
  });

  it('says the sentence form without a dash when there is nothing to break down', () => {
    expect(sourceHealthTally({ total: 0 })).toBe('No data sources reported.');
    expect(sourceHealthTally(undefined)).toBe('No data sources reported.');
    expect(sourceHealthTally({ total: 1, available: 1 })).toBe('1 data source, all available.');
    expect(sourceHealthTally({ total: 6, available: 6 })).toBe('6 data sources, all available.');
    expect(sourceHealthTally({ total: 6, available: 5, stale: 1 })).toBe('6 data sources — 1 stale, 5 available.');
    // "all available" is reserved for actually-all-available; one unclassified source
    // is enough to lose it.
    expect(sourceHealthTally({ total: 7, available: 6 })).not.toContain('all available');
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
    expect(sourceHealthSentence('partial')).toContain('missing from the snapshot rather than absent from the fleet');
    expect(sourceHealthSentence('nonsense')).toContain('does not recognize');
  });
});
