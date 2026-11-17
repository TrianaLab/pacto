<script>
  import HelpTip from './HelpTip.svelte';

  // The one provenance mark: a discreet "i" beside a value whose tip says where Pacto
  // read it. It knows no provenance vocabulary, so callers compose the words: `label` is
  // the accessible name, `text` the tip. Never a chip on every field.
  let { label = '', text = '' } = $props();
</script>

<span class="prov-mark"><HelpTip glyph="i" ariaLabel={label} {text} textClass="prov-tip" /></span>

<style>
  .prov-mark { display: inline-flex; flex-shrink: 0; }
  /* A `from` path is one long token; the tip breaks it anywhere instead of overflowing. */
  .prov-mark :global(.prov-tip) { overflow-wrap: anywhere; }
  /* --touch-min is 40px; a coarse pointer gets the full 44px. */
  @media (pointer: coarse) {
    .prov-mark :global(.help-btn) { width: 44px; height: 44px; margin: calc((44px - 18px) / -2); }
  }
</style>
