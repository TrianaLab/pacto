<script>
  import HelpTip from './HelpTip.svelte';

  // The one provenance mark: a discreet "i" beside a value whose tip says where Pacto
  // read it. It knows no provenance vocabulary, so callers compose the words: `label` is
  // the accessible name, `text` the tip. Never a chip on every field. `start` is for a
  // mark that opens its row: its tip grows rightward from the mark's left edge.
  let { label = '', text = '', start = false } = $props();

  // A mark inside wrapped text may end a short line near the left edge, where a tip
  // growing leftward would be clipped. So as it opens (hover, focus or a tap) the tip
  // takes the side with more room and is never wider than that room.
  let right = $state(false), room = $state(0);
  function place(e) {
    const r = e.currentTarget.getBoundingClientRect(), toRight = innerWidth - r.left - 12, toLeft = r.right - 12;
    right = !start && toRight > toLeft;
    room = Math.max(toRight, toLeft);
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<span class="prov-mark" class:prov-start={start} class:prov-right={right} style:--prov-room={room ? `${room}px` : null} onpointerenter={place} onfocusin={place}><HelpTip glyph="i" ariaLabel={label} {text} textClass="prov-tip" /></span>

<style>
  .prov-mark { display: inline-flex; flex-shrink: 0; }
  /* A `from` path is one long token; the tip breaks it anywhere instead of overflowing. */
  .prov-mark :global(.prov-tip) { overflow-wrap: anywhere; }
  /* By default the tip grows leftward from the button's right edge and stays inside a
     narrow viewport (the page clips horizontal overflow); on opening it takes the side
     with more room (prov-right) and at most that room (--prov-room). */
  .prov-mark :global(.help-body) { left: auto; right: 0; max-width: min(38ch, calc(100vw - 2 * var(--sp-3)), var(--prov-room, 100vw)); }
  .prov-mark.prov-right :global(.help-body) { left: 0; right: auto; }
  /* A mark that opens its row keeps the tip where HelpTip puts it: from its left edge,
     narrow enough to leave room for the row indent. */
  .prov-mark.prov-start :global(.help-body) { left: 0; right: auto; max-width: min(38ch, 78vw); }
  /* --touch-min is 40px; a coarse pointer gets the full 44px. */
  @media (pointer: coarse) {
    .prov-mark :global(.help-btn) { width: 44px; height: 44px; margin: calc((44px - 18px) / -2); }
  }
</style>
