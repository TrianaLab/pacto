<script>
  import { digestParts } from '../lib/delivery.ts';

  // A server sentence with its digests short: the full id is on hover, and a
  // click copies it. known resolves the short ids the server already wrote.
  let { text = '', known = [] } = $props();
  const parts = $derived(digestParts(text, known));

  async function copy(full) {
    // ponytail: the clipboard may be unavailable (insecure context); the full id is still in the title.
    try { await navigator.clipboard?.writeText(full); } catch { /* nothing to do */ }
  }
</script>

{#each parts as p, i (i)}{#if p.full}<button type="button" class="dt-id" title={`${p.full} (click to copy)`} aria-label={`${p.text}, copy the full id`} onclick={() => copy(p.full)}>{p.text}</button>{:else}{p.text}{/if}{/each}

<style>
  .dt-id {
    font-family: var(--font-mono); font-size: 0.95em; color: inherit;
    background: var(--c-surface-inset); border: none; border-radius: var(--radius-xs);
    padding: 0 4px; cursor: copy;
  }
  .dt-id:hover { color: var(--c-text); }
</style>
