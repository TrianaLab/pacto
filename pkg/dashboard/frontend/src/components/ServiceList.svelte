<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import {
    services, filteredServices, stats, graphData,
    phaseFilter, enabledSources, searchTerm, overviewView,
    navigateTo, isSourceEnabled, toggleSourceClick,
  } from '../lib/stores.js';
  import { getSources, pct, sourceTooltips, sourceColors } from '../lib/helpers.js';
  import PhaseBadge from './PhaseBadge.svelte';
  import SourcePill from './SourcePill.svelte';
  import GraphView from './GraphView.svelte';
  import DebugPanel from './DebugPanel.svelte';

  let loading = $state(true);
  let error = $state(null);
  let debugData = $state(null);

  onMount(async () => {
    // If services already loaded (from App), skip loading
    let svcList;
    services.subscribe((v) => (svcList = v))();
    if (svcList?.length) {
      loading = false;
    } else {
      try {
        const [svcRes, graphRes] = await Promise.all([
          api.listServices(),
          api.getGraph().catch(() => null),
        ]);
        services.set(svcRes || []);
        graphData.set(graphRes);
      } catch (e) {
        error = e.message;
      }
      loading = false;
    }
    // Load debug data in background
    api.getDebugSources().then((d) => (debugData = d)).catch(() => {});
  });

  function toggleFilter(f) {
    phaseFilter.update((cur) => (cur === f ? 'all' : f));
  }

  function clearAllFilters() {
    phaseFilter.set('all');
    enabledSources.set({});
    searchTerm.set('');
  }

  function toggleSource(src) {
    enabledSources.update((cur) => toggleSourceClick(src, cur));
  }

  function switchView(view) {
    overviewView.set(view);
    history.replaceState(null, '', view === 'graph' ? '#graph' : '#');
  }

  // Computed: at-risk services (degraded/invalid, sorted by blast radius)
  let atRiskServices = $derived.by(() => {
    return $filteredServices
      .filter((s) => s.phase === 'Degraded' || s.phase === 'Invalid')
      .sort((a, b) => {
        const br = (b.blastRadius || 0) - (a.blastRadius || 0);
        if (br !== 0) return br;
        const sev = { Invalid: 0, Degraded: 1 };
        return (sev[a.phase] ?? 2) - (sev[b.phase] ?? 2);
      });
  });

  // Computed: active source types for filter bar
  let sourceTypes = $derived.by(() => {
    const types = {};
    for (const svc of $services) {
      for (const src of getSources(svc)) types[src] = true;
    }
    return Object.keys(types).sort();
  });

  let hasActiveFilter = $derived(
    $phaseFilter !== 'all' || Object.keys($enabledSources).length > 0 || !!$searchTerm
  );

  let filterLabel = $derived.by(() => {
    const parts = [];
    if ($phaseFilter !== 'all') parts.push($phaseFilter);
    const srcKeys = Object.keys($enabledSources);
    if (srcKeys.length) parts.push('sources: ' + srcKeys.join(', '));
    if ($searchTerm) parts.push('search: "' + $searchTerm + '"');
    return parts.join(' \u2014 ');
  });
</script>

{#if loading}
  <div class="loading"><div class="spinner"></div>Loading...</div>
{:else if error}
  <div class="empty-state">
    <div class="empty-state-title">Failed to load</div>
    <p>{error}</p>
  </div>
{:else if $services.length === 0}
  <h1 class="page-title">Overview</h1>
  <p class="page-subtitle">0 contracts</p>
  <div class="empty-state">
    <div class="empty-state-title">No Pacto resources found</div>
    <p>No service contracts detected from any source.</p>
  </div>
  {#if debugData}
    <DebugPanel data={debugData} />
  {/if}
{:else}
  <h1 class="page-title">Overview</h1>
  <p class="page-subtitle">
    {$stats.total} contract{$stats.total !== 1 ? 's' : ''} &mdash; {$stats.monitored} active{$stats.unknown > 0 ? ', ' + $stats.unknown + ' unmonitored' : ''}
  </p>

  <!-- Status bar -->
  {#if $stats.monitored > 0 || $stats.unknown > 0}
    <div class="status-bar">
      <div class="status-bar-ok" style="width:{pct($stats.healthy, $stats.total)}%"></div>
      <div class="status-bar-warning" style="width:{pct($stats.degraded, $stats.total)}%"></div>
      <div class="status-bar-critical" style="width:{pct($stats.invalid, $stats.total)}%"></div>
      <div class="status-bar-neutral" style="width:{pct($stats.unknown, $stats.total)}%"></div>
    </div>
  {/if}

  <!-- Stats bar -->
  <div class="stats-bar">
    <button class="stat stat-neutral" class:stat-active={$phaseFilter === 'all'} onclick={() => toggleFilter('all')} aria-pressed={$phaseFilter === 'all'}>
      <div class="stat-value">{$stats.total}</div>
      <div class="stat-label">All</div>
    </button>
    <button class="stat stat-ok" class:stat-active={$phaseFilter === 'Healthy'} onclick={() => toggleFilter('Healthy')} aria-pressed={$phaseFilter === 'Healthy'}>
      <div class="stat-value">{$stats.healthy}</div>
      <div class="stat-label">Healthy</div>
    </button>
    <button class="stat stat-warning" class:stat-active={$phaseFilter === 'Degraded'} onclick={() => toggleFilter('Degraded')} aria-pressed={$phaseFilter === 'Degraded'}>
      <div class="stat-value">{$stats.degraded}</div>
      <div class="stat-label">Degraded</div>
    </button>
    <button class="stat stat-critical" class:stat-active={$phaseFilter === 'Invalid'} onclick={() => toggleFilter('Invalid')} aria-pressed={$phaseFilter === 'Invalid'}>
      <div class="stat-value">{$stats.invalid}</div>
      <div class="stat-label">Invalid</div>
    </button>
    {#if $stats.unknown > 0}
      <button class="stat stat-neutral" class:stat-active={$phaseFilter === 'Unknown'} onclick={() => toggleFilter('Unknown')} aria-pressed={$phaseFilter === 'Unknown'}>
        <div class="stat-value">{$stats.unknown}</div>
        <div class="stat-label">Unmonitored</div>
      </button>
    {/if}
  </div>

  <!-- View tabs -->
  <div class="view-tabs">
    <button class="view-tab" class:active={$overviewView === 'table'} onclick={() => switchView('table')}>Table</button>
    <button class="view-tab" class:active={$overviewView === 'graph'} onclick={() => switchView('graph')}>Graph</button>
  </div>

  <!-- Table view -->
  {#if $overviewView === 'table'}
    {#if hasActiveFilter}
      <div style="margin-bottom:16px">
        <span class="pill pill-accent" style="font-size:12px">Showing: {filterLabel}</span>
        <button class="filter-clear" onclick={clearAllFilters}>clear</button>
      </div>
    {/if}

    <!-- At risk section -->
    {#if atRiskServices.length > 0}
      <div style="margin-bottom:32px">
        <div class="section-heading">Needs Attention <span class="text-dim" style="font-weight:400;font-size:var(--text-sm)">{atRiskServices.length} service{atRiskServices.length > 1 ? 's' : ''}</span></div>
        <div class="at-risk-grid">
          {#each atRiskServices as svc}
            <button
              class="alert-card {svc.phase === 'Invalid' ? 'alert-card-critical' : 'alert-card-warning'}"
              onclick={() => navigateTo('detail', svc.name)}
            >
              <div class="alert-card-body">
                <div class="alert-card-title">{svc.name}</div>
                <div class="alert-card-desc">{svc.topInsight || svc.phase}</div>
              </div>
              <div class="alert-card-meta">
                <PhaseBadge phase={svc.phase} />
                {#if svc.checksFailed > 0}
                  <span class="pill pill-critical">{svc.checksFailed} failed</span>
                {/if}
                {#if svc.blastRadius > 0}
                  <span class="pill pill-accent">{'\u26A1'} {svc.blastRadius} affected</span>
                {/if}
              </div>
            </button>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Source filter bar -->
    {#if sourceTypes.length > 1}
      <div class="source-filter-bar">
        <span class="source-filter-label">Source</span>
        {#each sourceTypes as st}
          <button
            class="source-filter-btn"
            class:active={isSourceEnabled(st, $enabledSources)}
            title={sourceTooltips[st] || st}
            onclick={() => toggleSource(st)}
          >
            <span class="pill-dot" style="background:{sourceColors[st] || 'var(--neutral)'}"></span>
            {st.toUpperCase()}
          </button>
        {/each}
      </div>
    {/if}

    <!-- Service table -->
    <div class="section-heading">All Contracts</div>
    <div class="table-wrapper">
      <table>
        <thead>
          <tr>
            <th>Service</th>
            <th>Compliance</th>
            <th>Checks</th>
            <th>Impact</th>
            <th class="hide-narrow">Version</th>
            <th class="hide-narrow">Insight</th>
            <th>Sources</th>
          </tr>
        </thead>
        <tbody>
          {#each $filteredServices as svc}
            {@const sources = getSources(svc)}
            {@const cTotal = svc.checksTotal != null ? svc.checksTotal : -1}
            {@const cPassed = svc.checksPassed != null ? svc.checksPassed : 0}
            {@const cFailed = svc.checksFailed != null ? svc.checksFailed : 0}
            <tr data-click onclick={() => navigateTo('detail', svc.name)}>
              <td><a>{svc.name}</a></td>
              <td>
                {#if svc.complianceStatus || svc.phase === 'Reference'}
                  <span class="badge {({'OK':'badge-ok','WARNING':'badge-warning','ERROR':'badge-critical','REFERENCE':'badge-neutral'})[svc.complianceStatus || 'REFERENCE']}">{svc.complianceStatus || 'REFERENCE'}</span>
                {/if}
                {#if svc.complianceScore != null}
                  <span class="compliance-score {svc.complianceScore < 50 ? 'compliance-score-error' : svc.complianceScore < 80 ? 'compliance-score-warning' : 'compliance-score-ok'}">{svc.complianceScore}%</span>
                {/if}
                {#if svc.complianceErrors > 0}
                  <span class="pill pill-critical" style="font-size:10px">{svc.complianceErrors}E</span>
                {/if}
                {#if svc.complianceWarnings > 0}
                  <span class="pill pill-warning" style="font-size:10px">{svc.complianceWarnings}W</span>
                {/if}
                {#if !svc.complianceStatus && svc.phase !== 'Reference'}
                  <PhaseBadge phase={svc.phase} />
                {/if}
              </td>
              <td>
                {#if cTotal >= 0}
                  <span class="count {cFailed > 0 ? 'count-error' : 'count-zero'}">{cPassed}/{cTotal}</span>
                {:else}
                  <span class="count count-zero">&mdash;</span>
                {/if}
              </td>
              <td>
                {#if svc.blastRadius > 0 && svc.phase !== 'Healthy'}
                  <span class="blast-radius" style="color:var(--warning);background:var(--warning-bg)">{svc.blastRadius} affected</span>
                {:else if svc.blastRadius > 0}
                  <span class="blast-radius">{svc.blastRadius} dependent{svc.blastRadius > 1 ? 's' : ''}</span>
                {:else if svc.dependencyCount > 0}
                  <span class="text-dim">{svc.dependencyCount} dep{svc.dependencyCount > 1 ? 's' : ''}</span>
                {:else}
                  <span class="count count-zero">&mdash;</span>
                {/if}
              </td>
              <td class="hide-narrow"><span class="text-dim">{svc.version || '\u2014'}</span></td>
              <td class="hide-narrow"><span class="text-dim">{svc.topInsight || '\u2014'}</span></td>
              <td>
                {#each sources as src}
                  <SourcePill type={src} />
                {/each}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <!-- Graph view -->
    <GraphView />
  {/if}

  {#if debugData}
    <DebugPanel data={debugData} />
  {/if}
{/if}
