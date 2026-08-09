<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.ts';
  import { snapshotKnowledge, decideViewState, allClearAllowed } from '../lib/knowledgeState.ts';
  import { knowledgeLabel, knowledgeTone, attentionCategoryLabel, ATTENTION_CATEGORIES } from '../lib/entityLabels.ts';
  import { fleetAttentionUrl, fleetSourcesUrl } from '../lib/router.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import OperationalSummary from '../components/OperationalSummary.svelte';
  import SourceHealth from '../components/SourceHealth.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StatusBadge from '../components/StatusBadge.svelte';

  // The operational landing page (requirement G). It consumes /api/fleet/overview
  // as the single contract -- it never reconstructs the summary from the raw
  // snapshot -- and answers "what needs attention / is my knowledge incomplete /
  // where do I go next" without requiring graph knowledge.
  let { refreshTick = 0 } = $props();

  let overview = $state(null);
  let loading = $state(true);
  let error = $state(null);
  let lastTick = refreshTick;

  async function load() {
    loading = true;
    error = null;
    try {
      overview = await api.fleetOverview();
    } catch (e) {
      error = e;
    } finally {
      loading = false;
    }
  }

  onMount(load);
  // Re-load when the app-wide refresh tick advances (auto-reload / manual refresh).
  $effect(() => {
    if (refreshTick !== lastTick) {
      lastTick = refreshTick;
      load();
    }
  });

  const knowledge = $derived(snapshotKnowledge(overview?.meta));
  const attentionTotal = $derived(overview?.attention?.total ?? 0);
  // Page-level state: loading/error gate the whole view; once loaded, the overview
  // always has a summary to render (itemCount 1).
  const pageState = $derived(decideViewState({ loading, error, itemCount: overview ? 1 : 0, knowledge }));

  // A1: distinguish a genuinely empty fleet from a healthy populated one, using the
  // authoritative product summary counts (never the raw snapshot). A fleet with zero
  // services is empty, not "all clear"; "every deployment is compliant" is claimed
  // only when there actually ARE deployments.
  const totalServices = $derived(overview?.summary?.services ?? 0);
  const s = $derived(overview?.summary ?? {});
  const totalTargets = $derived(
    (s.exactTargetLinks || 0) + (s.inferredTargetLinks || 0) +
    (s.ambiguousTargetLinks || 0) + (s.unresolvedTargetLinks || 0),
  );
  const isEmptyFleet = $derived(!!overview && totalServices === 0);
  // All-clear needs complete knowledge, zero attention AND a populated fleet.
  const canAllClear = $derived(!!overview && totalServices > 0 && allClearAllowed(knowledge, attentionTotal));
</script>

<div class="overview">
  <Breadcrumbs trail={[{ label: 'Overview' }]} />
  <h1>Operational overview</h1>

  {#if pageState.kind !== 'ready'}
    <ProductEmptyState state={pageState} noun="operational data" onRetry={load} />
  {:else}
    {#if knowledge.incomplete}
      <div class="knowledge-banner tone-{knowledgeTone(knowledge.level)}" role="status">
        <strong>{knowledgeLabel(knowledge.level)}.</strong>
        <span>
          {#if isEmptyFleet}
            Nothing is being tracked yet, and some sources are degraded — we can neither confirm there is nothing to track nor call it healthy.
          {:else}
            Some sources are degraded, so the counts below may be incomplete — this is not a clean bill of health.
          {/if}
        </span>
      </div>
    {:else if isEmptyFleet}
      <div class="empty-fleet" role="status">
        <strong>No services tracked yet.</strong>
        <span>Nothing has reported a contract or a running target yet. That is not a health assessment.</span>
      </div>
    {:else if canAllClear}
      <div class="all-clear" role="status">
        <strong>All clear.</strong>
        <span>
          {#if totalTargets > 0}
            Every operational target is compliant and every data source is healthy.
          {:else}
            No open attention items, and every source is healthy.
          {/if}
        </span>
      </div>
    {/if}

    <OperationalSummary summary={overview.summary} entryPoints={overview.entryPoints} {attentionTotal} />

    <section class="ov-section">
      <div class="ov-head">
        <h2>Data sources</h2>
        <a class="ov-viewall" href={fleetSourcesUrl()}>View all data sources</a>
      </div>
      <SourceHealth sources={overview.meta?.sources || []} truncated={overview.meta?.sourcesTruncated} />
    </section>

    <section class="ov-section">
      <div class="ov-head">
        <h2>Needs attention</h2>
        <a class="ov-viewall" href={fleetAttentionUrl()}>View all ({attentionTotal})</a>
      </div>
      <!-- Triage dimensions, not destinations. Readiness lives here rather than in the
           primary nav: it is declared contract preparedness, one reason a thing needs
           attention, and it shares the product's single definition of it. These are
           filters, so they are deliberately count-free -- a chip claiming "0" would be a
           health assessment the overview has not made. -->
      <nav class="ov-cats" aria-label="Filter attention by category">
        {#each ATTENTION_CATEGORIES as c}
          <a class="ov-cat" href={fleetAttentionUrl({ category: c })}>{attentionCategoryLabel(c)}</a>
        {/each}
      </nav>
      {#if overview.attention.items.length === 0}
        <ProductEmptyState state={decideViewState({ loading: false, itemCount: 0, knowledge })} noun="attention items" />
      {:else}
        <ul class="attn-list">
          {#each overview.attention.items as it}
            <li class="attn-item">
              <StatusBadge status={it.severity} />
              <EntityLink ref={it.entity} showStatus={false} />
              <span class="attn-reason">{it.summary || it.reason || it.label}</span>
            </li>
          {/each}
        </ul>
        {#if overview.attention.truncated}
          <p class="ov-more">Showing {overview.attention.count} of {attentionTotal}. <a href={fleetAttentionUrl()}>See all</a></p>
        {/if}
      {/if}
    </section>

    <section class="ov-section">
      <h2>Recent evidence</h2>
      {#if overview.recentEvidence.items.length}
        <ul class="evi-list">
          {#each overview.recentEvidence.items as ev}
            <li class="evi-item">
              <EntityLink ref={ev.target} showStatus={false} />
              {#if ev.at}<span class="evi-at">{formatDate(ev.at)}</span>{/if}
            </li>
          {/each}
        </ul>
      {:else}
        <p class="ov-none">No evidence arrived recently.</p>
      {/if}
    </section>
  {/if}
</div>

<style>
  .overview { display: flex; flex-direction: column; gap: var(--sp-5); }
  h1 { margin: 0; }
  .ov-section { display: flex; flex-direction: column; gap: var(--sp-3); }
  .ov-head { display: flex; align-items: baseline; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; }
  .ov-head h2, .ov-section h2 { margin: 0; }
  .ov-viewall { color: var(--c-accent); text-decoration: none; font-size: var(--text-sm); }
  /* .ov-more's "See all" is an INLINE link within a sentence, so it stays underlined to
     be distinguishable from the surrounding text without color alone (WCAG 1.4.1). */
  .ov-more a { color: var(--c-accent); text-decoration: underline; font-size: var(--text-sm); }
  .ov-viewall:hover { text-decoration: underline; }
  .ov-cats { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .ov-cat {
    font-size: var(--text-sm); color: var(--c-text-2); text-decoration: none;
    padding: 4px 10px; border: 1px solid var(--c-border); border-radius: var(--radius-pill, var(--radius-sm));
    background: var(--c-surface); min-height: var(--touch-min); display: inline-flex; align-items: center;
  }
  .ov-cat:hover { border-color: var(--c-accent); color: var(--c-accent); }
  .knowledge-banner, .all-clear, .empty-fleet {
    display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: baseline;
    padding: var(--sp-3); border-radius: var(--radius-md); font-size: var(--text-sm);
  }
  .knowledge-banner { background: var(--c-warn-bg); border: 1px solid var(--c-warn-border); color: var(--c-text); }
  .knowledge-banner.tone-err { background: var(--c-err-bg); border-color: color-mix(in srgb, var(--c-err) 30%, transparent); }
  .all-clear { background: var(--c-ok-bg); border: 1px solid var(--c-ok-border); color: var(--c-text); }
  /* An empty fleet is a neutral fact, never a green all-clear. */
  .empty-fleet { background: var(--c-surface-inset); border: 1px solid var(--c-border); color: var(--c-text-2); }
  .attn-list, .evi-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .attn-item, .evi-item {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface);
  }
  .attn-reason { color: var(--c-text-3); font-size: var(--text-sm); }
  .evi-at { color: var(--c-text-3); font-size: var(--text-xs); margin-left: auto; }
  .ov-none, .ov-more { color: var(--c-text-3); font-size: var(--text-sm); }
</style>
