<script>
  import { fleetEntityUrl } from '../lib/router.ts';
  import { sourceHealthLabel, sourceHealthTone } from '../lib/entityLabels.ts';

  // Renders per-source health as navigable chips (requirement G: which sources are
  // partial, stale or unavailable). Each chip links to that source's detail. Sources
  // are ordered least-healthy first so a degraded source is never buried, and a
  // truncation note is honest when meta carried only the least-healthy subset.
  //
  // Every chip is a LINK, and it has to look like one before it is hovered: on a touch
  // screen there is no hover, and a row of bordered pills reads as status badges. So the
  // name is accent-coloured like every other link in the product AND underlines on
  // hover or focus -- two cues, neither of them hue alone (WCAG 1.4.1).
  // `label` names the LANDMARK, so it must not repeat the heading of the section it
  // sits in -- two landmarks called "Data sources" is a rotor with two identical rows.
  let { sources = [], truncated = false, label = 'Data sources by health' } = $props();

  const RANK = { unavailable: 0, stale: 1, partial: 2, available: 3 };
  const ordered = $derived(
    [...(sources ?? [])].sort((a, b) => (RANK[a?.status] ?? 9) - (RANK[b?.status] ?? 9)),
  );
</script>

<nav class="source-health" aria-label={label}>
  {#if ordered.length === 0}
    <span class="sh-none">No sources reported.</span>
  {:else}
    {#each ordered as s (s.id)}
      <a class="sh-chip tone-{sourceHealthTone(s.status)}" href={fleetEntityUrl('source', s.id)} title={s.error?.message || ''}>
        <span class="sh-dot" aria-hidden="true"></span>
        <span class="sh-id">{s.id}</span>
        {#if s.kind && s.kind !== s.id}<span class="sh-kind">{s.kind}</span>{/if}
        <span class="sh-status">{sourceHealthLabel(s.status)}</span>
      </a>
    {/each}
    {#if truncated}<span class="sh-trunc" title="Only the least-healthy sources are shown">+ more sources</span>{/if}
  {/if}
</nav>

<style>
  .source-health { display: flex; flex-wrap: wrap; gap: var(--sp-2); }
  .sh-none { color: var(--c-text-3); font-size: var(--text-sm); }
  .sh-chip {
    display: inline-flex; align-items: center; gap: var(--sp-1);
    padding: 2px 8px; border-radius: var(--radius-sm); text-decoration: none;
    font-size: var(--text-xs); color: var(--c-text-2);
    border: 1px solid var(--c-border); background: var(--c-surface);
  }
  .sh-chip:hover, .sh-chip:focus-visible { border-color: var(--c-accent); }
  .sh-chip:hover .sh-id, .sh-chip:focus-visible .sh-id { text-decoration: underline; }
  .sh-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--tone-c, var(--c-neutral)); flex-shrink: 0; }
  /* The destination, so it carries the product's link colour; the kind and the status
     beside it are metadata and stay quiet. */
  .sh-id { font-weight: 600; color: var(--c-accent); }
  .sh-kind { color: var(--c-text-3); }
  .sh-status { color: var(--tone-c, var(--c-text-3)); font-weight: 500; }
  .sh-trunc { color: var(--c-text-3); font-size: var(--text-xs); align-self: center; }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-info { --tone-c: var(--c-info); }
  .tone-neutral { --tone-c: var(--c-neutral); }
</style>
