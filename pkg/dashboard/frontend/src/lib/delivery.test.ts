import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  typeLabel, decisionLabel, shortDigest, decisionAsked, cliDecision,
  keepPending, pendingFor, dropPending, miraLink, miraServiceLink, entityMiraURL, predictionLabel, authTimeText,
  versionLabel, shortDigests, digestParts, sentence, timeLabel,
  verdictBadge, isBreaking, andList, reasonWords, verificationLabel, outcomeTone, reasonAfter, changeSummary, actorBadge, autonomyOf, unknownHelp, undoCause, UNKNOWN_DOCS,
  exceptParam, exceptList, offerLabel, checkText, watchText, progressStamp, isCommand, offerRequest, offerReason, lifecycleWords,
  causeText, historyWindow, windowSince,
} from './delivery.ts';
import {
  parseHash, deliveryInboxUrl, deliveryEnvironmentsUrl, deliveryPublishUrl, deliveryAutonomyUrl, deliveryHistoryUrl, deliveryChangeUrl,
} from './router.ts';

describe('delivery labels', () => {
  it('names types, decisions and predictions in plain words', () => {
    // The CRD, the CLI and the docs say freeze; so does the dashboard.
    expect(typeLabel('delivery.pacto.trianalab.io/unfreeze')).toBe('Unfreeze');
    expect(typeLabel('delivery.pacto.trianalab.io/freeze')).toBe('Freeze');
    expect(typeLabel('delivery.pacto.trianalab.io/acknowledge')).toBe('Acknowledge upstream');
    expect(typeLabel('example.com/migrate')).toBe('example.com/migrate');
    expect(typeLabel(undefined)).toBe('Change');
    expect(decisionLabel('accept-unknown')).toBe('Accept the unknown');
    expect(decisionLabel('other')).toBe('other');
    expect(decisionLabel(undefined)).toBe('');
    expect(predictionLabel('Held')).toMatch(/person/);
    expect(predictionLabel('Odd')).toBe('Odd');
    expect(predictionLabel(undefined)).toBe('');
  });

  it('shortens digests and reads sign-in times', () => {
    expect(shortDigest('sha256:0123456789abcdef')).toBe('0123456789ab');
    expect(shortDigest('abc')).toBe('abc');
    expect(shortDigest(undefined)).toBe('');
    // A level name is not a digest: cut, it reads auto-nonbrea.
    expect(shortDigest('auto-nonbreaking')).toBe('auto-nonbreaking');
    expect(authTimeText('1792560000')).toBe(new Date(1792560000 * 1000).toISOString());
    expect(authTimeText('soon')).toBe('soon');
    expect(authTimeText(undefined)).toBe('');
  });
});

describe('decisionAsked', () => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- fixture
  const op = (status: any): any => ({ metadata: { name: 'x' }, spec: {}, status });
  it('asks what the plan named before the change ships', () => {
    expect(decisionAsked(op({ plan: { floor: { decision: 'approve' } } }))).toBe('approve');
  });
  it('asks to accept the unknown after it shipped or when the plan names nothing', () => {
    expect(decisionAsked(op({ applied: true, plan: { floor: { decision: 'approve' } } }))).toBe('accept-unknown');
    expect(decisionAsked(op({}))).toBe('accept-unknown');
    expect(decisionAsked(undefined)).toBe('accept-unknown');
  });
});

describe('the CLI and the decision kept across a fresh login', () => {
  it('builds the decide command', () => {
    expect(cliDecision('prod', 'op1', 'approve')).toBe('pacto delivery approve -n prod op1 --decision approve');
  });


  describe('the decision kept across a fresh login', () => {
    beforeEach(() => sessionStorage.clear());
    it('is returned only for the same change and can be dropped', () => {
      keepPending({ namespace: 'prod', name: 'op1', decision: 'reject', note: 'no' });
      expect(pendingFor('prod', 'op1')).toEqual({ namespace: 'prod', name: 'op1', decision: 'reject', note: 'no' });
      expect(pendingFor('prod', 'op2')).toBeNull();
      dropPending();
      expect(pendingFor('prod', 'op1')).toBeNull();
    });
    it('survives garbage in storage', () => {
      sessionStorage.setItem('pacto.delivery.pending', '{');
      expect(pendingFor('prod', 'op1')).toBeNull();
    });
  });
});

describe('miraLink', () => {
  const query = JSON.stringify({ signal: 'traces', where: [{ attr: 'service.name', eq: 'payments' }, { field: 'status_code', eq: 2 }, { attr: 'x', gte: 3 }, { nothing: 1 }] });
  it('links to Mira with the same filter', () => {
    const href = miraLink({ query }, 'https://mira.example/')!;
    expect(href.startsWith('https://mira.example/#/traces?q=')).toBe(true);
    expect(decodeURIComponent(href.split('q=')[1].split('&')[0])).toBe('service.name="payments" status_code=2 x>=3');
    expect(href.endsWith('&range=all')).toBe(true);
  });
  it('gives no link without a web address or a Mira query', () => {
    expect(miraLink({ query }, '')).toBeNull();
    expect(miraLink({ query }, 'javascript:alert(1)')).toBeNull();
    expect(miraLink({}, 'https://m')).toBeNull();
    expect(miraLink(undefined, 'https://m')).toBeNull();
    expect(miraLink({ query: 'not json' }, 'https://m')).toBeNull();
    expect(miraLink({ query: '{"where":[]}' }, 'https://m')).toBeNull();
    expect(miraLink({ query: '{"signal":"logs"}' }, 'https://m')).toBe('https://m/#/logs?q=&range=all');
  });
});

describe('delivery routes', () => {
  it('parse and build every delivery view', () => {
    expect(parseHash('#/delivery')).toEqual({ view: 'delivery-inbox', params: {} });
    expect(parseHash(deliveryInboxUrl()).view).toBe('delivery-inbox');
    expect(parseHash('#/delivery/nonsense').view).toBe('delivery-inbox');
    expect(parseHash(deliveryEnvironmentsUrl()).view).toBe('delivery-environments');
    expect(parseHash(deliveryPublishUrl()).view).toBe('delivery-publish');
    expect(parseHash(deliveryHistoryUrl())).toEqual({ view: 'delivery-history', params: {} });
    expect(deliveryHistoryUrl({ namespace: 'prod' })).toBe('#/delivery/history');
    expect(parseHash(deliveryHistoryUrl({ namespace: 'prod', stage: 'pay ments' }))).toEqual({
      view: 'delivery-history', params: { namespace: 'prod', stage: 'pay ments' },
    });
    expect(parseHash(deliveryChangeUrl('prod', 'a/b'))).toEqual({ view: 'delivery-change', params: { namespace: 'prod', name: 'a/b' } });
  });

  it('keep the page and the filter of every list in the URL', () => {
    expect(parseHash(deliveryInboxUrl({ held: 25, q: 'pay' }))).toEqual({ view: 'delivery-inbox', params: { held: '25', q: 'pay' } });
    expect(parseHash(deliveryAutonomyUrl({ offset: 50, blockers: 25 })).params).toEqual({ offset: '50', blockers: '25' });
    expect(parseHash(deliveryPublishUrl({ limit: 100 })).params).toEqual({ limit: '100' });
    expect(parseHash('#/delivery/environments?namespace=prod&offset=25&junk=1').params).toEqual({ offset: '25' });
    expect(deliveryHistoryUrl({ namespace: 'prod', stage: 'pay', offset: 50 })).toBe('#/delivery/history?namespace=prod&stage=pay&offset=50');
    expect(deliveryHistoryUrl({ offset: 10, q: '' })).toBe('#/delivery/history?offset=10');
    // A service page's exact service and domain survive the round trip; a
    // domain without a service narrows nothing.
    expect(parseHash(deliveryEnvironmentsUrl({ service: 'pay', domain: 'ghcr.io/a' })).params).toEqual({ service: 'pay', domain: 'ghcr.io/a' });
    expect(parseHash(deliveryHistoryUrl({ service: 'pay' })).params).toEqual({ service: 'pay' });
    expect(parseHash('#/delivery/history?domain=ghcr.io%2Fa').params).toEqual({});
    // A figure's links carry the Autonomy level and the folded station's except.
    expect(parseHash(deliveryAutonomyUrl({ level: 'advise' })).params).toEqual({ level: 'advise' });
    const except = exceptParam(['prod', 'a,b']);
    expect(parseHash(deliveryEnvironmentsUrl({ except })).params).toEqual({ except });
    expect(parseHash(deliveryInboxUrl({ except })).params).toEqual({ except });
  });
});

describe('names and versions', () => {
  const D = 'sha256:' + 'ab'.repeat(32);
  it('names a digest by its version when known', () => {
    expect(versionLabel(D, { [D]: '2.4.0' })).toBe('2.4.0');
    expect(versionLabel(D, {})).toBe('abababababab');
    expect(versionLabel(D, null)).toBe('abababababab');
    expect(versionLabel(undefined, { [D]: '1' })).toBe('');
  });

  it('shortens full digests inside sentences and turns notes into sentences', () => {
    expect(shortDigests(`promote ${D} on Stage x`)).toBe('promote sha256:abababababab on Stage x');
    expect(shortDigests(undefined)).toBe('');
    expect(shortDigests(`${D} -> x`, { [D]: '2.4.0' })).toBe('2.4.0 (abababababab) -> x');
    expect(sentence(`the Stage runs ${D}`)).toBe('The Stage runs sha256:abababababab.');
    expect(sentence('Done!')).toBe('Done!');
    expect(sentence('  ')).toBe('');
    expect(sentence(undefined)).toBe('');
    expect(sentence('k8s:pay/x calls fraud')).toBe('k8s:pay/x calls fraud.');
    expect(sentence('records, verified')).toBe('Records, verified.');
  });

  it('says how a change ended once, with no code twice', () => {
    expect(verificationLabel('AcceptedUnknown')).toBe('A person accepted the unknown');
    expect(verificationLabel('Odd')).toBe('Odd');
    expect(verificationLabel(undefined)).toBe('');
    expect(outcomeTone('Succeeded')).toBe('badge-ok');
    expect(outcomeTone('Rejected')).toBe('badge-err');
    expect(outcomeTone(undefined)).toBe('badge-neutral');
    expect(reasonAfter('AcceptedUnknown: AcceptedUnknown: carol: low traffic', 'Succeeded', 'AcceptedUnknown')).toBe('carol: low traffic');
    expect(reasonAfter('Succeeded', 'Succeeded')).toBe('');
    expect(reasonAfter('VerifyFailed: health', 'Failed', undefined)).toBe('VerifyFailed: health');
    expect(reasonAfter(undefined)).toBe('');
  });

  it('splits digests out of a sentence, resolving short ones only when one known id starts them', () => {
    const E = 'sha256:' + 'ab'.repeat(6) + 'cd'.repeat(26);
    expect(digestParts(`from ${D} on`)).toEqual([{ text: 'from ' }, { text: 'abababababab', full: D }, { text: ' on' }]);
    expect(digestParts('seq 2 from sha256:abababababab', [D, undefined, 'x'])).toEqual([{ text: 'seq 2 from ' }, { text: 'abababababab', full: D }]);
    expect(digestParts('sha256:abababababab', [D, E])).toEqual([{ text: 'sha256:abababababab' }]);
    expect(digestParts('sha256:abababababab', [D, D])).toEqual([{ text: 'abababababab', full: D }]);
    expect(digestParts('sha256:ababababababab is not one')).toEqual([{ text: 'sha256:ababababababab is not one' }]);
    expect(digestParts(undefined)).toEqual([]);
    // A fork: the record the history kept is known by its short id; a whole id in the text is cut short too.
    const F = 'sha256:' + 'ef'.repeat(32);
    expect(digestParts(`seq 2 has two records, sha256:abababababab and ${F}: the stream forked`, [D])).toEqual([
      { text: 'seq 2 has two records, ' }, { text: 'abababababab', full: D }, { text: ' and ' },
      { text: 'efefefefefef', full: F }, { text: ': the stream forked' }]);
  });

  it('shows times to the minute', () => {
    expect(timeLabel('2026-10-21T11:00:05Z')).toBe('2026-10-21 11:00 UTC');
    expect(timeLabel('2026-10-21T11:00:05.123Z')).toBe('2026-10-21 11:00 UTC');
    expect(timeLabel('2026-10-21T11:00Z')).toBe('2026-10-21 11:00 UTC');
    expect(timeLabel('2026-10-21T11:00:05+02:00')).toBe('2026-10-21T11:00:05+02:00');
    expect(timeLabel(undefined)).toBe('');
  });

});

describe('what a check concluded', () => {
  it('reads a passed evidence check as Checked, with a chip for what can break', () => {
    expect(verdictBadge({ gate: 'pacto.diff', result: 'Pass', citation: { classification: 'BREAKING' } })).toEqual({ label: 'Checked', tone: 'badge-neutral', chip: 'Breaking' });
    expect(verdictBadge({ gate: 'pacto.impact', result: 'Pass', citation: { classification: 'POTENTIAL_BREAKING' } })).toEqual({ label: 'Checked', tone: 'badge-neutral', chip: 'May break' });
    expect(verdictBadge({ gate: 'pacto.diff', result: 'Pass', citation: { classification: 'NON_BREAKING' } })).toEqual({ label: 'Checked', tone: 'badge-neutral', chip: '' });
    expect(verdictBadge({ gate: 'pacto.diff', result: 'Unknown' })).toEqual({ label: 'Unknown', tone: 'badge-neutral', chip: '' });
    expect(verdictBadge({ gate: 'pacto.published', result: 'Pass' })).toEqual({ label: 'Pass', tone: 'badge-ok', chip: '' });
    expect(verdictBadge({ gate: 'mira.health', result: 'Fail' })).toEqual({ label: 'Fail', tone: 'badge-err', chip: '' });
    expect(verdictBadge({ gate: 'pacto.environment', result: 'Pass', citation: { applicable: 'false' } })).toEqual({ label: 'N/A', tone: 'badge-neutral', chip: '' });
    expect(verdictBadge({ gate: 'pacto.environment', result: 'Pass' })).toEqual({ label: 'Checked', tone: 'badge-neutral', chip: '' });
    expect(verdictBadge({})).toEqual({ label: '', tone: 'badge-neutral', chip: '' });
  });

  it('says whether a plan is breaking and lists names with no serial comma', () => {
    expect(isBreaking([{}, { citation: { classification: 'BREAKING' } }])).toBe(true);
    expect(isBreaking([{ citation: { classification: 'POTENTIAL_BREAKING' } }])).toBe(false);
    expect(isBreaking(undefined)).toBe(false);
    expect(andList([])).toBe('');
    expect(andList(['a'])).toBe('a');
    expect(andList(['a', 'b'])).toBe('a and b');
    expect(andList(['a', 'b', 'c'])).toBe('a, b and c');
    // The folded station's filter survives any name, and a bad value is no filter.
    expect(exceptList(exceptParam(['prod,eu', '', 'test'])!)).toEqual(['prod,eu', '', 'test']);
    expect(exceptParam([])).toBeUndefined();
    expect(exceptList('nope')).toEqual([]);
    expect(exceptList('[1]')).toEqual([]);
    expect(exceptList('{}')).toEqual([]);
    expect(reasonWords('ApprovalRequired')).toBe('Waiting for a person to approve');
    expect(reasonWords('ExecutionDeadline')).toBe('Execution deadline');
    expect(reasonWords('the plan changed')).toBe('the plan changed');
    expect(andList(['a', 'b', 'c', 'd'], 3)).toBe('a, b, c and d');
    expect(andList(['a', 'b', 'c', 'd', 'e'], 3, 'or')).toBe('a, b, c or 2 others');
    expect(andList(Array.from({ length: 1003 }, (_, i) => `s${i}`), 3)).toBe('s0, s1, s2 and 1,000 others');
  });
});

describe('what a change says about itself', () => {
  const D1 = 'sha256:' + '1'.repeat(64);
  const D2 = 'sha256:' + '2'.repeat(64);
  const promote = (status: Record<string, string>) => ({ spec: { type: 'delivery.pacto.trianalab.io/promote', stage: 'payments', params: { from: D1, to: D2 } }, status });

  it('says what a change asked and where it stands, with no empty fields or raw words', () => {
    const versions = { [D1]: '2.3.0', [D2]: '2.4.0' };
    expect(changeSummary(promote({ phase: 'Concluded', outcome: 'Failed', verification: 'Unverified' }), 'prod', versions))
      .toBe('Promote payments in prod from 2.3.0 to 2.4.0 ended: it failed, not verified.');
    expect(changeSummary(promote({ phase: 'Concluded', outcome: 'Succeeded', verification: 'AcceptedUnknown' }), 'prod', versions))
      .toBe('Promote payments in prod from 2.3.0 to 2.4.0 ended: it succeeded, a person accepted the unknown.');
    expect(changeSummary(promote({ phase: 'Concluded', outcome: 'Rejected' }), 'prod', versions)).toBe('Promote payments in prod from 2.3.0 to 2.4.0 ended: it was rejected.');
    expect(changeSummary(promote({ phase: 'Concluded' }), 'prod', versions)).toBe('Promote payments in prod from 2.3.0 to 2.4.0 ended.');
    expect(changeSummary(promote({ phase: 'Held' }), 'prod')).toBe('Promote payments in prod from 111111111111 to 222222222222 waits for a person.');
    expect(changeSummary(promote({ phase: '' }), 'prod', versions)).toBe('Promote payments in prod from 2.3.0 to 2.4.0 is not admitted yet.');
    expect(changeSummary(promote({ phase: 'Odd' }), 'prod', versions)).toBe('Promote payments in prod from 2.3.0 to 2.4.0 is odd.');
    expect(changeSummary({ spec: { type: 'delivery.pacto.trianalab.io/rollback', stage: 'payments', params: { to: D1 } }, status: { phase: 'Running' } }, 'prod', versions))
      .toBe('Roll back payments in prod to 2.3.0 is running.');
    expect(changeSummary({ spec: { type: 'delivery.pacto.trianalab.io/unfreeze', stage: 'payments' } }, 'prod')).toBe('Unfreeze payments in prod is not admitted yet.');
    expect(changeSummary(null, 'prod')).toBe('');
    expect(changeSummary(promote({ phase: 'Concluded', outcome: 'Failed', verification: 'Unverified' }), 'prod')).not.toMatch(/: :|pacto\.trianalab\.io|->/);
  });

  it('names a person, an agent or the controller with a chip', () => {
    expect(actorBadge('human')?.label).toBe('Person');
    expect(actorBadge('agent')?.label).toBe('Agent');
    expect(actorBadge('controller')?.label).toBe('Controller');
    expect(actorBadge('')).toBeNull();
    expect(actorBadge(undefined)).toBeNull();
  });

  it('says how to resolve each kind of Unknown', () => {
    expect(unknownHelp({ gate: 'health', citation: { provider: 'mira' } })).toContain('Mira');
    expect(unknownHelp({ gate: 'pacto.runtime' })).toContain('contract status');
    expect(unknownHelp({ gate: 'pacto.contract' })).toContain('contract status');
    expect(unknownHelp({ gate: 'pacto.impact' })).toContain('Publish a contract');
    expect(unknownHelp({ gate: 'custom' })).toContain('configured and reachable');
    expect(UNKNOWN_DOCS).toMatch(/^https:\/\/pacto\.run\/latest\/.*#what-unknown-means$/);
  });

  it('counts how far delivery ran with no person, and what only Unknown held', () => {
    const p = (over: Record<string, unknown>) => ({ type: 'delivery.pacto.trianalab.io/promote', phase: 'Concluded', actorClass: 'agent', approvals: null, ...over });
    expect(autonomyOf([
      p({}), p({ actorClass: 'controller' }),
      p({ approvals: [{ decision: 'approve' }] }),
      p({ verification: 'AcceptedUnknown', approvals: [{ decision: 'accept-unknown' }] }),
      p({ approvals: [{ decision: 'approve' }, { decision: 'accept-unknown' }] }),
      p({ actorClass: 'human' }),
      p({ phase: 'Held' }), p({ type: 'delivery.pacto.trianalab.io/rollback' }), p({ kind: 'gap' }), p({ kind: 'unrecordable' }),
    ])).toEqual({ total: 6, noPerson: 2, onlyUnknown: 1 });
    expect(autonomyOf([])).toEqual({ total: 0, noPerson: 0, onlyUnknown: 0 });
    // A promotion whose line does not say who approved it cannot be counted:
    // a person may have approved it.
    expect(autonomyOf([p({ approvals: null }), { type: 'delivery.pacto.trianalab.io/promote', phase: 'Concluded', actorClass: 'agent' }])).toBeNull();
  });

  it('reads why the controller undid a change from its reason', () => {
    expect(undoCause('RejectedByHuman', 'carol: risky')).toEqual({ by: 'carol' });
    expect(undoCause('RejectedByHuman: carol: risky')).toEqual({ by: 'carol' });
    expect(undoCause('RejectedByHuman')).toEqual({ text: 'a person rejected the unknown' });
    expect(undoCause('VerifyFailed', 'health: x')).toEqual({ text: 'a check failed after release' });
    expect(undoCause('ApplyFailed', 'boom')).toEqual({ text: 'the release did not apply' });
    expect(undoCause()).toEqual({});
  });
});

import { blockerReason } from './delivery.ts';
describe('blockerReason', () => {
  it('drops the subject the row already prints', () => {
    expect(blockerReason('orders calls cart at runtime but has no contract', 'orders')).toBe('It calls cart at runtime but has no contract');
    expect(blockerReason('simulated: incident for aml-media.', 'aml-media')).toBe('simulated: incident.');
    expect(blockerReason('a release failed here', 'orders')).toBe('a release failed here');
    expect(blockerReason('incident for orders-api', 'orders')).toBe('incident for orders-api');
  });
});

describe('miraServiceLink', () => {
  it("opens a service's spans in Mira's trace view", () => {
    expect(miraServiceLink('payments', 'https://mira.example/')).toBe('https://mira.example/#/traces?q=service.name%3D%22payments%22&range=-1h');
    expect(miraServiceLink('payments', 'https://m', '-6h')).toBe('https://m/#/traces?q=service.name%3D%22payments%22&range=-6h');
    expect(miraServiceLink('a"b', 'https://m')).toBe('https://m/#/traces?q=service.name%3D%22a%22b%22&range=-1h');
  });
  it('is null without a web address', () => {
    expect(miraServiceLink('payments', 'javascript:alert(1)')).toBeNull();
    expect(miraServiceLink('payments', '')).toBeNull();
    expect(miraServiceLink('payments', undefined)).toBeNull();
  });
});

describe('entityMiraURL', () => {
  it('gives the Mira address only to a caller with nothing hidden', () => {
    expect(entityMiraURL({ miraURL: 'https://m', auth: { hidden: false } })).toBe('https://m');
    expect(entityMiraURL({ miraURL: 'https://m', auth: { hidden: true } })).toBe('');
    expect(entityMiraURL(undefined)).toBe('');
    expect(entityMiraURL(null)).toBe('');
  });
});

describe('what a Stage row says', () => {
  it('names each offer', () => {
    expect(offerLabel('promote')).toBe('Promote');
    expect(offerLabel('unfreeze')).toBe('Unfreeze');
    expect(offerLabel('freeze')).toBe('Freeze');
    expect(offerLabel('undo')).toBe('Undo');
    expect(offerLabel('other')).toBe('other');
  });
  it('turns an offer into the request it plans, naming the version only for the candidate', () => {
    const r = { namespace: 'prod', stage: 'pay', pointer: 'sha256:a', candidate: 'sha256:b', candidateVersion: '2.0.0' };
    expect(offerRequest(r, { action: 'promote', available: true, reason: '', to: 'sha256:b' })).toEqual({
      request: { namespace: 'prod', stage: 'pay', type: 'delivery.pacto.trianalab.io/promote', to: 'sha256:b', from: 'sha256:a' }, version: '2.0.0' });
    expect(offerRequest({ ...r, pointer: '' }, { action: 'promote', available: true, reason: '', to: 'sha256:c' })).toEqual({
      request: { namespace: 'prod', stage: 'pay', type: 'delivery.pacto.trianalab.io/promote', to: 'sha256:c', from: undefined }, version: '' });
    expect(offerRequest(r, { action: 'freeze', available: true, reason: '' })).toEqual({ request: { namespace: 'prod', stage: 'pay', type: 'delivery.pacto.trianalab.io/freeze' }, version: '' });
  });
  it('never says the Ready fix twice: an offer reason drops the fix the row already shows', () => {
    const o = { action: 'promote' as const, available: false, reason: 'no eligible candidate: set spec.verify' };
    expect(offerReason(o, 'set spec.verify')).toBe('no eligible candidate');
    expect(offerReason(o, '')).toBe('no eligible candidate: set spec.verify');
    expect(offerReason(o, 'something else')).toBe('no eligible candidate: set spec.verify');
  });
  it('words a lifecycle answer: unknown never reads as yes or no', () => {
    expect(lifecycleWords.signature.signed).toBe('Signature checked');
    expect(lifecycleWords.signature.unsigned).toBe('Not signed');
    expect(lifecycleWords.signature.unknown).toBe('Signature not known here');
    expect(lifecycleWords.verified.unknown).toBe('verified: not known');
    expect(lifecycleWords.failed.unknown).toBe('failed: not known');
    expect(lifecycleWords.role.left).toBe('moved off it');
  });
  it('says when the newest re-check ran and what it found, and when it is stale', () => {
    expect(checkText({ at: '2026-11-04T09:58:00Z', result: 'Unknown', current: true })).toBe('Re-checked 2026-11-04 09:58 UTC, Unknown');
    expect(checkText({ at: '2026-11-04T09:58:00Z', result: 'Pass', current: false })).toBe('Re-checked 2026-11-04 09:58 UTC, Pass, stale: about an earlier version');
    expect(checkText(undefined)).toBe('');
  });
  it('never reads an old re-check as a pass about the running version', () => {
    expect(watchText({ status: 'Unknown', reason: 'NotRechecked' })).toBe('Not re-checked since it shipped');
    expect(watchText({ status: 'True', reason: 'Rechecked' })).toBe('Watched: the newest re-check passed');
    expect(watchText({ status: 'False', reason: 'RecheckFailed' })).toBe('The newest re-check failed');
    expect(watchText({ status: 'Unknown', reason: 'RecheckUnknown' })).toBe('The newest re-check could not decide');
    expect(watchText(undefined)).toBe('');
  });
  it('stamps where a Stage stands against its candidate, unknown never positive', () => {
    expect(progressStamp('up-to-date')).toEqual({ label: 'Up to date', tone: 'ok' });
    expect(progressStamp('can-promote')).toEqual({ label: 'Can promote', tone: 'accent' });
    expect(progressStamp('needs-person')).toEqual({ label: 'Needs a person', tone: 'warn' });
    expect(progressStamp('blocked')).toEqual({ label: 'Blocked', tone: 'err' });
    expect(progressStamp('unknown')).toEqual({ label: 'Not known', tone: 'neutral' });
    expect(progressStamp('other')).toEqual({ label: 'Not known', tone: 'neutral' });
  });
  it('tells a command to copy from a fix in words', () => {
    expect(isCommand('pacto delivery approve -n prod op-1')).toBe(true);
    expect(isCommand('a person of team-a approves it themselves; you cannot answer it')).toBe(false);
    expect(isCommand(undefined)).toBe(false);
  });
});

describe('history filters (B3)', () => {
  it('says why a change was raised: its reason, else the check, else the change that caused it', () => {
    expect(causeText({ reason: 'incident 42', operation: 'u1' })).toBe('incident 42');
    expect(causeText({ operation: 'u1', verdict: { gate: 'pacto.health', message: 'error rate 9%' } })).toBe('pacto.health: error rate 9%');
    expect(causeText({ verdict: { gate: 'pacto.health' } })).toBe('pacto.health');
    expect(causeText({ operation: 'u1' })).toBe('operation u1');
    expect(causeText(undefined)).toBe('');
  });
  it('names the window a URL holds, and custom for a timeline slice', () => {
    const now = Date.parse('2026-11-11T12:00:00Z');
    expect(historyWindow('', '', '', now)).toBe('all');
    expect(historyWindow('2026-11-04T12:00:00Z', '', '', now)).toBe('7d');
    expect(historyWindow('2026-11-10T12:00:00Z', '', '', now)).toBe('24h');
    expect(historyWindow('2026-10-12T12:00:00Z', '', '', now)).toBe('30d');
    expect(historyWindow('2026-11-11T08:00:00Z', '', '', now)).toBe('custom');
    expect(historyWindow('2026-11-10T12:00:00Z', '2026-11-11T00:00:00Z', '', now)).toBe('custom');
    expect(historyWindow('2026-11-10T12:00:00Z', '', '1', now)).toBe('custom');
    expect(windowSince('7d', now)).toBe('2026-11-04T12:00:00Z');
    expect(windowSince('all', now)).toBeUndefined();
  });
});
