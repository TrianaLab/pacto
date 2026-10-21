import { describe, it, expect, beforeEach } from 'vitest';
import {
  typeLabel, decisionLabel, shortDigest, decisionAsked, cliDecision, loginUrl,
  keepPending, pendingFor, dropPending, miraLink, predictionLabel, authTimeText,
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
