<script>
  import Fold from './Fold.svelte';
  import FieldProvenanceRow from './FieldProvenanceRow.svelte';
  import ProvenanceMark from './ProvenanceMark.svelte';
  import WorkloadFacts from './WorkloadFacts.svelte';
  import { fieldLabel, fieldProvenanceText, replacementLine } from '../lib/fieldProvenance.ts';
  import { formatCount } from '../lib/format.ts';
  import { completenessText, COMPLETENESS_STATE } from '../lib/entityLabels.ts';
  import { MISSING_SECTIONS } from '../lib/fleetMap/modes.ts';

  // Where each value of a declared service comes from (fleet.FieldsPreview), folded by
  // default: one row per field, essentials first in the server's order, one discreet
  // mark per row whose tip says declared, accepted (and by whom), attested or inferred,
  // with source, asOf and completeness. Inferred rows are muted and one checkbox hides
  // them; never a second list. Under the rows, the inferred services it replaced: the
  // newest one Pacto read a workload for shows that workload's facts, every other one is
  // a single line, so a service that replaced hundreds never stacks hundreds of blocks;
  // replacesTotal counts them all, so what the bound left out reads "N more".
  //
  // First, how complete the contract is (509-S12, fleet.ServiceCompleteness): one row per
  // checklist section that applies, one mark whose tip says declared, inferred only,
  // missing or unknown and why, and for a gap the record can fill, the YAML to declare it.
  let { fields = null, replaces = [], replacesTotal = 0, replacesHidden = 0, asOf = '', completeness = null } = $props();
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
  const sections = $derived((completeness?.sections ?? []).filter((x) => x.state !== 'not-applicable'));
  const sectionLabel = (k) => { const w = MISSING_SECTIONS[k] ?? k; return w[0].toUpperCase() + w.slice(1); };
  // "72% complete · 3 declared, 1 missing": the percentage only when Pacto can judge one.
  const completeMeta = $derived.by(() => {
    if (!completeness) return '';
    const terms = ['declared', 'inferred', 'missing', 'unknown'].map((st) => [st, sections.filter((x) => x.state === st).length])
      .filter(([, n]) => n).map(([st, n]) => `${formatCount(n)} ${COMPLETENESS_STATE[st].toLowerCase()}`);
    const pct = completeness.percentComplete == null ? 'Completeness unknown' : `${completeness.percentComplete}% complete`;
    return terms.length ? `${pct} · ${terms.join(', ')}` : pct;
  });
  const foldMeta = $derived([completeMeta, meta].filter(Boolean).join(' · '));
  let copied = $state('');
  async function copy(section, yaml) {
    try { await navigator.clipboard?.writeText(yaml); copied = section; } catch { copied = ''; }
  }
  const list = $derived(replaces ?? []);
  const facts = $derived(list.find((r) => r.workload));
</script>

<Fold title="Where the values come from" meta={foldMeta} testid="field-provenance-fold">
  <div class="fpf">
    {#if sections.length}
      <dl class="fpf-rows" data-testid="contract-completeness">
        {#each sections as x (x.section)}
          <div class="fpf-row" class:fpf-inferred={x.state === 'inferred'} data-state={x.state}>
            <dt>{sectionLabel(x.section)}</dt>
            <dd>
              <span class="fpf-state">{COMPLETENESS_STATE[x.state] ?? x.state}</span>
              <ProvenanceMark label={`How complete ${sectionLabel(x.section).toLowerCase()} is`} text={completenessText(x)} />
              {#if x.declare && x.state !== 'declared'}
                <details class="disclosure fpf-yaml" data-testid="completeness-declare">
                  <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>YAML to declare it</summary>
                  <pre><code>{x.declare}</code></pre>
                  <button type="button" class="btn btn-sm" onclick={() => copy(x.section, x.declare)}>{copied === x.section ? 'Copied' : 'Copy YAML'}</button>
                </details>
              {/if}
            </dd>
          </div>
        {/each}
      </dl>
    {/if}
    {#if truncated}<p class="t-meta fpf-p" data-nofilter>Showing {formatCount(count)} of {formatCount(total)}</p>{/if}
    {#if anyInferred}
      <label class="fpf-filter"><input type="checkbox" bind:checked={showInferred} data-testid="field-provenance-show-inferred" /> Show inferred values</label>
    {/if}
    {#if items.length}
      <dl class="fpf-rows" data-testid="field-provenance">
        {#each shown as f, i (`${i}:${f.field}`)}
          <FieldProvenanceRow label={fieldLabel(f.field)} value={f.value} mark={fieldProvenanceText(f)} provenance={f.provenance} inferred={f.provenance === 'inferred'} />
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
  /* The completeness rows; field rows draw through FieldProvenanceRow. */
  .fpf-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px var(--sp-3); padding: var(--sp-1) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; }
  @container (min-width: 28rem) {
    .fpf-row { grid-template-columns: minmax(14ch, 1fr) minmax(16ch, 2fr); align-items: center; }
  }
  .fpf-row dt { color: var(--c-text-2); overflow-wrap: anywhere; min-inline-size: 0; }
  .fpf-row dd { margin: 0; display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2); min-inline-size: 0; }
  .fpf-state { color: var(--c-text-2); }
  .fpf-yaml { flex-basis: 100%; min-inline-size: 0; }
  .fpf-yaml pre { margin: var(--sp-1) 0; padding: var(--sp-2); overflow-x: auto; background: var(--c-surface-inset); border-radius: var(--radius-sm); font-size: var(--text-sm); }
  /* Inferred values are muted by colour, never by opacity. */
  .fpf-inferred, .fpf-inferred dt { color: var(--c-text-3); }
  @media (pointer: coarse) {
    .fpf-filter { min-block-size: 44px; }
    .fpf-filter input { inline-size: 24px; block-size: 24px; }
  }
</style>
