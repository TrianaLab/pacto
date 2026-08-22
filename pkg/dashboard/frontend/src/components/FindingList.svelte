<script>
  import SeverityBadge from './SeverityBadge.svelte';
  import EntityLink from './EntityLink.svelte';

  // Renders findings for any rich entity page. Items are either a
  // ProductFinding or an AttributedFinding ({ finding, entity }); when attributed, the
  // exact entity the finding belongs to is linked. Evidence support is shown by its
  // truthful total, never fabricated.
  let { items = [] } = $props();
  const finding = (it) => it.finding ?? it;
</script>

<ul class="finding-list">
  {#each items as it, i (i)}
    {@const f = finding(it)}
    <li class="finding">
      <SeverityBadge severity={f.severity} />
      <div class="f-body">
        <span class="f-msg">{f.message || f.code || 'Finding'}</span>
        {#if it.entity}<div class="f-entity"><EntityLink ref={it.entity} showStatus={false} /></div>{/if}
        <span class="f-meta">
          {#if f.category}<span>{f.category}</span>{/if}
          {#if f.contractPath}<span class="f-path">{f.contractPath}</span>{/if}
          {#if f.evidenceRefs?.total}<span>{f.evidenceRefs.total} evidence ref{f.evidenceRefs.total === 1 ? '' : 's'}</span>{/if}
        </span>
      </div>
    </li>
  {/each}
</ul>

<style>
  .finding-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .finding { display: flex; align-items: flex-start; gap: var(--sp-2); }
  .f-body { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .f-msg { color: var(--c-text); font-size: var(--text-sm); }
  .f-entity { font-size: var(--text-sm); }
  .f-meta { display: flex; gap: var(--sp-2); flex-wrap: wrap; color: var(--c-text-3); font-size: var(--text-xs); }
  .f-path { font-family: var(--font-mono, monospace); }
</style>
