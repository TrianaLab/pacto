<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import MarkdownView from '../MarkdownView.svelte';
  import DocModal from '../DocModal.svelte';

  let { docs = [], referencedPaths = [], open = $bindable(false), id = '', source = '' } = $props();

  let hasContent = $derived(docs?.length > 0);
  let expanded = $state({});
  let modalDoc = $state(null);

  function toggle(i) {
    expanded = { ...expanded, [i]: !expanded[i] };
  }
</script>

{#if hasContent}
  <CollapsibleSection title="Documentation" count={docs.length} bind:open {id} {source}>
    {#each docs as d, i}
      <div class="detail-card">
        <div class="detail-card-header">
          <button type="button" class="detail-card-header-btn" onclick={() => toggle(i)}>
            <span class="detail-card-header-left">
              <span class="expand-icon" data-motion class:open={expanded[i]}>
                <svg viewBox="0 0 12 12" fill="none"><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </span>
              <span class="detail-card-title">{d.title}</span>
              <code class="detail-card-sub">{d.path}</code>
            </span>
          </button>
          <span class="detail-card-header-right">
            {#if referencedPaths.includes(d.path)}
              <span class="pill pill-ref">referenced by readiness</span>
            {/if}
            <button type="button" class="fullscreen-btn" title="Read full screen" aria-label="Read full screen"
              onclick={() => { modalDoc = d; }}>
              <svg viewBox="0 0 14 14" fill="none" aria-hidden="true"><path d="M2 5V2h3M12 5V2H9M2 9v3h3M12 9v3H9" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>
            </button>
          </span>
        </div>
        {#if expanded[i]}
          <div class="detail-card-body">
            <MarkdownView content={d.content} truncated={d.truncated} />
          </div>
        {/if}
      </div>
    {/each}
  </CollapsibleSection>
  <DocModal doc={modalDoc} onClose={() => { modalDoc = null; }} />
{/if}

<style>
  .detail-card {
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    background: var(--c-surface);
    margin-bottom: var(--sp-2);
  }
  .detail-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    gap: var(--sp-2);
    padding-right: var(--sp-3);
  }
  .detail-card-header-btn {
    display: flex;
    align-items: center;
    flex: 1 1 auto;
    min-width: 0;
    padding: var(--sp-3);
    background: none;
    border: none;
    font: inherit;
    color: var(--c-text);
    text-align: left;
    cursor: pointer;
    border-radius: var(--radius-sm);
  }
  .detail-card-header-btn:hover { background: var(--c-surface-hover, var(--c-surface-inset)); }
  .detail-card-header-right { display: flex; align-items: center; gap: var(--sp-2); flex-shrink: 0; }
  .fullscreen-btn {
    display: inline-flex; align-items: center; justify-content: center;
    width: 26px; height: 26px;
    background: none;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-xs);
    color: var(--c-text-3);
    cursor: pointer;
  }
  .fullscreen-btn:hover { background: var(--c-surface-hover, var(--c-surface-inset)); color: var(--c-text); }
  .fullscreen-btn svg { width: 14px; height: 14px; }
  .detail-card-header-left { display: flex; align-items: center; gap: var(--sp-2); min-width: 0; }
  .expand-icon { display: inline-flex; color: var(--c-text-3); transform: rotate(-90deg); flex-shrink: 0; }
  .expand-icon.open { transform: rotate(0deg); }
  .expand-icon svg { width: 12px; height: 12px; }
  .detail-card-title { font-weight: 600; }
  .detail-card-sub { font-size: var(--text-sm); color: var(--c-text-2); }
  .pill-ref { background: var(--c-accent-bg); color: var(--c-accent); font-size: var(--text-xs); flex-shrink: 0; }
  .detail-card-body { padding: 0 var(--sp-3) var(--sp-3); }
</style>
