<script>
  import { api } from '../lib/api.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import CopyableIdentifier from '../components/CopyableIdentifier.svelte';

  // Metrics (D12): Pacto's outcomes over time, read from the stats repository the host
  // was started with. Every state says what it knows and nothing more: no repository is
  // the explainer, a failed read is an error, a window without records is a sentence.
  // Never a number the page does not have.
  let { capabilities = null, window: win = '', servicesOffset = '', refreshTick = 0 } = $props();

  const GUIDE = 'https://pacto.run/latest/run-record-reference/';
  const REPO = 'ghcr.io/acme/pacto-stats';
  const CI_LINE = `export PACTO_STATS_REGISTRY=${REPO}`;
  const DASHBOARD_LINE = `pacto dashboard --stats-registry ${REPO}`;
  const STATS_LINE = `pacto stats --registry ${REPO}`;
  const WINDOWS = ['7d', '30d', '90d'];
  // An empty repository and a missing one say the same thing to the reader: nothing is
  // recorded yet.
  const NO_RECORDS = ['NO_RUN_RECORDS', 'RUN_RECORDS_REPOSITORY_NOT_FOUND'];

  const enabled = $derived(capabilities?.stats === true);
  let data = $state(null);
  let error = $state('');
  let seq = 0;

  async function load() {
    const mine = ++seq;
    try {
      const res = await api.stats({
        window: WINDOWS.includes(win) ? win : undefined,
        servicesOffset: Number.parseInt(servicesOffset, 10) || undefined,
      });
      if (mine !== seq) return;
      data = res;
      error = '';
    } catch (e) {
      if (mine !== seq) return;
      error = e?.message || 'Reading the stats repository failed.';
    }
  }
  function retry() { error = ''; data = null; load(); }

  $effect(() => {
    const _ = [win, servicesOffset, refreshTick];
    if (enabled) load();
  });

  const noRecords = $derived((data?.limitations || []).find((l) => NO_RECORDS.includes(l.code)) ?? null);
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
    <!-- ponytail: the tiles, trends and tables land here in the next change. -->
    <section data-testid="metrics-content" aria-label="Pacto outcomes"></section>
  {/if}
</div>

<style>
  .mx-explainer { display: flex; flex-direction: column; gap: var(--sp-2); max-width: 72ch; }
  .mx-explainer :is(h2, p) { margin: 0; }
  .mx-reason::first-letter { text-transform: uppercase; }
  .mx-lines { display: flex; flex-direction: column; gap: var(--sp-1); margin: var(--sp-2) 0 0; min-width: 0; }
  .mx-lines dd { margin: 0 0 var(--sp-2); min-width: 0; overflow-wrap: anywhere; }
</style>
