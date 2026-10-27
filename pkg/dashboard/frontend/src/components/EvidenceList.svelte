<script module>
  /**
   * groupEvidence puts runs of evidence that share an environment prefix
   * ("prod/Deployment") and a printed time into one group, so a long prefix and a
   * timestamp are printed once per run instead of on every row. `when` formats the
   * time; items with no prefix stay groups of one.
   */
  export function groupEvidence(items, when) {
    const groups = [];
    for (const ev of items ?? []) {
      const label = ev.target?.label || ev.target?.key || '';
      const cut = label.lastIndexOf('/');
      const prefix = cut > 0 ? label.slice(0, cut) : '';
      const at = ev.at ? when(ev.at) : '';
      const last = groups.at(-1);
      if (prefix && last && last.prefix === prefix && last.at === at) last.items.push({ ev, name: label.slice(cut + 1) });
      else groups.push({ prefix, at, iso: ev.at, items: [{ ev, name: label.slice(cut + 1) }] });
    }
    return groups;
  }
</script>

<script>
  import EntityLink from './EntityLink.svelte';
  import { formatDate } from '../lib/dateFormat.ts';

  // Renders recent/related evidence as navigable rows: the evidenced target plus when
  // it was observed. Reused by the overview and the service page. Rows that share an
  // environment and a time are grouped under one heading line.
  let { items = [], when = formatDate } = $props();
  const groups = $derived(groupEvidence(items, when));
</script>

<ul class="evi-list">
  {#each groups as g, i (i)}
    {#if g.items.length === 1}
      <li class="evi-row">
        <EntityLink ref={g.items[0].ev.target} showStatus={false} />
        {#if g.at}<time class="evi-at" datetime={g.iso}>{g.at}</time>{/if}
      </li>
    {:else}
      <li class="evi-group">
        <div class="evi-head"><span class="evi-prefix">{g.prefix}/</span>{#if g.at}<time class="evi-at" datetime={g.iso}>{g.at}</time>{/if}</div>
        <ul class="evi-names">
          {#each g.items as it, j (j)}
            <li><EntityLink ref={{ ...it.ev.target, label: it.name }} showStatus={false} showKind={false} /></li>
          {/each}
        </ul>
      </li>
    {/if}
  {/each}
</ul>

<style>
  .evi-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .evi-row, .evi-head { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; padding: var(--sp-1) var(--sp-2); border-radius: var(--radius-xs); }
  .evi-row:hover { background: var(--c-surface-hover); }
  .evi-prefix { color: var(--c-text-2); font-size: var(--text-sm); overflow-wrap: anywhere; min-width: 0; }
  .evi-names { list-style: none; margin: 0; padding: 0 var(--sp-2) 0 var(--sp-5); display: flex; flex-direction: column; gap: 2px; font-size: var(--text-sm); }
  .evi-at { color: var(--c-text-3); font-size: var(--text-xs); margin-left: auto; white-space: nowrap; }
</style>
