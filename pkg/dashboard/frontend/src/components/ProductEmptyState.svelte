<script>
  import EmptyState from './EmptyState.svelte';
  import { knowledgeLabel } from '../lib/entityLabels.ts';

  // Renders a knowledgeState.ViewState honestly (requirement H). It NEVER renders a
  // blanket "all clear": an empty result under incomplete knowledge is shown as
  // "nothing known + knowledge is incomplete", distinct from a genuinely empty fleet
  // and from a filter that matched nothing. Loading/error variants delegate to the
  // shared EmptyState so styling stays in one place.
  let {
    state,
    noun = 'items',
    onRetry = null,
    onClearFilters = null,
  } = $props();
</script>

{#if state.kind === 'loading'}
  <EmptyState loading={true} />
{:else if state.kind === 'backend-error'}
  <EmptyState error={true} title="Can’t reach the Pacto backend" message={state.message} {onRetry} />
{:else if state.kind === 'schema-error'}
  <EmptyState error={true} title="Dashboard is out of date" message={`${state.message} Reload the page or upgrade the dashboard.`} {onRetry} />
{:else if state.kind === 'not-found'}
  <EmptyState error={true} title="Not found" message={state.message} />
{:else if state.kind === 'filtered-empty'}
  <div class="state-box">
    <h3>No matching {noun}</h3>
    <p>No {noun} match the current filters or search.</p>
    {#if onClearFilters}
      <button type="button" class="ps-btn" onclick={onClearFilters}>Clear filters</button>
    {/if}
  </div>
{:else if state.kind === 'empty-unknown'}
  <!-- The non-negotiable case: no items, but knowledge is incomplete. This is NOT
       "all clear" — it is a lack of knowledge, shown as such. -->
  <div class="state-box is-unknown" role="status">
    <h3>No {noun} known</h3>
    <p>Knowledge is incomplete, so this is not a clean bill of health.</p>
    <span class="ps-knowledge">{knowledgeLabel(state.knowledge.level)}</span>
    {#if state.knowledge.unavailableSources > 0}<p class="ps-detail">{state.knowledge.unavailableSources} source(s) unavailable.</p>{/if}
    {#if state.knowledge.staleSources > 0}<p class="ps-detail">{state.knowledge.staleSources} source(s) stale.</p>{/if}
    {#if state.knowledge.degradedSources > 0}<p class="ps-detail">{state.knowledge.degradedSources} source(s) partial.</p>{/if}
    {#if onRetry}<button type="button" class="ps-btn" onclick={onRetry}>Retry</button>{/if}
  </div>
{:else if state.kind === 'empty-fleet'}
  <div class="state-box">
    <h3>No {noun} yet</h3>
    <p>This fleet has no {noun}. Once contracts are published or deployments are observed they appear here.</p>
  </div>
{/if}

<style>
  .state-box {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3);
    gap: var(--sp-2);
  }
  .state-box h3 { color: var(--c-text-2); }
  .state-box.is-unknown h3 { color: var(--c-warn); }
  .ps-knowledge {
    font-size: var(--text-xs); font-weight: 600; color: var(--c-warn);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
    padding: 2px 8px; border-radius: var(--radius-xs);
  }
  .ps-detail { font-size: var(--text-sm); margin: 0; }
  .ps-btn {
    margin-top: var(--sp-2); background: none; border: 1px solid var(--c-border);
    border-radius: var(--radius-xs); color: var(--c-accent); font: inherit;
    padding: 6px 14px; min-height: var(--touch-min); cursor: pointer;
  }
  .ps-btn:hover { background: var(--c-surface-inset); }
</style>
