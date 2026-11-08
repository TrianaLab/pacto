<script>
  // The provenance strip (SPEC 3, Figure caption): one line under a fleet figure that
  // says where its numbers came from. The sources link to the Sources page, the
  // snapshot carries its time with its day and the completeness is a wrapping badge
  // (C10) that is never shown as complete when it is not.
  import Glyph from './Glyph.svelte';
  import { provenance } from '../../lib/provenance.ts';
  import { countOf } from '../../lib/format.ts';
  import { formatDate } from '../../lib/dateFormat.ts';
  import { fleetSourcesUrl } from '../../lib/router.ts';

  /** @type {{ meta: import('../../lib/provenance.ts').MetaLike | null | undefined }} */
  let { meta } = $props();
  const p = $derived(provenance(meta));
</script>

{#if meta}
  <span class="prov t-meta" data-testid="provenance">
    <Glyph kind="source" />
    <span>From <a href={fleetSourcesUrl()}>{countOf(p.sources, 'source')}</a>{#if p.asOf}, as of {formatDate(p.asOf)}{/if}.</span>
    <span class="badge wrap" class:t-unk={p.partial}>{p.text}</span>
  </span>
{/if}

<style>
  .prov { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-2); margin-top: var(--sp-2); }
  .prov a { text-decoration: underline; }
</style>
