<script>
  import HelpTip from './HelpTip.svelte';

  // The one provenance mark: a discreet "i" beside a value whose tip says where Pacto
  // read it. It knows no provenance vocabulary, so callers compose the words: `label` is
  // the accessible name, `text` the tip. Never a chip on every field. `start` is for a
  // mark that opens its row: its tip grows rightward from the mark's left edge.
  let { label = '', text = '', start = false } = $props();
</script>

<span class="prov-mark" class:prov-start={start}><HelpTip glyph="i" ariaLabel={label} {text} textClass="prov-tip" /></span>

<style>
  .prov-mark { display: inline-flex; flex-shrink: 0; }
  /* A `from` path is one long token; the tip breaks it anywhere instead of overflowing. */
  .prov-mark :global(.prov-tip) { overflow-wrap: anywhere; }
  /* Every mark ends its line, so the tip grows leftward from the button's right edge
     and stays inside a narrow viewport (the page clips horizontal overflow). */
  .prov-mark :global(.help-body) { left: auto; right: 0; max-width: min(38ch, calc(100vw - 2 * var(--sp-3))); }
  /* A mark that opens its row keeps the tip where HelpTip puts it: from its left edge,
     narrow enough to leave room for the row indent. */
  .prov-mark.prov-start :global(.help-body) { left: 0; right: auto; max-width: min(38ch, 78vw); }
  /* --touch-min is 40px; a coarse pointer gets the full 44px. */
  @media (pointer: coarse) {
    .prov-mark :global(.help-btn) { width: 44px; height: 44px; margin: calc((44px - 18px) / -2); }
  }
</style>
