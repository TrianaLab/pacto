<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import SourceDot from '../components/SourceDot.svelte';
  import { sourceTooltip } from '../lib/format.ts';

  // Renders a stable, self-explaining placeholder for a section that has no
  // data to show, so the dashboard never silently hides a section. `meta` is the
  // SectionInfo from the backend ({ state, reason, source }). `onRetry`, when
  // provided, renders a retry affordance (used for "unavailable" client fetches).
  let { title, meta = null, open = $bindable(false), id = '', onRetry = null } = $props();

  const state = $derived(meta?.state ?? 'empty');
  const label = $derived(
    state === 'not_applicable' ? 'Not applicable'
    : state === 'unavailable' ? "Couldn't load"
    : 'None declared',
  );
  const cls = $derived(
    state === 'unavailable' ? 'state-unavailable'
    : state === 'not_applicable' ? 'state-na'
    : 'state-empty',
  );
</script>

<CollapsibleSection {title} bind:open {id}>
  <div class="section-state {cls}">
    {#if meta?.source}<SourceDot source={meta.source} align="right" />{/if}
    <span class="state-label">{label}</span>
    {#if meta?.reason}<span class="state-reason">{meta.reason}</span>{/if}
    {#if onRetry && state === 'unavailable'}
      <button type="button" class="state-retry" onclick={onRetry}>Retry</button>
    {/if}
  </div>
</CollapsibleSection>

<style>
  .section-state {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    padding: var(--sp-3);
    border: 1px dashed var(--c-border);
    border-radius: var(--radius-sm);
    font-size: var(--text-sm);
  }
  .state-label { font-weight: 600; }
  .state-reason { color: var(--c-text-2); }
  .state-empty .state-label { color: var(--c-text-3); }
  .state-na .state-label { color: var(--c-text-2); }
  .state-unavailable {
    border-color: var(--c-warn);
    border-style: solid;
  }
  .state-unavailable .state-label { color: var(--c-warn); }
  .state-retry {
    margin-left: auto;
    background: none;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-xs);
    color: var(--c-accent);
    font: inherit;
    padding: 4px 10px;
    cursor: pointer;
  }
  .state-retry:hover { background: var(--c-surface-hover, var(--c-surface-inset)); }
</style>
