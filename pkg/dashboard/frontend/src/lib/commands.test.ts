import { describe, it, expect } from 'vitest';
import { buildCommands, flattenCommands } from './commands';

const services = [
  { name: 'payments-api', version: '1.2.0', owner: { team: 'team/payments' } },
  { name: 'checkout', version: '0.9.0', owner: { team: 'team/checkout' } },
];

describe('buildCommands', () => {
  it('empty query shows Views and Actions only', () => {
    const groups = buildCommands('', services);
    const labels = groups.map((g) => g.label);
    expect(labels).toEqual(['Views', 'Actions']);
    expect(groups[0].items.map((i) => i.label)).toContain('Graph');
  });

  it('filters services by name', () => {
    const groups = buildCommands('payments', services);
    const svc = groups.find((g) => g.label === 'Services');
    expect(svc?.items.map((i) => i.label)).toEqual(['payments-api']);
    expect(svc?.items[0].href).toBe('#/services/payments-api');
  });

  it('matches owners and dedupes them', () => {
    const groups = buildCommands('checkout', services);
    const owners = groups.find((g) => g.label === 'Owners');
    expect(owners?.items.map((i) => i.label)).toEqual(['team/checkout']);
  });

  it('filters views and actions by label', () => {
    const groups = buildCommands('theme', services);
    expect(groups.map((g) => g.label)).toEqual(['Actions']);
    expect(groups[0].items[0].action).toBe('theme');
  });

  it('flattenCommands preserves group order', () => {
    const flat = flattenCommands(buildCommands('', services));
    expect(flat[0].label).toBe('Services'); // first View is "Services" (href #/)
    expect(flat[flat.length - 1].action).toBe('autoreload');
  });
});
