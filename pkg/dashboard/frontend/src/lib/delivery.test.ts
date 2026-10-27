import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  typeLabel, decisionLabel, shortDigest, decisionAsked, cliDecision, loginUrl,
  keepPending, pendingFor, dropPending, miraLink, predictionLabel, authTimeText,
  versionLabel, shortDigests, digestParts, sentence, timeLabel, personLabel, signIn, hostSignsIn,
  verdictBadge, isBreaking, andList, reasonWords, verificationLabel, outcomeTone, reasonAfter, changeSummary, actorBadge, autonomyOf, unknownHelp, undoCause, UNKNOWN_DOCS,
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

describe('sign-in and the CLI', () => {
  it('builds the decide command and the login URLs', () => {
    expect(cliDecision('prod', 'op1', 'approve')).toBe('pacto delivery approve -n prod op1 --decision approve');
    expect(loginUrl('#/delivery/inbox')).toBe('/auth/login?return=%2F%23%2Fdelivery%2Finbox');
    expect(loginUrl('/delivery/inbox', true)).toBe('/auth/login?return=%2F%23%2Fdelivery%2Finbox&fresh=1');
  });

  it('signs in through the host when it handles sign-in itself, else through /auth/login', () => {
    const w = window as unknown as { __pactoSignIn?: (h: string, f: boolean) => void };
    const assign = vi.fn();
    vi.stubGlobal('location', { assign });
    try {
      expect(hostSignsIn()).toBe(false);
      signIn('#/delivery/inbox', true);
      expect(assign).toHaveBeenCalledWith('/auth/login?return=%2F%23%2Fdelivery%2Finbox&fresh=1');
      const hook = vi.fn();
      w.__pactoSignIn = hook;
      expect(hostSignsIn()).toBe(true);
      signIn('#/delivery/inbox');
      expect(hook).toHaveBeenCalledWith('#/delivery/inbox', false);
      expect(assign).toHaveBeenCalledTimes(1);
    } finally {
      delete w.__pactoSignIn;
      vi.unstubAllGlobals();
    }
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

  it('names people: a known name, the signed-in person, a service account, a subject', () => {
    expect(personLabel('https://idp#u1', { 'https://idp#u1': 'Ann' })).toBe('Ann');
    expect(personLabel('https://idp#u1', null, { user: 'https://idp#u1', name: 'Me' })).toBe('Me');
    expect(personLabel('https://idp#u1', null, { user: 'other', name: 'Me' })).toBe('u1');
    expect(personLabel('https://idp#u1', null, { user: 'https://idp#u1' })).toBe('u1');
    expect(personLabel('system:serviceaccount:agents:pay-agent')).toBe('pay-agent');
    expect(personLabel('kind-admin')).toBe('kind-admin');
    expect(personLabel('#x')).toBe('#x');
    expect(personLabel('weird#')).toBe('weird#');
    expect(personLabel(undefined)).toBe('');
  });
});

describe('what a check concluded', () => {
  it('reads a passed evidence check as Checked, with a chip for what can break', () => {
    expect(verdictBadge({ gate: 'pacto.diff', result: 'Pass', citation: { classification: 'BREAKING' } })).toEqual({ label: 'Checked', tone: 'badge-neutral', chip: 'Breaking' });
    expect(verdictBadge({ gate: 'pacto.impact', result: 'Pass', citation: { classification: 'POTENTIAL_BREAKING' } })).toEqual({ label: 'Checked', tone: 'badge-neutral', chip: 'May break' });
    expect(verdictBadge({ gate: 'pacto.diff', result: 'Pass', citation: { classification: 'NON_BREAKING' } })).toEqual({ label: 'Checked', tone: 'badge-neutral', chip: '' });
    expect(verdictBadge({ gate: 'pacto.diff', result: 'Unknown' })).toEqual({ label: 'Unknown', tone: 'badge-warn', chip: '' });
    expect(verdictBadge({ gate: 'pacto.published', result: 'Pass' })).toEqual({ label: 'Pass', tone: 'badge-ok', chip: '' });
    expect(verdictBadge({ gate: 'mira.health', result: 'Fail' })).toEqual({ label: 'Fail', tone: 'badge-err', chip: '' });
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
