<script>
  // A flat pin (SPEC 3): circle for a state, square for an entity, diamond for a gate.
  // One glyph, drawn as geometry (C15); no words. Unknown is a grey hatched ring with a
  // solid neutral edge around a plain face, so the glyph never sits on the stripes. `a` names the anchor wires attach to; `m` names the mark a narration
  // reveals. A diamond also anchors on its vertices (`a-t`, `a-l`, `a-r`, `a-b`), so a
  // wire ends on the shape, never on its box (C13).
  import { GLYPH_PATH } from '../../lib/figure.ts';

  /** @type {{ tone?: string, hatch?: boolean, glyph?: keyof typeof GLYPH_PATH | '', shape?: 'circle' | 'square' | 'diamond', big?: boolean, a?: string, m?: string }} */
  let { tone = 'neutral', hatch = false, glyph = '', shape = 'circle', big = false, a = '', m = '' } = $props();
  const s = $derived(big ? 40 : 28);
  const c = $derived(s / 2);
  const r = $derived(big ? 13 : 9);
  const d = $derived(r + 3);
</script>

<span class="nd mk t-{tone}" class:unk={hatch} class:big data-a={a || undefined} data-m={m || undefined} aria-hidden="true">
  <svg width={s} height={s} viewBox="0 0 {s} {s}" focusable="false">
    {#if shape === 'diamond'}
      <path class="f" d="M{c} {c - d} L{c + d} {c} L{c} {c + d} L{c - d} {c} Z" />
    {:else if shape === 'square'}
      <rect class="f" x={c - r} y={c - r} width={2 * r} height={2 * r} rx="3" />
    {:else}
      <circle class="f" cx={c} cy={c} r={r} />
    {/if}
    {#if hatch}
      {#if shape === 'diamond'}
        <path class="fc" d="M{c} {c - d + 4} L{c + d - 4} {c} L{c} {c + d - 4} L{c - d + 4} {c} Z" />
      {:else if shape === 'square'}
        <rect class="fc" x={c - r + 3} y={c - r + 3} width={2 * r - 6} height={2 * r - 6} rx="2" />
      {:else}
        <circle class="fc" cx={c} cy={c} r={r - 3} />
      {/if}
    {/if}
    {#if glyph}<path class="gp" transform="translate({c} {c}) scale({big ? 1.2 : 0.95})" d={GLYPH_PATH[glyph]} />{/if}
    {#if shape === 'diamond' && a}
      <circle r="0.5" cx={c} cy={c - d} fill="none" data-a="{a}-t" />
      <circle r="0.5" cx={c - d} cy={c} fill="none" data-a="{a}-l" />
      <circle r="0.5" cx={c + d} cy={c} fill="none" data-a="{a}-r" />
      <circle r="0.5" cx={c} cy={c + d} fill="none" data-a="{a}-b" />
    {/if}
  </svg>
</span>
