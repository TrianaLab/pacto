<script>
  import { serviceUrl, compareDiffUrl } from '../lib/router.ts';
  import StatusBadge from './StatusBadge.svelte';
  import OwnerLink from './OwnerLink.svelte';
  import SourceDot from './SourceDot.svelte';

  let { data = null, onClose } = $props();

  function onKeydown(e) { if (e.key === 'Escape') onClose?.(); }
</script>

<svelte:window onkeydown={onKeydown} />

{#if data}
  <aside class="drawer" data-motion aria-label="Service details for {data.name}">
    <div class="drawer-head">
      <div class="drawer-title">
        <span class="drawer-name">{data.name}</span>
        {#if data.external}
          <span class="badge">external</span>
        {:else}
          <StatusBadge status={data.status} />
        {/if}
      </div>
      <button type="button" class="drawer-close" onclick={onClose} aria-label="Close details">×</button>
    </div>

    {#if !data.external}
      <dl class="drawer-meta">
        {#if data.version}<div><dt>Version</dt><dd>{data.version}</dd></div>{/if}
        {#if data.owner}<div><dt>Owner</dt><dd><OwnerLink owner={data.owner} /></dd></div>{/if}
        {#if data.sources.length}
          <div><dt>Sources</dt><dd class="drawer-sources">{#each data.sources as s}<span class="src"><SourceDot source={s} />{s}</span>{/each}</dd></div>
        {/if}
        <div><dt>Blast radius</dt><dd>{data.blastRadius}</dd></div>
      </dl>
    {/if}

    <div class="drawer-lists">
      <div>
        <div class="drawer-list-label">Depends on <span class="count">{data.dependencies.length}</span></div>
        {#if data.dependencies.length}
          <ul>{#each data.dependencies as d}<li><a href={serviceUrl(d.name)}>{d.name}</a>{#if d.type === 'reference'} <span class="badge badge-accent">ref</span>{:else if d.required} <span class="badge badge-info">req</span>{/if}</li>{/each}</ul>
        {:else}<p class="drawer-empty">—</p>{/if}
      </div>
      <div>
        <div class="drawer-list-label">Depended on by <span class="count">{data.dependents.length}</span></div>
        {#if data.dependents.length}
          <ul>{#each data.dependents as name}<li><a href={serviceUrl(name)}>{name}</a></li>{/each}</ul>
        {:else}<p class="drawer-empty">—</p>{/if}
      </div>
    </div>

    {#if !data.external}
      <div class="drawer-actions">
        <a class="btn btn-sm" href={serviceUrl(data.name)}>Open detail</a>
        <a class="btn btn-sm btn-ghost" href={compareDiffUrl({ fromName: data.name, toName: data.name })}>Compare</a>
      </div>
    {/if}
  </aside>
{/if}

<style>
  .drawer {
    position: absolute; top: 0; right: 0; bottom: 0; width: min(320px, 86%);
    background: var(--c-surface); border-left: 1px solid var(--c-border);
    box-shadow: var(--shadow-md); z-index: 20;
    padding: var(--sp-4); overflow-y: auto;
    display: flex; flex-direction: column; gap: var(--sp-4);
    animation: drawer-in 160ms ease-out both;
  }
  .drawer-head { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--sp-2); }
  .drawer-title { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .drawer-name { font-weight: 700; font-size: var(--text-md, 1rem); }
  .drawer-close {
    background: none; border: none; color: var(--c-text-3); cursor: pointer;
    font-size: 22px; line-height: 1; padding: 0 4px;
  }
  .drawer-close:hover { color: var(--c-text); }
  .drawer-meta { display: grid; gap: var(--sp-2); margin: 0; }
  .drawer-meta div { display: flex; justify-content: space-between; gap: var(--sp-2); }
  .drawer-meta dt { color: var(--c-text-3); font-size: var(--text-xs); }
  .drawer-meta dd { margin: 0; font-size: var(--text-sm); text-align: right; }
  .drawer-sources { display: inline-flex; gap: var(--sp-2); flex-wrap: wrap; }
  .drawer-sources .src { display: inline-flex; align-items: center; gap: 4px; text-transform: uppercase; font-size: 10px; }
  .drawer-lists { display: grid; gap: var(--sp-3); }
  .drawer-list-label { font-size: var(--text-xs); font-weight: 600; color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.04em; margin-bottom: var(--sp-1); }
  .drawer-list-label .count { color: var(--c-text-2); }
  .drawer-lists ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 2px; }
  .drawer-lists li { font-size: var(--text-sm); }
  .drawer-empty { color: var(--c-text-3); margin: 0; }
  .drawer-actions { display: flex; gap: var(--sp-2); margin-top: auto; }
  @keyframes drawer-in { from { transform: translateX(12px); opacity: 0; } to { transform: none; opacity: 1; } }
</style>
