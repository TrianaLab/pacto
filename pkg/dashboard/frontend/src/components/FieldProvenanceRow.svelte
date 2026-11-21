<script>
  import ProvenanceMark from './ProvenanceMark.svelte';

  // One field row with its one provenance mark: the label, the value and a discreet
  // mark whose tip says where the value comes from. FieldProvenanceFold and the
  // Declare panel draw their rows through it, so the two read alike. `control` goes
  // before the label (the Declare panel's accept checkbox); inferred rows are muted.
  // Rows sit inside a <dl> whose container query lays them out (see .fpf-row).
  let { label = '', value = '', mark = '', provenance = '', inferred = false, control = undefined } = $props();
</script>

<div class="fpf-row" class:fpf-inferred={inferred} data-provenance={provenance || undefined}>
  <!-- With a control the label wraps it, so the whole label toggles it (a touch target wider than the box). -->
  <dt>{#if control}<label class="fpf-control">{@render control()}<bdi title={label}>{label}</bdi></label>{:else}<bdi title={label}>{label}</bdi>{/if}</dt>
  <dd><bdi class="fpf-value" title={value}>{value}</bdi> <ProvenanceMark label={`Where ${label} comes from`} text={mark} /></dd>
</div>

<style>
  /* Label above value on a narrow container, two columns once it has room; the
     parent sets container-type. */
  .fpf-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px var(--sp-3); padding: var(--sp-1) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; }
  @container (min-width: 28rem) {
    .fpf-row { grid-template-columns: minmax(14ch, 1fr) minmax(16ch, 2fr); align-items: center; }
  }
  .fpf-row dt { display: flex; align-items: center; gap: var(--sp-1); color: var(--c-text-2); overflow-wrap: anywhere; min-inline-size: 0; }
  .fpf-row dd { margin: 0; display: flex; align-items: center; gap: var(--sp-2); min-inline-size: 0; }
  .fpf-control { display: flex; align-items: center; gap: var(--sp-1); min-inline-size: 0; cursor: pointer; }
  .fpf-value { font-family: var(--font-mono); font-size: 0.95em; overflow-wrap: anywhere; min-inline-size: 0; }
  /* Inferred values are muted by colour, never by opacity. */
  .fpf-inferred, .fpf-inferred dt { color: var(--c-text-3); }
</style>
