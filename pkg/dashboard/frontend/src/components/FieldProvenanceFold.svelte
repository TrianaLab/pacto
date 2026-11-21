<script>
  import Fold from './Fold.svelte';
  import ProvenanceMark from './ProvenanceMark.svelte';
  import WorkloadFacts from './WorkloadFacts.svelte';
  import { fieldLabel, fieldProvenanceText, replacementLine } from '../lib/fieldProvenance.ts';
  import { formatCount } from '../lib/format.ts';

  // Where each value of a declared service comes from (fleet.FieldsPreview), folded by
  // default: one row per field, essentials first in the server's order, one discreet
  // mark per row whose tip says declared, accepted (and by whom), attested or inferred,
  // with source, asOf and completeness. Inferred rows are muted and one checkbox hides
  // them; never a second list. Under the rows, the inferred services it replaced: the
  // newest one Pacto read a workload for shows that workload's facts, every other one is
  // a single line, so a service that replaced hundreds never stacks hundreds of blocks;
  // replacesTotal counts them all, so what the bound left out reads "N more".
  let { fields = null, replaces = [], replacesTotal = 0, replacesHidden = 0, asOf = '' } = $props();
  let showInferred = $state(true);

  const items = $derived(fields?.items ?? []);
  const count = $derived(fields?.count ?? items.length);
  const total = $derived(fields?.total ?? items.length);
  const truncated = $derived(!!fields?.truncated);
  const hiddenFields = $derived(fields?.hidden ?? 0);
  const inferredCount = $derived(items.filter((f) => f.provenance === 'inferred').length);
  const anyInferred = $derived(inferredCount > 0);
  const shown = $derived(showInferred ? items : items.filter((f) => f.provenance !== 'inferred'));
  // The meta counts what the rows hold, by provenance, zero terms left out.
  const meta = $derived.by(() => {
    const n = (p) => items.filter((f) => f.provenance === p).length;
    const terms = ['declared', 'accepted', 'attested', 'inferred'].filter((p) => n(p)).map((p) => `${formatCount(n(p))} ${p}`);
    return terms.length ? `${terms.join(', ')}${truncated ? ` of the first ${formatCount(count)}` : ''}` : '';
  });
  const list = $derived(replaces ?? []);
  const facts = $derived(list.find((r) => r.workload));
</script>

<Fold title="Where the values come from" {meta} testid="field-provenance-fold">
  <div class="fpf">
    {#if truncated}<p class="t-meta fpf-p" data-nofilter>Showing {formatCount(count)} of {formatCount(total)}</p>{/if}
    {#if anyInferred}
      <label class="fpf-filter"><input type="checkbox" bind:checked={showInferred} data-testid="field-provenance-show-inferred" /> Show inferred values</label>
    {/if}
    {#if items.length}
      <dl class="fpf-rows" data-testid="field-provenance">
        {#each shown as f, i (`${i}:${f.field}`)}
          <div class="fpf-row" class:fpf-inferred={f.provenance === 'inferred'} data-provenance={f.provenance}>
            <dt><bdi title={fieldLabel(f.field)}>{fieldLabel(f.field)}</bdi></dt>
            <dd><bdi class="fpf-value" title={f.value}>{f.value}</bdi> <ProvenanceMark label={`Where ${fieldLabel(f.field)} comes from`} text={fieldProvenanceText(f)} /></dd>
          </div>
        {/each}
      </dl>
    {/if}
    {#if !showInferred && inferredCount}<p class="t-meta fpf-p" data-testid="field-provenance-inferred-hidden">{formatCount(inferredCount)} inferred {inferredCount === 1 ? 'value' : 'values'} hidden</p>{/if}
    {#if truncated && total > count}<p class="t-meta fpf-p" data-nofilter>{formatCount(total - count)} more</p>{/if}
    {#if hiddenFields}<p class="t-meta fpf-p" data-testid="field-provenance-hidden">{formatCount(hiddenFields)} {hiddenFields === 1 ? 'value' : 'values'} hidden by your permissions</p>{/if}
    {#each list as r, i (`${i}:${r.from}`)}
      <p class="t-body-2 fpf-p" data-testid="field-provenance-replacement">{replacementLine(r, asOf)}{#if r.link === 'inferred-from' && !r.workload}. Pacto read no workload for it here{/if}</p>
      {#if r === facts}<WorkloadFacts workload={r.workload} />{/if}
    {/each}
    {#if replacesTotal > list.length}<p class="t-meta fpf-p" data-nofilter data-testid="field-provenance-replaces-more">{formatCount(replacesTotal - list.length)} more</p>{/if}
    {#if replacesHidden}<p class="t-meta fpf-p" data-testid="field-provenance-replaces-hidden">Replaces {formatCount(replacesHidden)} inferred {replacesHidden === 1 ? 'service' : 'services'} whose workload you cannot see</p>{/if}
  </div>
</Fold>

<style>
  .fpf { container-type: inline-size; display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .fpf-p { margin: 0; overflow-wrap: anywhere; }
  .fpf-filter { display: inline-flex; align-items: center; gap: var(--sp-1); min-block-size: 32px; font-size: var(--text-sm); }
  .fpf-rows { margin: 0; display: flex; flex-direction: column; }
  /* Label above value on a narrow container, two columns once it has room. */
  .fpf-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px var(--sp-3); padding: var(--sp-1) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; }
  @container (min-width: 28rem) {
    .fpf-row { grid-template-columns: minmax(14ch, 1fr) minmax(16ch, 2fr); align-items: center; }
  }
  .fpf-row dt { color: var(--c-text-2); overflow-wrap: anywhere; min-inline-size: 0; }
  .fpf-row dd { margin: 0; display: flex; align-items: center; gap: var(--sp-2); min-inline-size: 0; }
  .fpf-value { font-family: var(--font-mono); font-size: 0.95em; overflow-wrap: anywhere; min-inline-size: 0; }
  /* Inferred values are muted by colour, never by opacity. */
  .fpf-inferred, .fpf-inferred dt { color: var(--c-text-3); }
  @media (pointer: coarse) {
    .fpf-filter { min-block-size: 44px; }
    .fpf-filter input { inline-size: 24px; block-size: 24px; }
  }
</style>
