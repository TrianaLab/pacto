<script>
  import EntityLink from './EntityLink.svelte';

  // Renders knowledge limitations for any rich entity page (requirement K/D): the
  // honest "what we could not determine" list. Items are either a Limitation or an
  // AttributedLimitation ({ limitation, entity }); when attributed, the exact entity
  // the limitation applies to is linked.
  let { items = [] } = $props();
  const lim = (it) => it.limitation ?? it;
</script>

<ul class="lim-list">
  {#each items as it, i (i)}
    {@const l = lim(it)}
    <li class="lim">
      <code class="lim-code">{l.code}</code>
      <span class="lim-msg">{l.message}</span>
      {#if it.entity}<EntityLink ref={it.entity} showStatus={false} />{/if}
    </li>
  {/each}
</ul>

<style>
  .lim-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .lim { display: flex; align-items: baseline; gap: var(--sp-2); flex-wrap: wrap; }
  .lim-code {
    font-family: var(--font-mono, monospace); font-size: var(--text-xs);
    color: var(--c-warn); background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
    padding: 1px 6px; border-radius: var(--radius-xs); flex-shrink: 0;
  }
  .lim-msg { color: var(--c-text-2); font-size: var(--text-sm); }
</style>
