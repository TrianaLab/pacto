<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';

  let { validation, conditions = [], open = $bindable(false), id = '' } = $props();

  let hasContent = $derived(
    (validation?.errors?.length > 0) ||
    (validation?.warnings?.length > 0) ||
    (conditions?.length > 0)
  );
</script>

{#if hasContent}
  <CollapsibleSection title="Validation" bind:open {id}>
    {#if validation?.errors?.length > 0}
      <div class="subsection">
        <h3 style="color:var(--c-err)">Errors</h3>
        {#each validation.errors as issue}
          <div class="insight insight-critical">
            <code>{issue.path}</code> <strong>[{issue.code}]</strong> {issue.message}
          </div>
        {/each}
      </div>
    {/if}
    {#if validation?.warnings?.length > 0}
      <div class="subsection">
        <h3 style="color:var(--c-warn)">Warnings</h3>
        {#each validation.warnings as issue}
          <div class="insight insight-warning">
            <code>{issue.path}</code> <strong>[{issue.code}]</strong> {issue.message}
          </div>
        {/each}
      </div>
    {/if}
  </CollapsibleSection>
{/if}

<style>
  .subsection { margin-top: var(--sp-4); }
  .subsection h3 { margin-bottom: var(--sp-2); }
</style>
