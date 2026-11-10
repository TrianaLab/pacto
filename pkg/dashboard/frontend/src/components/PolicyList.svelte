<script>
  import ContractReference from './ContractReference.svelte';
  import SchemaContent from './SchemaContent.svelte';

  // A revision's policies: name, local or remote kind, definition and target, plus the
  // content of a local policy schema. A remote policy's content is the referenced
  // contract's, so it links there instead.
  let { items = [] } = $props();
</script>

<!-- The shared table rules: fixed columns so a long reference can no longer squeeze
     Name to one word per line or push Target off the card. In a narrow column
     (SPEC 24.2) every policy becomes a card instead. -->
<div class="cq-cards">
<table class="data-table">
  <colgroup><col style="width:26%" /><col style="width:11ch" /><col /><col style="width:22%" /></colgroup>
  <thead><tr><th scope="col">Name</th><th scope="col">Kind</th><th scope="col">Definition</th><th scope="col">Target</th></tr></thead>
  <tbody>
    {#each items as p, pi (`${p.name}:${pi}`)}
      <tr>
        <td data-label="Name" class="pl-wrap">{p.name}</td>
        <td data-label="Kind">{p.ref ? 'Remote' : 'Local'}</td>
        <td data-label="Definition"><div class="pl-def">
          {#if p.ref}
            <span class="pl-note">Defined by the referenced contract</span>
            <ContractReference value={p.ref} resolution={p.resolution} />
          {:else}
            <span class="pl-path">{p.schema || '—'}</span>
          {/if}
          <SchemaContent content={p.content} />
        </div></td>
        <td data-label="Target" class="pl-wrap">{p.target || '—'}</td>
      </tr>
    {/each}
  </tbody>
</table>
</div>

<style>
  .pl-wrap { overflow-wrap: anywhere; }
  .pl-def { display: flex; flex-direction: column; gap: var(--sp-1); min-width: 0; }
  .pl-path { font-family: var(--font-mono, monospace); color: var(--c-text); overflow-wrap: anywhere; }
  .pl-note { font-size: var(--text-xs); color: var(--c-text-3); }
</style>
