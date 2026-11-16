<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { metricsUrl, hashForHref, deliveryEnvironmentsUrl, deliveryHistoryUrl } from '../lib/router.ts';
  import { formatCount, countOf } from '../lib/format.ts';
  import { formatDateTime } from '../lib/dateFormat.ts';
  import { andList, placedByName } from '../lib/delivery.ts';
  import { durationText } from '../lib/chart.ts';
  import { isDaily, windowDays, envTitle, hiddenLine, ratioText, noCI, protectedText, NO_CI, RATES, METRIC_LABEL } from '../lib/metrics.ts';
  import { tiles, trend, caption, windowTotal, coverageTrend, TRENDS, COVERAGE_SERIES } from '../lib/metricsLegacy.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';
  import Select from '../components/Select.svelte';
  import Pager from '../components/Pager.svelte';
  import LimitationsList from '../components/LimitationsList.svelte';
  import StatsStrip from '../components/StatsStrip.svelte';
  import HorizontalBars from '../components/viz/HorizontalBars.svelte';

  // Metrics (D12): Pacto's outcomes over time, read from the stats repository the host
  // was started with. Every state says what it knows and nothing more: no repository is
  // the explainer, a failed read is an error, a window without records is a sentence.
  // Never a number the page does not have; every number leads to the records behind it.
  let {
    capabilities = null, window: win = '', metric = '', environment = '', offset = '', limit = '',
    servicesOffset = '', servicesLimit = '', refreshTick = 0,
  } = $props();

  const GUIDE = 'https://pacto.run/latest/measure-what-pacto-does/';
  const REPO = 'ghcr.io/acme/pacto-stats';
  const CI_LINE = `export PACTO_STATS_REGISTRY=${REPO}`;
  const DASHBOARD_LINE = `pacto dashboard --stats-registry ${REPO}`;
  const STATS_LINE = `pacto stats --registry ${REPO}`;
  const WINDOWS = [{ value: '7d', label: 'Last 7 days' }, { value: '30d', label: 'Last 30 days' }, { value: '90d', label: 'Last 90 days' }];
  // An empty repository and a missing one say the same thing to the reader: nothing is
  // recorded yet.
  const NO_RECORDS = ['NO_RUN_RECORDS', 'RUN_RECORDS_REPOSITORY_NOT_FOUND'];
  const COVERAGE_RESTRICTED = 'Coverage history counts every service, so it is shown to people who can read every service.';
  // The coverage section says this one itself, so the limitations list leaves it out.
  const COVERAGE_HIDDEN = 'COVERAGE_OUTSIDE_YOUR_VIEW';

  const enabled = $derived(capabilities?.stats === true);
  const w = $derived(WINDOWS.some((o) => o.value === win) ? win : '');
  const int = (v) => Number.parseInt(v, 10) || undefined;
  const message = (e, fallback) => e?.message || fallback;

  // Both reads keep what is on screen while the next one loads (stale while revalidate): a
  // poll or a page turn never collapses the page, and a failed poll keeps the numbers and
  // says the refresh failed instead of blanking them.
  const stats = createProductLoader(() => api.stats({ window: w || undefined, servicesOffset: int(servicesOffset), servicesLimit: int(servicesLimit) }));
  const statsQuestion = $derived(`${w}@@${int(servicesOffset)}@@${int(servicesLimit)}`);
  $effect(() => { if (enabled) stats.sync(`${statsQuestion}@@${refreshTick}`, statsQuestion, w); });
  const statsAnswer = $derived(stats.answer(statsQuestion, w));
  const data = $derived(statsAnswer.data);
  const error = $derived(stats.error && !data ? message(stats.error, 'Reading the stats repository failed.') : '');
  const refreshError = $derived(stats.error && data ? message(stats.error, 'Reading the stats repository failed.') : '');
  function retry() { stats.refresh(); }

  // The records behind one number (?metric=), on the same URL as the number.
  const records = createProductLoader(() => api.statsRecords({ window: w || undefined, metric, environment: environment || undefined, offset: int(offset), limit: int(limit) }));
  const recQuestion = $derived(`${w}@@${metric}@@${environment}@@${int(offset)}@@${int(limit)}`);
  const recFamily = $derived(`${w}@@${metric}@@${environment}`);
  $effect(() => { if (enabled && metric) records.sync(`${recQuestion}@@${refreshTick}`, recQuestion, recFamily); });
  const rec = $derived(records.answer(recQuestion, recFamily).data);
  const recError = $derived(records.error && !rec ? message(records.error, 'Reading the records failed.') : '');
  onDestroy(() => { stats.destroy(); records.destroy(); });

  // The explainer only for a window that is truly empty: when records were skipped, cut
  // off or are outside the reader's view, the page says that instead of "recording is off".
  const noRecords = $derived(data?.completeness === 'empty' ? (data.limitations || []).find((l) => NO_RECORDS.includes(l.code)) ?? null : null);
  const shownLimits = $derived((data?.limitations || []).filter((l) => l.code !== COVERAGE_HIDDEN));
  const coverageHidden = $derived((data?.limitations || []).some((l) => l.code === COVERAGE_HIDDEN));
  const tileList = $derived(data ? tiles(data, w) : []);
  const daily = $derived(data ? isDaily(data) : false);
  const days = $derived(data ? windowDays(data) : 0);
  const charts = $derived(data ? TRENDS.map((t) => {
    const rows = trend(data, t.metric);
    return { ...t, rows, caption: caption(t.title, rows, daily, days, windowTotal(data, t.metric)) };
  }) : []);
  const envs = $derived(data?.environments ?? []);
  const byName = $derived(placedByName(envs));
  const hidden = $derived(data ? hiddenLine(data) : '');
  // Coverage history is fleet-wide: a reader who cannot see every service sees only now.
  const coverageCharts = $derived(data ? COVERAGE_SERIES.map((c) => ({ ...c, rows: coverageTrend(data, c.key) })) : []);
  const now = $derived(data?.coverageNow ?? null);

  const go = (q) => { location.hash = metricsUrl(q); };
  const keep = $derived({ window: w, metric, environment, offset, limit, servicesOffset, servicesLimit });
  const servicesHref = (o, l) => metricsUrl({ ...keep, servicesOffset: o ? String(o) : '', servicesLimit: l && l !== 25 ? String(l) : '' });
  const recordsHref = (o, l) => metricsUrl({ ...keep, offset: o ? String(o) : '', limit: l && l !== 25 ? String(l) : '' });
  const countLink = (m, n, env) => (n > 0 ? metricsUrl({ window: w, metric: m, environment: env }) : '');

  function kindText(r) {
    if (r.kind === 'operation') return [r.type || 'operation', r.outcome].filter(Boolean).join(', ');
    if (r.gate?.verdict) return `${r.kind}, gate ${r.gate.verdict}`;
    return [r.kind, r.classification].filter(Boolean).join(', ');
  }
</script>

<div class="product-page">
  {#snippet recordingLines()}
    <dl class="mx-lines">
      <dt class="t-meta">In CI, before pacto diff, impact and publish</dt>
      <dd><CopyableIdentifier value={CI_LINE} label="CI line" /></dd>
      <dt class="t-meta">Then start the dashboard</dt>
      <dd><CopyableIdentifier value={DASHBOARD_LINE} label="dashboard command" /></dd>
      <dt class="t-meta">The same numbers in a terminal</dt>
      <dd><CopyableIdentifier value={STATS_LINE} label="stats command" /></dd>
    </dl>
  {/snippet}

  {#snippet unknownText(text = 'unknown')}
    <span class="mx-unk"><span class="mx-hatch hatch" aria-hidden="true"></span>{text}</span>
  {/snippet}

  {#snippet count(text, href, n)}
    {#if href}<a {href} data-lists={n}>{text}</a>{:else if text === 'unknown'}{@render unknownText()}{:else}{text}{/if}
  {/snippet}

  <PageHeader title="Metrics" subtitle="What has Pacto done for us lately?" />

  {#if capabilities === null}
    <EmptyState loading={true} />
  {:else if !enabled}
    <section class="mx-explainer" data-testid="metrics-off" aria-labelledby="mx-off-title">
      <h2 id="mx-off-title" class="t-section-title">Metrics are off</h2>
      <p class="t-body">Pacto records run statistics in your own registry.</p>
      <p class="t-body">Start the dashboard with <code>--stats-registry &lt;repo&gt;</code>.</p>
      {@render recordingLines()}
      <p class="t-body"><a href={GUIDE} target="_blank" rel="noopener noreferrer">How recording works</a></p>
    </section>
  {:else if error}
    <div role="alert" data-testid="metrics-error">
      <EmptyState error={true} title="Can’t read the stats repository" message={error} onRetry={retry} />
    </div>
  {:else if !data}
    <EmptyState loading={true} />
  {:else if noRecords}
    <section class="mx-explainer" data-testid="metrics-none" aria-labelledby="mx-none-title">
      <h2 id="mx-none-title" class="t-section-title">No run records in this window</h2>
      <p class="t-body mx-reason">{noRecords.message}</p>
      {@render recordingLines()}
      <p class="t-body"><a href={GUIDE} target="_blank" rel="noopener noreferrer">How recording works</a></p>
    </section>
  {:else}
    <section class="mx-content" data-testid="metrics-content" aria-label="Pacto outcomes">
      <div class="mx-head">
        <Select label="Window" value={w || '30d'} options={WINDOWS} onchange={(e) => go({ window: e.currentTarget.value, metric, environment })} />
        <StatsStrip st={data} />
      </div>
      {#if refreshError}<p role="alert" class="t-body-2" data-testid="metrics-refresh-error">Refreshing failed, so these numbers are from {formatDateTime(data.asOf)}: {refreshError}</p>{/if}
      {#if hidden}<p class="t-body-2 mx-hidden" data-testid="metrics-hidden">{hidden}</p>{/if}
      {#if shownLimits.length}<LimitationsList items={shownLimits} />{/if}

      {#if metric}
        <section class="mx-records" data-testid="metrics-records" aria-labelledby="mx-rec-title">
          <div class="mx-rec-head">
            <h2 id="mx-rec-title" class="t-section-title">Records behind {METRIC_LABEL[metric] ?? metric}{#if environment}{' '}in <bdi title={environment}>{environment}</bdi>{/if}</h2>
            <a href={metricsUrl({ window: w, servicesOffset, servicesLimit })}>Close</a>
          </div>
          {#if recError}
            <p role="alert" class="t-body-2">{recError}</p>
          {:else if !rec}
            <EmptyState loading={true} />
          {:else}
            <StatsStrip st={rec} />
            {#if (rec.items ?? []).length === 0 && rec.total === 0}
              <p class="t-body-2">No record you can see is behind this number in this window.</p>
            {:else}
              <div class="data-scroll cq-cards">
                <table class="data-table mx-table">
                  <caption class="visually-hidden">The run records behind {METRIC_LABEL[metric] ?? metric}, newest first</caption>
                  <colgroup><col style="width: max(calc(12ch + 1.5rem), 16%)" /><col style="width: max(calc(10ch + 1.5rem), 14%)" /><col style="width: 22%" /><col style="width: 26%" /><col style="width: 22%" /></colgroup>
                  <thead><tr><th scope="col">Recorded</th><th scope="col">Kind</th><th scope="col">Service</th><th scope="col">Change</th><th scope="col">Stage</th></tr></thead>
                  <tbody>
                    {#each rec.items ?? [] as row (row.record.id)}
                      {@const r = row.record}
                      <tr data-testid="metrics-record">
                        <td data-label="Recorded"><time datetime={r.recordedAt}>{formatDateTime(r.recordedAt)}</time></td>
                        <td data-label="Kind">{kindText(r)}</td>
                        <td data-label="Service">
                          <div class="cell-stack">
                            {#if row.service}<a class="cell-clip" href={hashForHref(row.service.href)} title={row.service.label}><bdi>{row.service.label}</bdi></a>
                            {:else}<bdi class="cell-clip" title={r.service}>{r.service}</bdi>{/if}
                            {#if row.note}<span class="t-meta">{row.note}</span>{/if}
                          </div>
                        </td>
                        <td data-label="Change">
                          <div class="cell-stack">
                            {#if r.oldRevision || r.newRevision}
                              <span class="mx-revs">
                                {#each [[row.oldRevision, r.oldRevision], [row.newRevision, r.newRevision]] as [ln, digest], i (i)}
                                  {#if i === 1}<span aria-hidden="true">→</span><span class="visually-hidden">to</span>{/if}
                                  {#if !digest}<span class="t-meta">none</span>
                                  {:else if ln}<a class="cell-clip" href={hashForHref(ln.href)} title={digest}>{digest}</a>
                                  {:else}<span class="cell-clip" title={digest}>{digest}</span>{/if}
                                {/each}
                              </span>
                            {/if}
                            {#if row.change}<a href={hashForHref(row.change.href)}>Change analysis</a>{/if}
                          </div>
                        </td>
                        <td data-label="Stage">
                          {#if row.stage}
                            <div class="cell-stack">
                              <a class="cell-clip" href={deliveryHistoryUrl({ namespace: row.stage.namespace, stage: row.stage.name })} title={`${row.stage.namespace}/${row.stage.name}`}><bdi>{row.stage.name}</bdi></a>
                              {#if row.stage.environment}<a class="cell-clip t-meta" href={deliveryEnvironmentsUrl({ environment: row.stage.environment })} title={row.stage.environment}><bdi>{row.stage.environment}</bdi></a>{/if}
                            </div>
                          {/if}
                        </td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
              <Pager page={{ total: rec.total, offset: int(offset) ?? 0, limit: int(limit) ?? 25 }} href={recordsHref} count={(rec.items ?? []).length}
                label="Pages of records" noun="records" testid="metrics-records-pager" />
            {/if}
          {/if}
        </section>
      {/if}

      <div class="mx-tiles" data-fig data-testid="metrics-tiles">
        {#each tileList as t (t.key)}
          <div class="mx-tile">
            <span class="t-label mx-tile-label" title={t.label}>{t.label}</span>
            {#if t.sub}<bdi class="t-meta mx-tile-sub">{t.sub}</bdi>{/if}
            <span class="t-metric mx-tile-value">
              {#if t.unknown && t.href}<a href={t.href} data-lists={t.lists}>{@render unknownText(t.value)}</a>{:else if t.unknown}{@render unknownText(t.value)}{:else if t.href}<a href={t.href} data-lists={t.lists}>{t.value}</a>{:else}{t.value}{/if}
            </span>
            {#if t.detail}<span class="t-meta">{t.detail}</span>{/if}
          </div>
        {/each}
      </div>

      <section aria-labelledby="mx-trends-title">
        <h2 id="mx-trends-title" class="t-section-title">Over time</h2>
        <div class="mx-trends">
          {#each charts as c (c.metric)}
            <div class="mx-chart" data-fig data-testid="metrics-trend">
              <HorizontalBars title={`${c.title} per ${daily ? 'day' : 'week'}`} items={c.rows} />
              <p class="t-meta mx-caption">{c.caption}</p>
              <StatsStrip st={data} />
            </div>
          {/each}
        </div>
      </section>

      <section aria-labelledby="mx-env-title" data-testid="metrics-environments">
        <h2 id="mx-env-title" class="t-section-title">{envTitle(data)}</h2>
        {#if envs.length === 0}
          <p class="t-body-2">No delivery operation is recorded in this window.</p>
        {:else}
          <div class="data-scroll cq-cards" data-fig>
            <table class="data-table mx-table">
              <caption class="visually-hidden">Delivery numbers per environment</caption>
              <colgroup><col style="width: 20%" />{#each Array(7) as _, i (i)}<col style="width: max(calc(9ch + 1.5rem), {i < 5 ? 12 : 10}%)" />{/each}</colgroup>
              <thead><tr><th scope="col">Environment</th><th scope="col">Operations</th>{#each RATES as r (r.metric)}<th scope="col">{r.label}</th>{/each}<th scope="col">Lead time p50</th><th scope="col">p90</th></tr></thead>
              <tbody>
                {#each envs as e (e.environment)}
                  <tr data-testid="metrics-env-row">
                    <th scope="row">
                      <div class="cell-stack">
                        <a class="cell-clip" href={deliveryEnvironmentsUrl({ environment: e.environment })} title={e.environment}><bdi>{e.environment}</bdi></a>
                        {#if (e.upstream ?? []).length}<span class="t-meta cell-clip" title={`fed by ${andList(e.upstream ?? [])}`}>fed by <bdi>{andList(e.upstream ?? [], 2)}</bdi></span>{/if}
                        {#if e.cycle}<span class="t-meta">on a cycle of upstream links, placed by name</span>{/if}
                        {#if e.unordered}<span class="t-meta">ordered both ways, placed by name</span>{/if}
                      </div>
                    </th>
                    <td data-label="Operations">{formatCount(e.operations)}</td>
                    {#each RATES as r (r.metric)}
                      <td data-label={r.label}>{@render count(ratioText(e[r.field]), countLink(r.metric, e[r.field].count, e.environment), e[r.field].count)}</td>
                    {/each}
                    <td data-label="Lead time p50">{@render count(durationText(e.leadTime.p50Seconds), '', 0)}</td>
                    <td data-label="Lead time p90">{@render count(durationText(e.leadTime.p90Seconds), '', 0)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
          {#if byName}<p class="t-meta">{byName}</p>{/if}
        {/if}
      </section>

      <section aria-labelledby="mx-cov-title" data-testid="metrics-coverage">
        <h2 id="mx-cov-title" class="t-section-title">Coverage</h2>
        <div class="mx-trends">
          {#if now}
            <div class="mx-chart" data-testid="metrics-coverage-now">
              {#if now.knowledge}
                {@const k = now.knowledge}
                <HorizontalBars title="Now" items={[
                  { label: 'Services', value: now.services },
                  { label: 'Described', value: k.described, tone: 'ok' },
                  { label: 'With gaps', value: k.withGaps, tone: 'warn' },
                  { label: 'Deployed', value: k.services - k.unknown - k.notDeployed, tone: 'info' },
                  { label: 'Unknown', value: k.unknown, tone: 'neutral', hatch: true },
                  { label: 'Not deployed', value: k.notDeployed, tone: 'neutral' },
                ]} />
              {:else}
                <h3 class="t-subsection-title">Now</h3>
                <p class="t-body-2">Services: {formatCount(now.services)}; what is known about them is {@render unknownText()}</p>
              {/if}
              <p class="t-meta">Links: {formatCount(now.links.exact)} exact, {formatCount(now.links.inferred)} inferred, {formatCount(now.links.ambiguous)} ambiguous and {formatCount(now.links.unresolved)} unresolved; {countOf(now.uncontracted, 'caller')} with no contract.</p>
              <p class="t-meta">From the snapshot of {formatDateTime(now.snapshotAsOf)}, {now.completeness}.</p>
            </div>
          {/if}
          {#if coverageHidden}
            <p class="t-body-2" data-testid="metrics-coverage-restricted">{COVERAGE_RESTRICTED}</p>
          {:else}
            <div class="mx-chart" data-fig data-testid="metrics-coverage-trend">
              {#each coverageCharts as c (c.key)}
                <HorizontalBars title={`${c.title} per ${daily ? 'day' : 'week'}`} items={c.rows} />
              {/each}
              <p class="t-meta mx-caption">The newest coverage snapshot of each {daily ? 'day' : 'week'}; a partial snapshot is hatched, and so is every count of services Pacto cannot judge.</p>
              <StatsStrip st={data} />
            </div>
          {/if}
        </div>
      </section>

      <section aria-labelledby="mx-svc-title" data-testid="metrics-services">
        <h2 id="mx-svc-title" class="t-section-title">Services</h2>
        {#if (data.services ?? []).length === 0}
          <p class="t-body-2">No service has a counted change in this window.</p>
        {:else}
          <div class="data-scroll">
            <table class="data-table mx-table">
              <caption class="visually-hidden">Numbers per service</caption>
              <colgroup><col style="width: 30%" />{#each Array(5) as _, i (i)}<col style="width: 14%" />{/each}</colgroup>
              <thead><tr><th scope="col">Service</th><th scope="col">Breaking caught</th><th scope="col">Blocked</th><th scope="col">Clear</th><th scope="col">Unverified</th><th scope="col">Consumers protected</th></tr></thead>
              <tbody>
                {#each data.services ?? [] as s, i (s.label + '#' + i)}
                  {@const m = s.metrics}
                  <tr data-testid="metrics-service-row">
                    <th scope="row">
                      <div class="cell-stack">
                        {#if s.href}<a class="cell-clip" href={hashForHref(s.href)} title={s.label}><bdi>{s.label}</bdi></a>
                        {:else}<bdi class="cell-clip" title={s.label}>{s.label}</bdi>{/if}
                        {#if s.note}<span class="t-meta">{s.note}</span>{/if}
                      </div>
                    </th>
                    {#if noCI(m)}
                      {#each ['Breaking caught', 'Blocked', 'Clear', 'Unverified', 'Consumers protected'] as l (l)}<td data-label={l}>{@render unknownText(NO_CI)}</td>{/each}
                    {:else}
                      {@const p = protectedText(m)}
                      <td data-label="Breaking caught">{formatCount(m.breakingCaught.distinct)}</td>
                      <td data-label="Blocked">{formatCount(m.gates.blocked)}</td>
                      <td data-label="Clear">{formatCount(m.gates.clear)}</td>
                      <td data-label="Unverified">{formatCount(m.gates.unverified)}</td>
                      <td data-label="Consumers protected">{#if p.qualified}{@render unknownText(p.text)}{:else}{p.text}{/if}</td>
                    {/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
        <Pager page={{ total: data.servicesTotal, offset: int(servicesOffset) ?? 0, limit: int(servicesLimit) ?? 25 }} href={servicesHref}
          count={(data.services ?? []).length} label="Pages of services" noun="services" testid="metrics-services-pager" />
      </section>
    </section>
  {/if}
</div>

<style>
  .mx-explainer { display: flex; flex-direction: column; gap: var(--sp-2); max-width: 72ch; }
  .mx-explainer :is(h2, p) { margin: 0; }
  .mx-reason::first-letter { text-transform: uppercase; }
  .mx-lines { display: flex; flex-direction: column; gap: var(--sp-1); margin: var(--sp-2) 0 0; min-width: 0; }
  .mx-lines dd { margin: 0 0 var(--sp-2); min-width: 0; overflow-wrap: anywhere; }
  .mx-content { display: flex; flex-direction: column; gap: var(--sp-5); min-width: 0; }
  .mx-content :is(h2, p) { margin: 0; }
  .mx-content > section { display: flex; flex-direction: column; gap: var(--sp-3); min-width: 0; }
  .mx-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2) var(--sp-4); }
  .mx-hidden { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-neutral); border-radius: var(--radius-sm); background: var(--c-neutral-bg); }
  .mx-rec-head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: var(--sp-2); min-width: 0; }
  .mx-rec-head h2 { min-width: 0; overflow-wrap: anywhere; }
  .mx-tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(24ch, 100%), 1fr)); gap: var(--sp-3); }
  .mx-tile { display: flex; flex-direction: column; gap: var(--sp-1); min-width: 0; padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .mx-tile-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  /* The environment tells the lead-time tiles apart, so it wraps and is never clipped. */
  .mx-tile-sub { min-width: 0; overflow-wrap: anywhere; }
  /* A rate ("12/1,200 (1%)") in the metric size is wider than a narrow tile: it wraps at its spaces. */
  .mx-tile-value { min-width: 0; overflow-wrap: anywhere; }
  .mx-tile-value a { display: inline-block; max-width: 100%; min-height: var(--touch-min); }
  .mx-trends { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(32ch, 100%), 1fr)); gap: var(--sp-4); }
  .mx-chart { display: flex; flex-direction: column; gap: var(--sp-2); min-width: 0; padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); }
  .mx-unk { font-style: italic; color: var(--c-text-2); }
  .mx-hatch { display: inline-block; inline-size: 0.75em; block-size: 0.75em; margin-inline-end: var(--sp-1); border-radius: 2px; vertical-align: middle; }
  .mx-revs { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-2); min-width: 0; max-width: 100%; }
  .mx-revs .cell-clip { max-width: 100%; }
</style>
