import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  typeLabel, decisionLabel, shortDigest, decisionAsked, cliDecision, loginUrl,
  keepPending, pendingFor, dropPending, miraLink, predictionLabel, authTimeText,
  versionLabel, shortDigests, digestParts, sentence, timeLabel, personLabel, signIn, hostSignsIn,
} from './delivery.ts';
import {
  parseHash, deliveryInboxUrl, deliveryEnvironmentsUrl, deliveryPublishUrl, deliveryHistoryUrl, deliveryChangeUrl,
} from './router.ts';

describe('delivery labels', () => {
  it('names types, decisions and predictions in plain words', () => {
    expect(typeLabel('pacto.dev/unfreeze')).toBe('Unpause');
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
  });

  it('splits digests out of a sentence, resolving short ones only when one known id starts them', () => {
    const E = 'sha256:' + 'ab'.repeat(6) + 'cd'.repeat(26);
    expect(digestParts(`from ${D} on`)).toEqual([{ text: 'from ' }, { text: 'abababababab', full: D }, { text: ' on' }]);
    expect(digestParts('seq 2 from sha256:abababababab', [D, undefined, 'x'])).toEqual([{ text: 'seq 2 from ' }, { text: 'abababababab', full: D }]);
    expect(digestParts('sha256:abababababab', [D, E])).toEqual([{ text: 'sha256:abababababab' }]);
    expect(digestParts('sha256:abababababab', [D, D])).toEqual([{ text: 'abababababab', full: D }]);
    expect(digestParts('sha256:ababababababab is not one')).toEqual([{ text: 'sha256:ababababababab is not one' }]);
    expect(digestParts(undefined)).toEqual([]);
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
