<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { metricsUrl, hashForHref, deliveryEnvironmentsUrl, deliveryHistoryUrl } from '../lib/router.ts';
  import { formatCount, countOf } from '../lib/format.ts';
  import { formatDateTime } from '../lib/dateFormat.ts';
  import { andList, placedByName } from '../lib/delivery.ts';
  import { durationText } from '../lib/chart.ts';
  import {
    isDaily, envTitle, hiddenLine, ratioText, noCI, protectedText, NO_CI, RATES, METRIC_LABEL,
    buckets, series, unknownReasons, headline, coverageSeries, tableRows, caption, noteLimitations,
  } from '../lib/metrics.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';
  import Select from '../components/Select.svelte';
  import Pager from '../components/Pager.svelte';
  import LimitationsList from '../components/LimitationsList.svelte';
  import StatsStrip from '../components/StatsStrip.svelte';
  import HorizontalBars from '../components/viz/HorizontalBars.svelte';
  import ColumnChart from '../components/viz/ColumnChart.svelte';
  import LineChart from '../components/viz/LineChart.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import ChartCard from '../components/ChartCard.svelte';
  import ChartTip from '../components/ChartTip.svelte';

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
  const limits = $derived(data ? noteLimitations(data) : { note: null, rest: [] });
  const daily = $derived(data ? isDaily(data) : false);
  const per = $derived(daily ? 'day' : 'week');
  const envs = $derived(data?.environments ?? []);
  const byName = $derived(placedByName(envs));
  const hidden = $derived(data ? hiddenLine(data) : '');
  const now = $derived(data?.coverageNow ?? null);

  // The charts (#597): every drawing, tooltip and table cell reads lib/metrics.ts.
  const bl = $derived(data ? buckets(data) : []);
  const why = $derived(data ? unknownReasons(data) : []);
  const head = $derived(data ? headline(data, { window: w }) : null);
  const breaking = $derived(data ? series(data, 'breaking') : { values: [], caps: [] });
  const runs = $derived(data ? series(data, 'runs') : { values: [], caps: [] });
  const protectedS = $derived(data ? series(data, 'protected') : { values: [], caps: [] });
  const GATES = [
    { key: 'clear', label: 'Clear', icon: '✓', color: 'var(--c-ok)', tone: 'ok' },
    { key: 'blocked', label: 'Blocked', icon: '✕', color: 'var(--c-err)', tone: 'err' },
    { key: 'unverified', label: 'Unverified', icon: '?', texture: 'dots' },
  ];
  const gateSeries = $derived(data ? GATES.map((g) => ({ key: g.key, label: g.label, ...(g.texture ? { texture: g.texture } : { color: g.color }), values: series(data, g.key).values })) : []);
  const coverage = $derived(data ? coverageSeries(data) : null);
  const opts = $derived({ window: w });

  const key = (g) => (g.texture ? { texture: g.texture } : { color: g.color });
  const stoppedTip = (i) => ({ title: bl[i].label, rows: [{ value: formatCount(breaking.values[i] ?? 0), label: 'Breaking changes caught', color: 'var(--chart-1)' }], foot: countOf(runs.values[i] ?? 0, 'run') });
  const protectedTip = (i) => ({ title: bl[i].label, rows: [{ value: protectedText(data.buckets[i].metrics).text, label: 'Consumers protected', color: 'var(--chart-1)' }], foot: protectedS.caps[i] ? 'A lower bound: some consumers are unknown' : '' });
  const gatesTip = (i) => {
    const vs = gateSeries.map((g) => g.values[i] ?? 0);
    return { title: bl[i].label, rows: GATES.map((g, k) => ({ value: formatCount(vs[k]), label: g.label, ...key(g) })), foot: `${countOf(vs.reduce((a, b) => a + b, 0), 'change')} gated` };
  };
  const leadTip = (i) => ({ title: bl[i].label, rows: [{ value: durationText(head.lead.spark[i]), label: `p50 ${head.lead.title.toLowerCase()}`, color: 'var(--chart-1)' }] });
  const coverageTip = (i) => ({
    title: bl[i].label,
    rows: [
      ...coverage.series.map((c) => ({ value: c.values[i] == null ? 'unknown' : formatCount(c.values[i]), label: c.label, color: c.color })),
      { value: coverage.unknown[i] == null ? 'unknown' : formatCount(coverage.unknown[i]), label: 'Pacto cannot judge', texture: 'hatch' },
    ],
    foot: [coverage.foot[i], coverage.series[0].partial[i] ? 'from a partial snapshot' : ''].filter(Boolean).join('; '),
  });

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

  {#snippet tileValue(text, unknown, href, n)}
    {#if href}<a {href} data-lists={n}>{#if unknown}{@render unknownText(text)}{:else}{text}{/if}</a>{:else if unknown}{@render unknownText(text)}{:else}{text}{/if}
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
      <div class="mx-prov-wrap" data-testid="metrics-prov">
        <div class="mx-prov">
          <Select label="Window" value={w || '30d'} options={WINDOWS} onchange={(e) => go({ window: e.currentTarget.value, metric, environment })} />
          <StatsStrip st={data} />
        </div>
        {#if limits.note}<p class="mx-note t-body-2" data-testid="metrics-note"><span class="mx-hatch hatch" aria-hidden="true"></span><span>{limits.note.message}</span></p>{/if}
        {#if limits.rest.length}
          <details class="disclosure mx-lims" data-testid="metrics-limitations">
            <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{limits.note ? `${countOf(limits.rest.length, 'more limitation')}` : countOf(limits.rest.length, 'limitation')}</summary>
            <LimitationsList items={limits.rest} />
          </details>
        {/if}
      </div>
      {#if refreshError}<p role="alert" class="t-body-2" data-testid="metrics-refresh-error">Refreshing failed, so these numbers are from {formatDateTime(data.asOf)}: {refreshError}</p>{/if}
      {#if hidden}<p class="t-body-2 mx-hidden" data-testid="metrics-hidden">{hidden}</p>{/if}

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

      {#if head}
        <section class="mx-tiles" data-fig data-testid="metrics-tiles" aria-label="Headline numbers">
          <div class="mx-tile">
            <span class="mx-tile-label">Breaking changes caught</span>
            <span class="mx-tile-value">{@render tileValue(head.breaking.value, head.breaking.unknown, head.breaking.href, head.breaking.lists)}</span>
            <span class="mx-tile-sub">{head.breaking.protectedQualified ? `Consumers protected: ${head.breaking.protected}` : `${head.breaking.protected} consumers protected`}</span>
            <div class="mx-tile-fig">
              <ColumnChart compact buckets={bl} unknown={why} series={[{ key: 'breaking', label: 'Breaking changes caught', values: breaking.values }]} tipOf={stoppedTip} label={`Breaking changes caught per ${per}`} />
            </div>
          </div>
          <div class="mx-tile">
            <span class="mx-tile-label">Release gates</span>
            <span class="mx-tile-value">{@render tileValue(head.gates.value, head.gates.unknown, head.gates.href, head.gates.lists)}{#if !head.gates.unknown}<small>blocked of {formatCount(head.gates.total)}</small>{/if}</span>
            {#if !head.gates.unknown && head.gates.total}
              <div class="mx-tile-fig wide">
                <DistributionBar label="Release gates by newest verdict" total={head.gates.total}
                  segments={GATES.map((g) => ({ label: g.label, icon: g.icon, value: head.gates[g.key], ...(g.texture ? { texture: g.texture } : { tone: g.tone }) }))} />
              </div>
            {/if}
          </div>
          <div class="mx-tile">
            <span class="mx-tile-label">{head.lead.title}</span>
            <span class="mx-tile-value">{@render tileValue(head.lead.value, head.lead.unknown, head.lead.href, undefined)}</span>
            <span class="mx-tile-sub">{head.lead.sub}</span>
            {#if head.lead.spark.length}
              <div class="mx-tile-fig">
                <LineChart compact buckets={bl} series={[{ key: 'lead', label: 'p50', color: 'var(--chart-1)', values: head.lead.spark }]} tipOf={leadTip} label={`${head.lead.title} per ${per}`} />
              </div>
            {/if}
          </div>
        </section>
      {/if}

      <div class="mx-grid2">
        <ChartCard title="What Pacto stopped" question={`Per ${per}, before the change shipped`} tables={tableRows(data, 'stopped', opts)} testid="metrics-stopped">
          {#snippet chart()}
            <div class="mx-multiples">
              <div>
                <p class="mx-mult-title">Breaking changes caught</p>
                <ColumnChart buckets={bl} unknown={why} series={[{ key: 'breaking', label: 'Breaking changes caught', values: breaking.values }]} tipOf={stoppedTip} label={`Breaking changes caught per ${per}`} />
              </div>
              <div>
                <p class="mx-mult-title">Consumers protected</p>
                <ColumnChart buckets={bl} unknown={why} caps={protectedS.caps} series={[{ key: 'protected', label: 'Consumers protected', values: protectedS.values }]} tipOf={protectedTip} label={`Consumers protected per ${per}`} />
              </div>
            </div>
          {/snippet}
          {#snippet foot()}
            <div class="mx-keys"><span class="mx-key"><span class="mx-hatch hatch" aria-hidden="true"></span><span>no records in that {per}, never 0</span></span></div>
            <p class="t-meta">{caption(data, 'stopped')}</p>
          {/snippet}
        </ChartCard>

        <ChartCard title={`Gate outcomes per ${per}`} question="Each change by its newest gate verdict" tables={tableRows(data, 'gates', opts)} testid="metrics-gates">
          {#snippet chart()}
            <div class="mx-keys">
              {#each GATES as g (g.key)}
                <span class="mx-key"><i class:dots={g.texture} style:background={g.color} aria-hidden="true"></i><span aria-hidden="true">{g.icon}</span><span>{g.label}</span>{#if head && !head.gates.unknown}<b>{formatCount(head.gates[g.key])}</b>{/if}</span>
              {/each}
            </div>
            <ColumnChart buckets={bl} unknown={why} series={gateSeries} tipOf={gatesTip} label={`Gate outcomes per ${per}`} />
          {/snippet}
          {#snippet foot()}
            <div class="mx-keys"><span class="mx-key"><span class="mx-hatch hatch" aria-hidden="true"></span><span>pale band: no records in that {per}</span></span></div>
            <p class="t-meta">{caption(data, 'gates')}</p>
          {/snippet}
        </ChartCard>
      </div>

      <!-- ponytail: today's environments table holds the delivery health and lead time numbers until #597 P10 draws them. -->
      <section class="card mx-card" aria-labelledby="mx-env-title" data-testid="metrics-environments">
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

      <div class="mx-grid2">
        <ChartCard title="Coverage over time" question={`Newest coverage snapshot of each ${per}`} tables={tableRows(data, 'coverage', opts)} testid="metrics-coverage">
          {#snippet chart()}
            {#if coverage}
              <div class="mx-keys">
                {#each coverage.series as c (c.key)}<span class="mx-key"><i class="line" style:background={c.color} aria-hidden="true"></i><span>{c.label}</span></span>{/each}
                <span class="mx-key"><span class="mx-hatch hatch" aria-hidden="true"></span><span>Pacto cannot judge, or no records</span></span>
              </div>
              <div data-testid="metrics-coverage-trend">
                <LineChart buckets={bl} series={coverage.series} tipOf={coverageTip} label={`Coverage per ${per}`} />
              </div>
              {#if now}<p class="t-meta">Now: links {formatCount(now.links.exact)} exact, {formatCount(now.links.inferred)} inferred, {formatCount(now.links.ambiguous)} ambiguous and {formatCount(now.links.unresolved)} unresolved; {countOf(now.uncontracted, 'caller')} with no contract, from the snapshot of {formatDateTime(now.snapshotAsOf)}, {now.completeness}.</p>{/if}
            {:else}
              {#if now}
                <div data-testid="metrics-coverage-now">
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
                  <p class="t-meta">From the snapshot of {formatDateTime(now.snapshotAsOf)}, {now.completeness}.</p>
                </div>
              {/if}
              <p class="t-body-2" data-testid="metrics-coverage-restricted">{COVERAGE_RESTRICTED}</p>
            {/if}
          {/snippet}
        </ChartCard>
      </div>

      <!-- A plain section, not a card: its URL pager needs the full column on a phone. -->
      <section aria-labelledby="mx-svc-title" data-testid="metrics-services">
        <h2 id="mx-svc-title" class="mx-card-title">By service</h2>
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
      <ChartTip />
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
  .mx-content > section:not(.mx-tiles) { display: flex; flex-direction: column; gap: var(--sp-3); min-width: 0; }
  .mx-prov-wrap { display: flex; flex-direction: column; gap: var(--sp-2); min-width: 0; }
  .mx-prov { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2) var(--sp-4); min-width: 0; }
  .mx-note { display: flex; align-items: baseline; gap: var(--sp-2); }
  .mx-lims summary { min-height: var(--touch-min); }
  .mx-lims :global(.lim-list) { margin-top: var(--sp-2); }
  .mx-hidden { padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-neutral); border-radius: var(--radius-sm); background: var(--c-neutral-bg); }
  .mx-rec-head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: var(--sp-2); min-width: 0; }
  .mx-rec-head h2 { min-width: 0; overflow-wrap: anywhere; }
  /* Headline tiles (#597): the number and its line on the left, the sparkline on the right. */
  .mx-tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(30ch, 100%), 1fr)); gap: var(--sp-4); }
  .mx-tile {
    display: grid; grid-template-columns: minmax(0, 1fr) minmax(8ch, 16ch); grid-template-rows: auto auto 1fr; column-gap: var(--sp-4);
    min-width: 0; padding: var(--sp-4); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface);
  }
  .mx-tile-label {
    grid-column: 1 / -1; min-width: 0; overflow-wrap: anywhere;
    font: 600 var(--text-base)/1.4 var(--font-display); font-variant-caps: all-small-caps; letter-spacing: 0.05em; color: var(--c-text-2);
  }
  /* A value wraps at its spaces in a narrow tile; its link is a full touch target. */
  .mx-tile-value { grid-column: 1; min-width: 0; margin-top: var(--sp-1); overflow-wrap: anywhere; font: 700 2.4rem/1.1 var(--font-display); color: var(--c-text); }
  .mx-tile-value small { margin-inline-start: var(--sp-1); font-size: var(--text-md); font-weight: 600; color: var(--c-text-2); }
  /* The number reads as the number (text colour); a quiet underline says it opens its records. */
  .mx-tile-value a {
    display: inline-block; max-width: 100%; min-height: var(--touch-min); color: inherit;
    text-decoration: underline; text-decoration-color: var(--c-border); text-decoration-thickness: 2px; text-underline-offset: 6px;
  }
  .mx-tile-value a:hover, .mx-tile-value a:focus-visible { text-decoration-color: currentColor; }
  .mx-tile-value :global(.mx-unk) { font-size: var(--text-lg); }
  .mx-tile-sub { grid-column: 1; align-self: end; min-width: 0; overflow-wrap: anywhere; font-size: var(--text-xs); color: var(--c-text-3); }
  .mx-tile-fig { grid-column: 2; grid-row: 2 / 4; align-self: end; min-width: 0; }
  .mx-tile-fig.wide { grid-column: 1 / -1; grid-row: auto; margin-top: var(--sp-3); }
  .mx-grid2 { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(46ch, 100%), 1fr)); gap: var(--sp-4); }
  .mx-multiples { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(24ch, 100%), 1fr)); gap: var(--sp-4) var(--sp-5); }
  .mx-mult-title { margin: 0 0 var(--sp-1); font-size: var(--text-sm); font-weight: 600; color: var(--c-text); }
  .mx-keys { display: flex; flex-wrap: wrap; gap: 2px var(--sp-3); font-size: var(--text-xs); color: var(--c-text-2); }
  .mx-key { display: inline-flex; align-items: center; gap: var(--sp-2); min-width: 0; }
  .mx-key i { flex: none; inline-size: 10px; block-size: 10px; border-radius: 2px; }
  .mx-key i.dots { box-shadow: inset 0 0 0 1px var(--c-neutral); }
  .mx-key i.line { inline-size: 14px; block-size: 2px; border-radius: 1px; }
  .mx-key b { color: var(--c-text); font-weight: 600; }
  .mx-card { container-type: inline-size; display: flex; flex-direction: column; gap: var(--sp-3); min-width: 0; padding: var(--sp-4); border-radius: var(--radius-sm); }
  .mx-card:hover { border-color: var(--c-border); }
  .mx-card-title { font: italic 600 var(--text-md)/1.35 var(--font-display); }
  .mx-unk { font-style: italic; color: var(--c-text-2); }
  .mx-hatch { display: inline-block; inline-size: 0.75em; block-size: 0.75em; margin-inline-end: var(--sp-1); border-radius: 2px; vertical-align: middle; }
  .mx-revs { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-2); min-width: 0; max-width: 100%; }
  .mx-revs .cell-clip { max-width: 100%; }
</style>
