<script>
  import ShowAll from '../components/ShowAll.svelte';
  import { cap } from '../lib/cap.svelte.ts';
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import SectionState from './SectionState.svelte';

  let { sbom = null, open = $bindable(false), id = '' } = $props();
  let packages = $derived(sbom?.packages ?? []);
  let hasContent = $derived(packages.length > 0);
  const rows = cap(50);
</script>

{#if hasContent}
  <CollapsibleSection title="SBOM" count={packages.length} bind:open {id}>
    <p class="sbom-format">Format <code>{sbom.format}</code> · {packages.length} packages</p>
    <div class="table-wrap">
    <table class="sbom-table">
      <thead><tr><th>Package</th><th>Version</th><th>License</th><th>Supplier</th></tr></thead>
      <tbody>
        {#each rows.slice(packages) as p, i (i)}
          <tr>
            <td>{p.name}</td>
            <td>{p.version || '—'}</td>
            <td>{p.license || '—'}</td>
            <td>{p.supplier || '—'}</td>
          </tr>
        {/each}
      </tbody>
    </table>
    </div>
    <ShowAll cap={rows} total={packages.length} noun="packages" testid="sbom-show-all" />
  </CollapsibleSection>
{:else}
  <SectionState title="SBOM" bind:open {id} />
{/if}

<style>
  .sbom-format { font-size: var(--text-xs); color: var(--c-text-3); margin: 0 0 var(--sp-2); }
  .sbom-table { width: 100%; border-collapse: collapse; font-size: var(--text-sm); }
  .sbom-table th, .sbom-table td { text-align: left; padding: var(--sp-1) var(--sp-2); border-bottom: 1px solid var(--c-border); }
  .sbom-table th { color: var(--c-text-3); font-weight: 500; }
</style>
