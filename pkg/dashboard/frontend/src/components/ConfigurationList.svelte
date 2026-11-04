<script>
  import { formatCount } from '../lib/format.ts';
  import IdentityBadge from './IdentityBadge.svelte';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import ContractReference from './ContractReference.svelte';
  import SchemaContent from './SchemaContent.svelte';

  // A revision's configuration scopes: whether each is required, its schema path or
  // the contract it references, the content of a local schema and the bounded values.
  let { items = [] } = $props();
</script>

<ul class="cl">
  {#each items as c, ci (`${c.name}:${ci}`)}
    <li class="inset-card">
      <div class="cl-head">
        <span class="cl-name">{c.name || 'default'}</span>
        <IdentityBadge label={c.required ? 'required' : 'optional'} tone={c.required ? 'warn' : 'neutral'} />
      </div>
      {#if c.schema}<div class="cl-ref"><span class="cl-k">Schema</span><CopyableIdentifier value={c.schema} /></div>{/if}
      {#if c.ref}
        <div class="cl-ref"><span class="cl-k">Reference</span><ContractReference value={c.ref} resolution={c.resolution} /></div>
        <p class="cl-note">Defined by the referenced contract</p>
      {/if}
      <SchemaContent content={c.content} />
      {#if (c.values?.count ?? 0) > 0}
        <!-- The scope, whether it is required and the reference it resolves to are what
             a reader scans for; the value table is what they open when this is the
             scope they came for. -->
        <details class="cl-ops disclosure">
          <summary>
            <span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span>
            {formatCount(c.values.count)}{c.values.truncated && typeof c.values.total === 'number' ? ` of ${formatCount(c.values.total)}` : ''} {c.values.count === 1 && !c.values.truncated ? 'value' : 'values'}
          </summary>
          <table class="kv-table kv-table--mono">
            <thead><tr><th scope="col">Key</th><th scope="col">Value</th></tr></thead>
            <tbody>
              {#each c.values.items as v, vi (`${v.key}:${vi}`)}<tr><td>{v.key}</td><td>{v.value}</td></tr>{/each}
            </tbody>
          </table>
          {#if c.values.truncated}<p class="cl-note">Showing {formatCount(c.values.count)} values{typeof c.values.total === 'number' ? ` of ${formatCount(c.values.total)}` : '; total unknown'}.</p>{/if}
        </details>
      {/if}
    </li>
  {/each}
</ul>

<style>
  .cl { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-3); }
  .cl-head { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .cl-name { font-weight: 600; color: var(--c-text); overflow-wrap: anywhere; }
  .cl-ref { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .cl-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .cl-note { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .cl-ops > summary { color: var(--c-text-3); }
</style>
