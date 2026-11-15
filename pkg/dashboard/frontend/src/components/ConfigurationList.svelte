<script>
  import { formatCount } from '../lib/format.ts';
  import IdentityBadge from './IdentityBadge.svelte';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import ContractReference from './ContractReference.svelte';
  import SchemaContent from './SchemaContent.svelte';
  import { scrollToSection } from '../lib/motion.ts';

  // A revision's configuration scopes: whether each is required, its schema path or
  // the contract it references (and the revision and configuration a ref resolves
  // to), the content of its schema and the bounded values. An entry in `resourceEntries`
  // is a request the service page's Resources section already shows in full, so here it
  // keeps only its name and a jump there: counted once, never shown twice.
  /** @type {{ items?: any[], resourceEntries?: Set<string> }} */
  let { items = [], resourceEntries = new Set() } = $props();
</script>

<ul class="cl">
  {#each items as c, ci (`${c.name}:${ci}`)}
    <li class="inset-card">
      {#if resourceEntries.has(c.name)}
      <div class="cl-head" data-testid="configuration-resource">
        <span class="cl-name">{c.name || 'default'}</span>
        <span class="cl-note">Requested from {c.resolution?.service?.label ?? 'its provider'}, see</span>
        <button type="button" class="cl-jump" onclick={() => scrollToSection('sec-resources')}>Resources</button>
      </div>
      {:else}
      <div class="cl-head">
        <span class="cl-name">{c.name || 'default'}</span>
        <IdentityBadge label={c.required ? 'required' : 'optional'} tone={c.required ? 'warn' : 'neutral'} />
      </div>
      {#if c.schema}<div class="cl-ref"><span class="cl-k">Schema</span><CopyableIdentifier value={c.schema} /></div>{/if}
      {#if c.ref}
        <div class="cl-ref"><span class="cl-k">Reference</span><ContractReference value={c.ref} resolution={c.resolution} /></div>
        <p class="cl-note">{#if c.resolution?.revision}From {c.resolution.revision.version ? `${c.resolution.service?.label}@${c.resolution.revision.version}` : c.resolution.revision.label}{c.configuration ? `, configuration ${c.configuration}` : ''}{:else}Defined by the referenced contract{c.configuration ? `, configuration ${c.configuration}` : ''}{/if}</p>
      {/if}
      <SchemaContent content={c.content} />
      {#if (c.values?.count ?? 0) > 0}
        <!-- The scope, whether it is required and the reference it resolves to are what
             a reader scans for; the value table is what they open when this is the
             scope they came for. -->
        <details class="cl-ops disclosure">
          <summary>
            <span class="disclosure-caret" data-motion aria-hidden="true"></span>
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
  .cl-jump { padding: 0; border: none; background: none; color: var(--c-accent); font: inherit; font-size: var(--text-sm); cursor: pointer; text-decoration: underline; }
  @media (pointer: coarse) { .cl-jump { min-block-size: 44px; } }
  .cl-ops > summary { color: var(--c-text-3); }
</style>
