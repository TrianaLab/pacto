<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import SourceDot from '../components/SourceDot.svelte';
  import { sourceTooltip } from '../lib/format.ts';
  import { api } from '../lib/api.ts';

  // Drill-down provenance: what each connected source contributes to the merged
  // view. Lazily fetches /api/services/{name}/sources when first opened.
  let { name, open = $bindable(false), id = '' } = $props();

  let data = $state(null);
  let error = $state(false);
  let loaded = $state(false);

  async function load() {
    if (loaded) return;
    loaded = true;
    try {
      data = await api.serviceSources(name);
    } catch {
      error = true;
      loaded = false; // allow retry
    }
  }

  $effect(() => {
    if (open && !loaded) load();
  });

  function summarize(svc) {
    return [
      svc?.version && `v${svc.version}`,
      svc?.contractStatus,
      svc?.interfaces?.length && `${svc.interfaces.length} interfaces`,
      svc?.configurations?.length && `${svc.configurations.length} configs`,
      svc?.policies?.length && `${svc.policies.length} policies`,
      svc?.dependencies?.length && `${svc.dependencies.length} deps`,
      svc?.docs?.length && `${svc.docs.length} docs`,
      svc?.readiness && 'readiness',
      svc?.observedRuntime && 'observed runtime',
      svc?.resources && 'resources',
    ].filter(Boolean);
  }
</script>

<CollapsibleSection title="Sources" bind:open {id}>
  {#if error}
    <div class="src-note src-err">
      Couldn't load the source breakdown.
      <button type="button" class="src-retry" onclick={() => { error = false; load(); }}>Retry</button>
    </div>
  {:else if !data}
    <div class="src-note">Loading…</div>
  {:else}
    <p class="src-intro">
      What each connected source contributes. The view is priority-merged:
      <strong>k8s</strong> runtime over <strong>local</strong> over <strong>oci</strong> over <strong>cache</strong> for the contract base.
    </p>
    {#each data.sources || [] as s}
      <div class="src-row">
        <SourceDot source={s.sourceType} />
        <span class="src-type">{s.sourceType}</span>
        <span class="src-summary">{summarize(s.service).join(' · ') || '—'}</span>
      </div>
    {/each}
  {/if}
</CollapsibleSection>

<style>
  .src-intro { font-size: var(--text-sm); color: var(--c-text-2); margin: 0 0 var(--sp-3); }
  .src-note { font-size: var(--text-sm); color: var(--c-text-2); display: flex; align-items: center; gap: var(--sp-2); }
  .src-err { color: var(--c-warn); }
  .src-retry {
    background: none; border: 1px solid var(--c-border); border-radius: var(--radius-xs);
    color: var(--c-accent); font: inherit; padding: 4px 10px; cursor: pointer;
  }
  .src-row {
    display: flex; align-items: center; gap: var(--sp-2);
    padding: var(--sp-2) 0; border-top: 1px solid var(--c-border); font-size: var(--text-sm);
  }
  .src-row:first-of-type { border-top: none; }
  .src-type { font-weight: 600; min-width: 56px; text-transform: uppercase; font-size: var(--text-xs); letter-spacing: 0.03em; }
  .src-summary { color: var(--c-text-2); }
</style>
