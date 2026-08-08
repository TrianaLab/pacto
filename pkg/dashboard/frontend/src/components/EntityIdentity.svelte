<script>
  import { kindLabel } from '../lib/entityLabels.ts';
  import StatusBadge from './StatusBadge.svelte';
  import CopyableIdentifier from './CopyableIdentifier.svelte';

  // Renders enough identity to DISAMBIGUATE an entity from same-named entities in
  // other domains/scopes (requirement F): kind, human label, and the qualifying
  // context (domain / scope / parent service). `ref` is a product EntityRef.
  let { ref = {}, showStatus = true, showKey = false } = $props();

  // The disambiguating context bits, in priority order. Same-named services in two
  // domains differ by domain; targets differ by scope + parent service.
  const context = $derived(
    [
      ref.domain ? `domain ${ref.domain}` : '',
      ref.parentService && ref.parentService !== ref.key ? ref.parentService : '',
      ref.scope ? ref.scope : '',
      // secondary is the copyable-ish extra (a digest or scope); show it only when it
      // is not already the key and not already surfaced as scope.
      ref.secondary && ref.secondary !== ref.scope ? ref.secondary : '',
    ].filter(Boolean),
  );
  const primary = $derived(ref.label || ref.key || '(unknown)');
</script>

<span class="entity-identity">
  <span class="ei-kind">{kindLabel(ref.kind)}</span>
  <span class="ei-label">{primary}</span>
  {#if showStatus && ref.status}
    <StatusBadge status={ref.status} />
  {/if}
  {#if context.length}
    <span class="ei-context">
      {#each context as bit, i}{#if i > 0}<span class="ei-dot" aria-hidden="true">·</span>{/if}<span>{bit}</span>{/each}
    </span>
  {/if}
</span>
{#if showKey && ref.key}
  <div class="ei-key"><CopyableIdentifier value={ref.key} label="canonical key" /></div>
{/if}

<style>
  .entity-identity { display: inline-flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .ei-kind {
    font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em;
    color: var(--c-text-3); background: var(--c-surface-inset);
    padding: 1px 6px; border-radius: var(--radius-xs); flex-shrink: 0;
  }
  .ei-label { font-weight: 600; color: var(--c-text); overflow-wrap: anywhere; }
  .ei-context { font-size: var(--text-xs); color: var(--c-text-3); display: inline-flex; gap: var(--sp-1); flex-wrap: wrap; }
  .ei-dot { color: var(--c-text-3); }
  .ei-key { margin-top: var(--sp-1); }
</style>
