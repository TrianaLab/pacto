<script>
  // Wire (SPEC 3, 2.1): a path between two anchors in its section 2.1 style (observed
  // solid, projected dashed, stale dotted, missing broken with a ✕ at 35% of its length),
  // with an arrowhead when it means "moves to" (C13b) and the path a narration's one-beat
  // signal runs along. Figure places it; it carries no words.
  /** @type {{ w: import('../../lib/figureLayout.ts').DrawnWire }} */
  let { w } = $props();
</script>

<!-- An unplaced wire keeps its paths (a narration may target them) with display none. -->
<g class="wire t-{w.tone}">
  <path class="w mk w--{w.style}" data-wid={w.id} d={w.d} style:display={w.off ? 'none' : null} />
  {#if w.mark && !w.off}<path class="w-x mk" data-wid={w.id} d={w.mark} />{/if}
  {#if w.head && !w.off}<path class="w-head mk" data-wid={w.id} d={w.head} />{/if}
  <path class="sig" data-wid={w.id} d={w.d} style:display={w.off ? 'none' : null} />
</g>
