import { describe, it, expect } from 'vitest';
import {
  guaranteeTerms, mismatchText, guaranteeBadge, sunsetPassed, lifecycleBadge, lifecycleText,
  environmentBadge, stageBadge, locationText,
} from './behavior.ts';
import { todayUTC } from './dateFormat.ts';

describe('guaranteeTerms', () => {
  it('lists the offered vocabulary in order and skips what is absent', () => {
    expect(guaranteeTerms({ timeout: '30s', delivery: 'at-least-once', idempotent: true, profile: '1' })).toEqual([
      { label: 'Delivery', bound: '', value: 'at-least-once' },
      { label: 'Timeout', bound: '', value: '30s' },
      { label: 'Idempotent', bound: '', value: 'yes' },
    ]);
    expect(guaranteeTerms(null)).toEqual([]);
    expect(guaranteeTerms({ idempotent: false })).toEqual([]);
  });

  it('spells out the bound a required duration has to meet', () => {
    expect(guaranteeTerms({ dedupeWindow: '5m', retention: '168h', timeout: '1s', ordering: 'per-key' }, true).map((t) => `${t.bound} ${t.value}`.trim()))
      .toEqual(['per-key', 'at least 5m', 'at least 168h', 'at most 1s']);
  });
});

describe('mismatchText', () => {
  const base = { consumer: 'c', provider: 'p', interface: 'orders' };
  it('names what is required and what is offered', () => {
    expect(mismatchText({ ...base, guarantee: 'delivery', required: 'at-least-once', offered: 'at-most-once' }))
      .toBe('orders: Delivery at-least-once required, at-most-once offered');
    expect(mismatchText({ ...base, guarantee: 'dedupeWindow', required: '5m' })).toBe('orders: Dedupe window at least 5m required, nothing offered');
    expect(mismatchText({ ...base, guarantee: 'idempotent', required: 'true' })).toBe('orders: Idempotent yes required, nothing offered');
    expect(mismatchText({ ...base, guarantee: 'novel', required: 'x', offered: 'y' })).toBe('orders: novel x required, y offered');
  });
  it('names a missing interface', () => {
    expect(mismatchText({ ...base, guarantee: 'interface', required: 'orders' })).toBe('orders: the provider declares no such interface');
  });
});

describe('guaranteeBadge', () => {
  it('keeps Refused apart from Not verified', () => {
    expect(guaranteeBadge('unmet')).toMatchObject({ label: 'Refused', tone: 'err' });
    expect(guaranteeBadge('unknown')).toMatchObject({ label: 'Not verified', tone: 'neutral' });
    expect(guaranteeBadge('unknown', 'the provider is hidden')?.title).toBe('Not verified: the provider is hidden.');
    expect(guaranteeBadge('met', 'ignored')).toMatchObject({ label: 'Guarantees met', tone: 'ok' });
    expect(guaranteeBadge('')).toBeNull();
    expect(guaranteeBadge('novel')).toBeNull();
  });
});

describe('lifecycle', () => {
  const l = { deprecated: '2026-01-01', sunset: '2026-06-01', successor: 'orders-v2' };
  it('turns louder on and after the sunset date', () => {
    expect(sunsetPassed(l, '2026-05-31')).toBe(false);
    expect(sunsetPassed(l, '2026-06-01')).toBe(true);
    expect(sunsetPassed({ deprecated: '2026-01-01' }, '2099-01-01')).toBe(false);
    expect(sunsetPassed(null)).toBe(false);
    expect(lifecycleBadge(l, '2026-05-31')).toMatchObject({ label: 'Deprecated', tone: 'warn' });
    expect(lifecycleBadge(l, '2026-06-02')).toMatchObject({ label: 'Past sunset', tone: 'err' });
    expect(lifecycleBadge(l, '2026-06-02')?.title).toMatch(/may be removed in any release/);
    expect(lifecycleBadge(null)).toBeNull();
    expect(lifecycleBadge({ deprecated: '' })).toBeNull();
  });
  it('reads the schedule as one line', () => {
    expect(lifecycleText(l)).toBe('Deprecated Jan 1, 2026 · sunset Jun 1, 2026 · successor orders-v2');
    expect(lifecycleText({ deprecated: 'soon' })).toBe('Deprecated soon');
  });
  it('reads today in UTC', () => {
    expect(todayUTC(Date.UTC(2026, 9, 30, 23, 59))).toBe('2026-10-30');
    expect(sunsetPassed({ deprecated: '2000-01-01', sunset: '2000-01-02' })).toBe(true);
  });
});

describe('environment and stage words', () => {
  it('names each environment status', () => {
    expect(environmentBadge('pass')).toEqual({ label: 'Passed', tone: 'ok' });
    expect(environmentBadge('fail')).toEqual({ label: 'Failed', tone: 'err' });
    expect(environmentBadge('unverified').label).toBe('Not verified');
    expect(environmentBadge('skip').label).toBe('Not checked');
    expect(environmentBadge(undefined).label).toBe('Unknown');
  });
  it('names each stage and keeps its reason', () => {
    expect(stageBadge({ name: 'structural', status: 'pass' })).toEqual({ label: 'Structure passed', tone: 'ok', title: 'Structure passed' });
    expect(stageBadge({ name: 'environments', status: 'skip', reason: 'policy failed' })).toEqual({ label: 'Environments not run', tone: 'neutral', title: 'Environments: policy failed' });
    expect(stageBadge({ name: 'policy', status: 'fail' }).tone).toBe('err');
    expect(stageBadge({ name: 'novel', status: 'odd' })).toMatchObject({ label: 'novel odd', tone: 'neutral' });
  });
  it('locates a finding in its file', () => {
    expect(locationText({ file: 'envs/prod.yaml', line: 3, column: 5 })).toBe('envs/prod.yaml:3:5');
    expect(locationText({ file: 'envs/prod.yaml', line: 3 })).toBe('envs/prod.yaml:3');
    expect(locationText({ file: 'envs/prod.yaml' })).toBe('envs/prod.yaml');
    expect(locationText({ line: 3 })).toBe('');
  });
});
