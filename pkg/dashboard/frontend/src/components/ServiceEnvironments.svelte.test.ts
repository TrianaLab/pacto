/**
 * The Environments section of a service page (#467): one row per environment the
 * current revision declares or a target reports, each fact declared, observed or
 * inferred, and an unknown never read as a positive.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ServiceEnvironments from './ServiceEnvironments.svelte';

const ASOF = '2026-11-12T10:00:00Z';
const rev = (version: string) => ({ kind: 'revision', key: `storefront-web@${version}`, label: `storefront-web@${version}`, version, href: `/fleet/revisions/storefront-web%40${version}` });
const tally = (over = {}) => ({ requested: 0, provisioning: 0, ready: 0, failed: 0, deleting: 0, unknown: 0, ...over });
const decl = (name: string, over = {}) => ({ name, file: `environments/${name}.yaml`, status: 'pass', lifetime: 'not-declared',
  findings: { total: 0, count: 0, truncated: false, items: [] }, ...over });
const group = (environment: string, over = {}) => ({ environment, targets: 0, declared: 0, notDeclared: 0, unknown: 0,
  revisions: [], revisionsTotal: 0, revisionsTruncated: false, targetsComplete: true,
  provisioning: tally(), provisioningTracked: true,
  declaration: decl(environment), owner: { provenance: 'inferred', owner: 'web-team', reason: 'the environment declares no owner; the service owner is shown' }, ...over });
const placement = (items: unknown[], over = {}) => ({ total: items.length, count: items.length, truncated: false, items,
  currentRevision: rev('1.1.0'), differences: [], differencesTotal: 0, ...over });
const history = (items: unknown[] = [], over = {}) => ({ total: items.length, count: items.length, truncated: false, items, gaps: 0, completeness: 'complete', ...over });
const stage = (environment: string, over = {}) => ({ namespace: environment, stage: 'storefront-web', service: 'storefront-web', environment, state: 'verified', ...over });

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });
function render(props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(ServiceEnvironments, { target, props: { asOf: ASOF, completeness: 'complete', service: 'storefront-web', history: history(), ...props } });
  flushSync();
  cleanup = () => { unmount(c); target.remove(); };
  return target;
}
const text = (el: Element | null | undefined) => el?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
const row = (t: Element, env: string) => t.querySelector(`[data-testid="service-environment"][data-env="${env}"]`);
const cell = (t: Element, env: string, name: string) => row(t, env)?.querySelector(`[data-cell="${name}"]`);

describe('ServiceEnvironments', () => {
  it('LifetimeNotDeclaredNeverPersistent', () => {
    const t = render({ placement: placement([group('production')]) });
    expect(text(cell(t, 'production', 'declared'))).toContain('Lifetime not declared');
    expect(text(row(t, 'production'))).not.toContain('Persistent');
  });

  it('ExpiredIsInferredWithAsOf', () => {
    const t = render({ placement: placement([group('preview', { declaration: decl('preview', { lifetime: 'temporary', expires: '2026-09-30T00:00:00Z', expired: true }) }),
      group('pr-482', { declaration: decl('pr-482', { lifetime: 'temporary', expires: '2030-01-01T00:00:00Z' }) })]) });
    expect(text(cell(t, 'preview', 'declared'))).toContain('Temporary');
    expect(text(cell(t, 'preview', 'declared'))).toContain('Expired (inferred, as of 2026-11-12 10:00 UTC)');
    expect(text(cell(t, 'pr-482', 'declared'))).toContain('Expires 2030-01-01 00:00 UTC');
  });

  it('ObservedOnlyReadsNotDeclared', () => {
    const t = render({ placement: placement([group('canary', { declaration: undefined, targets: 1, owner: { provenance: 'unknown', reason: 'not declared by the current revision' } })]) });
    expect(text(cell(t, 'canary', 'declared'))).toContain('Not declared by 1.1.0');
    expect(text(row(t, 'canary'))).toContain('Not declared');
    expect(row(t, 'canary')?.querySelector('.badge-warn')).not.toBeNull();
  });

  it('UnlabelledGroupReadsUnknownWithReason', () => {
    const t = render({ placement: placement([group('', { declaration: undefined, targets: 2, unknown: 2, owner: { provenance: 'unknown' } })]) });
    const r = row(t, '');
    expect(text(r)).toContain('(no environment label)');
    expect(r?.querySelector('.badge-neutral .hatch')).not.toBeNull();
    expect(text(cell(t, '', 'declared'))).toContain('report no environment label');
  });

  it('OwnerFallbackLabelledInferred', () => {
    const t = render({ placement: placement([group('production'),
      group('staging', { owner: { provenance: 'declared', owner: 'storefront-qa' } })]) });
    expect(text(cell(t, 'production', 'declared'))).toContain('Owner web-team (service owner, inferred)');
    expect(text(cell(t, 'staging', 'declared'))).toContain('Owner storefront-qa (declared)');
  });

  it('StageCellsJoinByEnvironment', () => {
    const t = render({ placement: placement([group('production'), group('staging')]), stagesState: 'served', stagesCompleteness: 'complete',
      stageRows: [stage('production'), stage('prod', { state: 'failed' })] });
    const c = cell(t, 'production', 'stage');
    expect(c?.querySelectorAll('[data-testid="env-state-mark"]')).toHaveLength(1);
    expect(c?.querySelector('[data-testid="env-state-mark"]')?.getAttribute('data-state')).toBe('verified');
    expect(c?.querySelector('a')?.getAttribute('href')).toContain('stage=storefront-web');
    expect(text(cell(t, 'staging', 'stage'))).toContain('No Stage you can see');
  });

  it('PartialStageListingNeverSaysNoStage', () => {
    const t = render({ placement: placement([group('staging')]), stagesState: 'served', stagesCompleteness: 'partial', stageRows: [] });
    expect(text(cell(t, 'staging', 'stage'))).toContain('Stages unknown: partial listing');
    expect(text(t)).not.toContain('No Stage you can see');
  });

  it('NoStageCellsWithoutDelivery', () => {
    const t = render({ placement: placement([group('staging')]), stagesState: 'none', stageRows: [] });
    expect(cell(t, 'staging', 'stage')).toBeNull();
    expect(t.querySelector('[data-cell="stage"]')).toBeNull();
    expect(text(t)).not.toContain('No Stage you can see');
  });

  it('DifferencesTableAndError', () => {
    const items = [group('production'), group('pr-482')];
    const t = render({ placement: placement(items, { differences: [{ configuration: 'web', path: 'replicas', values: { production: 3, 'pr-482': 1 } },
      { configuration: 'web', path: 'banner', values: { 'pr-482': 'preview' } }], differencesTotal: 3 }) });
    const d = t.querySelector('[data-testid="environment-differences"]')!;
    expect(text(d)).toContain('web replicas');
    const rows = [...d.querySelectorAll('[data-testid="difference-row"]')].map(text);
    expect(rows).toContain('production 3');
    expect(rows).toContain('production not set');
    expect(rows).toContain('pr-482 "preview"');
    expect(text(d)).toContain('1 more');
    cleanup?.();
    const e = render({ placement: placement(items, { differencesError: 'environments/pr-482.yaml cannot be read' }) });
    expect(text(e.querySelector('[data-testid="environment-differences"]'))).toContain('environments/pr-482.yaml cannot be read');
    cleanup?.();
    const one = render({ placement: placement([group('production'), group('canary', { declaration: undefined })]) });
    expect(one.querySelector('[data-testid="environment-differences"]')).toBeNull();
  });

  it('DifferencesBoundedAt200Environments', () => {
    const names = Array.from({ length: 200 }, (_, i) => `env-${i}`);
    const t = render({ placement: placement(names.map((n) => group(n)), {
      differences: [{ configuration: 'web', path: 'replicas', values: Object.fromEntries(names.map((n, i) => [n, i])) }], differencesTotal: 1 }) });
    const d = t.querySelector('[data-testid="environment-differences"]')!;
    expect(d.querySelectorAll('[data-testid="difference-row"]')).toHaveLength(200);
    expect(d.querySelectorAll('th, [role="columnheader"]')).toHaveLength(0);
    for (const r of d.querySelectorAll('[data-testid="difference-row"]')) expect(r.children).toHaveLength(2);
  });

  it('HistoryGapsStated', () => {
    const t = render({ placement: placement([group('production')]), history: history([], { gaps: 2 }) });
    expect(text(t.querySelector('[data-testid="environment-history"]'))).toContain('2 revisions could not be read; changes across them are unknown');
  });

  it('HistoryPartialAndTimeNotRecorded', () => {
    const t = render({ placement: placement([group('production')]), history: history([
      { change: 'removed', environment: 'pr-482', revision: rev('1.2.0'), previous: rev('1.1.0') },
      { change: 'changed', environment: 'staging', fields: ['lifetime', 'owner'], revision: rev('1.2.0') },
      { change: 'added', environment: 'pr-482', revision: rev('1.1.0') },
      { change: 'present', environment: 'production', revision: rev('1.0.0') },
    ], { completeness: 'partial', completenessReason: 'you may not see every revision of this service' }) });
    const h = t.querySelector('[data-testid="environment-history"]')!;
    expect(text(h)).toContain('Changes between versions (version order)');
    const items = [...h.querySelectorAll('[data-testid="environment-event"]')];
    expect(items).toHaveLength(4);
    for (const i of items) expect(text(i)).toContain('time not recorded');
    expect(text(items[3])).toContain('declared since at least 1.0.0');
    expect(text(items[1])).toContain('staging lifetime and owner changed in 1.2.0');
    expect(text(h)).toContain('you may not see every revision of this service');
  });

  it('TargetsIncompleteNeverBareZero', () => {
    const t = render({ placement: placement([group('production', { targetsComplete: false, targetsReason: 'you may not see every target of this service' })]) });
    const c = text(cell(t, 'production', 'observed'));
    expect(c).toContain('Targets you can see: 0 (incomplete)');
    expect(c).toContain('you may not see every target of this service');
  });

  it('ProvisioningUntrackedNeverZero', () => {
    const t = render({ placement: placement([group('canary', { declaration: undefined, provisioningTracked: false, provisioningReason: 'the current revision does not declare canary, so no provider is asked about it there' }),
      group('production', { provisioning: tally({ unknown: 2 }) }), group('staging', { provisioning: tally({ ready: 1, failed: 1 }) })]) });
    const c = cell(t, 'canary', 'provisioning');
    expect(text(c)).toContain('Provisioning not tracked: the current revision does not declare canary');
    expect(c?.querySelectorAll('.tag')).toHaveLength(0);
    expect(text(cell(t, 'production', 'provisioning'))).toContain('Unknown until a provider reports');
    expect([...cell(t, 'staging', 'provisioning')!.querySelectorAll('.tag')].map(text)).toEqual(['Ready 1', 'Failed 1']);
  });

  it('StageOnlyEnvironmentListed', () => {
    const t = render({ placement: placement([group('staging')]), stagesState: 'served', stagesCompleteness: 'complete', stageRows: [stage('prod')] });
    const r = row(t, 'prod');
    expect(text(r)).toContain('Stage only: not declared by 1.1.0, no target you can see reports it');
    expect(r?.querySelector('[data-testid="env-state-mark"]')).not.toBeNull();
  });

  it('StageCellProvenanceDeclaredByStage', () => {
    const t = render({ placement: placement([group('production')]), stagesState: 'served', stagesCompleteness: 'complete', stageRows: [stage('production')] });
    const c = text(cell(t, 'production', 'stage'));
    expect(c).toContain('declared by a Stage (admin)');
    expect(c).toContain('observed');
  });

  it('DeclaredMarkIsGlyphNotEnvMark', () => {
    const t = render({ placement: placement([group('production')]), stagesState: 'served', stagesCompleteness: 'complete', stageRows: [stage('production')] });
    const r = row(t, 'production')!;
    expect(r.querySelector('[data-cell="name"] svg[data-kind="environment"]')).not.toBeNull();
    expect(r.querySelector('[data-cell="name"] .badge-ok')?.textContent).toContain('Declared');
    expect(r.querySelector('[data-cell="declared"] [data-testid="env-state-mark"]')).toBeNull();
    expect(r.querySelector('[data-cell="name"] [data-testid="env-state-mark"]')).toBeNull();
  });

  it('ObservedLinkCarriesServiceAndEnvironment', () => {
    const t = render({ placement: placement([group('production', { targets: 1 })]) });
    const href = cell(t, 'production', 'observed')?.querySelector('a')?.getAttribute('href') ?? '';
    expect(href).toContain('service=storefront-web');
    expect(href).toContain('environment=production');
  });

  it('RefsUseHref', () => {
    const t = render({ placement: placement([group('production', { targets: 1, revisions: [rev('1.1.0')], revisionsTotal: 1 })]),
      history: history([{ change: 'present', environment: 'production', revision: rev('1.0.0') }]) });
    expect(cell(t, 'production', 'observed')?.querySelector('a[href="#/fleet/revisions/storefront-web%401.1.0"]')).not.toBeNull();
    expect(t.querySelector('[data-testid="environment-event"] a')?.getAttribute('href')).toBe('#/fleet/revisions/storefront-web%401.0.0');
  });

  it('TruncatedTextHasTitle', () => {
    const long = 'preview-' + 'settlement-reconciliation-'.repeat(8);
    const t = render({ placement: placement([group(long), group('production')], { differences: [{ configuration: 'web', path: 'a.very.long.path', values: { [long]: 'x'.repeat(200) } }], differencesTotal: 1 }) });
    expect(row(t, long)?.querySelector('[data-cell="name"] bdi')?.getAttribute('title')).toBe(long);
    const d = t.querySelector('[data-testid="environment-differences"]')!;
    expect(d.querySelector('[data-testid="difference-row"] bdi')?.getAttribute('title')).toBe(long);
    expect(d.querySelector('[data-testid="difference-row"] code[title]')?.getAttribute('title')).toBe(JSON.stringify('x'.repeat(200)));
  });

  it('states the answer time and completeness in its footer', () => {
    const t = render({ placement: placement([group('production')]), completeness: 'partial' });
    expect(text(t.querySelector('[data-testid="environments-footer"]'))).toBe('As of 2026-11-12 10:00 UTC, partial');
  });
});
