<script>
  import { formatCount } from '../lib/format.ts';

  // The content of one policy or configuration JSON Schema, as read from the bundle
  // when the page was requested: its title, description and a bounded key/type/value
  // table. A schema Pacto could not read says why instead of reading as empty.
  let { content = null } = $props();
  const props = $derived(content?.properties ?? []);
</script>

{#if content?.unavailable}
  <p class="sc-note">Content unavailable: {content.unavailable}</p>
{:else if content}
  <details class="sc disclosure">
    <summary>
      <span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span>
      {formatCount(props.length)}{content.truncated ? ` of ${formatCount(content.total ?? 0)}` : ''} {props.length === 1 && !content.truncated ? 'property' : 'properties'}
    </summary>
    {#if content.title}<p class="sc-title">{content.title}</p>{/if}
    {#if content.description}<p class="sc-note">{content.description}</p>{/if}
    {#if props.length}
      <table class="kv-table kv-table--mono">
        <thead><tr><th scope="col">Key</th><th scope="col">Type</th><th scope="col">Value</th></tr></thead>
        <tbody>
          {#each props as p, i (`${p.key}:${i}`)}<tr><td>{p.key}</td><td>{p.type}</td><td>{p.value}</td></tr>{/each}
        </tbody>
      </table>
    {:else}
      <p class="sc-note">The schema declares no properties.</p>
    {/if}
    {#if content.truncated}<p class="sc-note">Showing {formatCount(props.length)} of {formatCount(content.total ?? 0)} properties.</p>{/if}
  </details>
{/if}

<style>
  .sc > summary { color: var(--c-text-3); }
  .sc-title { margin: var(--sp-1) 0 0; font-weight: 600; color: var(--c-text); overflow-wrap: anywhere; }
  .sc-note { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); overflow-wrap: anywhere; }
</style>
