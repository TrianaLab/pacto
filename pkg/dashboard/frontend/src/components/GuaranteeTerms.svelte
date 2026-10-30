<script>
  import { guaranteeTerms } from '../lib/behavior.ts';

  // The guarantees an interface offers, or (`required`) those a dependency requires,
  // as label/value pairs so each value keeps its meaning for a screen reader.
  let { guarantees = null, required = false, label = 'Guarantees' } = $props();
  const terms = $derived(guaranteeTerms(guarantees, required));
</script>

{#if terms.length}
  <!-- A dl takes no accessible name, so a named group carries the label. -->
  <div role="group" aria-label={label}>
    <dl class="gt">
      {#each terms as t (t.label)}<div class="gt-term"><dt>{t.label}</dt><dd>{#if t.bound}<span class="gt-bound">{t.bound}</span>{' '}{/if}<code>{t.value}</code></dd></div>{/each}
    </dl>
  </div>
{/if}

<style>
  .gt { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-2); margin: 0; }
  .gt-term {
    display: inline-flex; gap: var(--sp-1); align-items: baseline; max-width: 100%;
    font-size: var(--text-xs); padding: 1px 8px; border-radius: var(--radius-xs);
    background: var(--c-surface-inset); border: 1px solid var(--c-border);
  }
  .gt dt { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .gt dd { margin: 0; color: var(--c-text); overflow-wrap: anywhere; }
  .gt dd code { font-size: inherit; }
  .gt-bound { color: var(--c-text-2); }
</style>
