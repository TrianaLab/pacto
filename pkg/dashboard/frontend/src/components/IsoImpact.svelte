<script>
  // The impact of ONE change, drawn in the landing hero's isometric plane: the changed
  // service (later revision over earlier) on top, its direct consumers below with their
  // verdicts, and the lifted summary sheet with the classification stamp. An illustration
  // of the answer beside the table that carries it in full: role="img" with the whole
  // answer in its aria-label, no links, no focus stops. layoutImpact does all the work.
  //
  // Motion follows Ration: only err moves on its own. Err consumers rise (240ms), err
  // wires flow and the one blocking consumer rings for the finite alarm budget, and a
  // BREAKING stamp lands. Everything else is still, and reduced motion zeroes it all.
  import { layoutImpact, slab, ringPath } from '../lib/isoImpact.ts';
  import { formatCount } from '../lib/format.ts';

  /** @type {{ impact: import('../lib/api.ts').ProductImpact, onmore?: (() => void) | null }} */
  let { impact, onmore = null } = $props();
  let w = $state(0);
  // ponytail: two sizes only; 0 before the first measure counts as wide.
  const L = $derived(layoutImpact(impact, { maxDrawn: w && w < 900 ? 4 : 8 }));
  const S = $derived(L.sheet);
  const prev = $derived(slab(L.X0, L.Yn + 34, 74, 14));
  const next = $derived(slab(L.X0, L.Yn, 74, 14));
  // The paper's bottom edge, in diagram coordinates, where the two drops leave it.
  const by = (/** @type {number} */ lx) => S.y + S.h - 0.4472 * lx;
  const chipW = (/** @type {string} */ s) => Math.max(44, Math.round(Array.from(s).length * 7.4) + 16);
</script>

{#snippet slabG(/** @type {ReturnType<typeof slab>} */ p, /** @type {string} */ cls)}
  <g class="slab {cls}">
    <path class="side" d={p.left} />
    <path class="side dk" d={p.right} />
    <path class="top" d={p.top} />
    <path class="edge" d={p.edge} />
  </g>
{/snippet}

<figure class="iso-impact" data-testid="iso-impact" bind:clientWidth={w}>
  <svg viewBox="0 0 {L.W} {L.H}" role="img" aria-label={L.label} style:max-width="{L.W}px">
    {#each L.consumers as c (c.key)}
      <path class="wire tone-{c.tone}" class:flow={c.flow} data-motion={c.flow ? '' : undefined} d="M{L.X0} {L.Yn + 48} L{c.cx} {c.cy + c.t}" />
    {/each}
    {#if L.placeholder}
      {@const p = L.placeholder}
      <path class="wire tone-neutral" d="M{L.X0} {L.Yn + 48} L{p.cx} {p.cy + p.t}" />
    {/if}
    {#each L.consumers as c (c.key)}
      {#if c.ring}<path class="ring" data-motion="" d={ringPath(c.cx, c.cy, c.hw)} />{/if}
    {/each}
    {#each L.consumers as c (c.key)}
      <g class="c tone-{c.tone}" class:rise={c.rise >= 0} data-motion={c.rise >= 0 ? '' : undefined} style:--i={c.rise >= 0 ? c.rise : undefined} data-verdict={c.verdict}>
        <title>{c.title}</title>
        {@render slabG(slab(c.cx, c.cy, c.hw, c.t), '')}
        <text class="glyph" x={c.cx} y={c.cy + 6} text-anchor="middle">{c.glyph}</text>
        <text class="em lbl" x={c.cx} y={c.y0} text-anchor="middle">{c.name}</text>
        <text class="verdict lbl" x={c.cx} y={c.y0 + 18} text-anchor="middle">{c.verdict}</text>
        <g class="chip" class:absent={c.rangeAbsent}>
          <rect x={c.cx - chipW(c.range) / 2} y={c.y0 + 28} width={chipW(c.range)} height="20" rx="10" />
          <text x={c.cx} y={c.y0 + 42} text-anchor="middle">{c.range}</text>
        </g>
        <text class="lbl" x={c.cx} y={c.y0 + 64} text-anchor="middle">{c.owner}</text>
      </g>
    {/each}
    {#if L.placeholder}
      {@const p = L.placeholder}
      <g class="c tone-neutral" data-testid="iso-impact-placeholder">
        {@render slabG(slab(p.cx, p.cy, p.hw, p.t), '')}
        <text class="em lbl" x={p.cx} y={p.y0} text-anchor="middle">{p.label}</text>
      </g>
    {/if}
    {@render slabG(prev, 'prev')}
    {@render slabG(next, 'next')}
    <path class="glow" d={slab(L.X0, L.Yn, 44, 0).top} />
    <text class="face" transform="matrix(.8944 .4472 -.8944 .4472 {L.X0} {L.Yn})" text-anchor="middle" y="5">{L.leaders.face}</text>
    <path class="drop" d="M{L.X0 - 51.9} {by(60)} V{L.Yn + 2} M{L.X0 + 44.7} {by(168)} V{L.Yn - 4}" />
    <circle class="dot" cx={L.X0 - 51.9} cy={L.Yn + 2} r="2.5" />
    <circle class="dot" cx={L.X0 + 44.7} cy={L.Yn - 4} r="2.5" />
    <circle class="dot" cx={L.X0 - 74} cy={L.Yn} r="3.5" />
    <path class="ldr" d="M{L.X0 - 79} {L.Yn} H{L.X0 - 104}" />
    <text class="em lbl" x={L.X0 - 110} y={L.Yn - 3} text-anchor="end">{L.leaders.next}</text>
    <text class="lbl" x={L.X0 - 110} y={L.Yn + 13} text-anchor="end">later revision</text>
    <circle class="dot dim" cx={L.X0 - 74} cy={L.Yn + 34} r="3.5" />
    <path class="ldr" d="M{L.X0 - 79} {L.Yn + 34} H{L.X0 - 104}" />
    <text class="em lbl" x={L.X0 - 110} y={L.Yn + 38} text-anchor="end">{L.leaders.prev}</text>
    <text class="lbl" x={L.X0 - 110} y={L.Yn + 54} text-anchor="end">earlier revision</text>
    <g class="lift">
      <g transform="matrix(.8944 -.4472 0 1 {S.x} {S.y})">
        <path class="paper" d="M0 0 H212 L236 24 V{S.h} H0 Z" />
        <path class="fold" d="M212 0 V24 H236" />
        <text class="sk" x="18" y="26">change analysis</text>
        <text class="st" x="18" y="50"><title>{S.fullTitle}</title>{S.title}</text>
        <path class="rule" d="M18 60 H218 M18 64 H218" />
        <text x="18" y="86">change</text>
        <text class="v" x="120" y="86"><title>{S.fullChange}</title>{S.change}</text>
        <text x="18" y="106">consumers</text>
        <text class="v" x="120" y="106">{S.consumers}</text>
        <text x="18" y="126">release gate</text>
        <text class="v tone-{S.gate.tone}" x="120" y="126">{S.gate.word}</text>
        <text x="18" y="146">evidence</text>
        <text class="v tone-{S.evidence.tone}" x="120" y="146">{S.evidence.word}</text>
        <g class="stamp tone-{S.stamp.tone}" transform="rotate(-7 {S.stamp.x + S.stamp.w / 2} 173)">
          <g class="stamp-in" class:land={S.stamp.land} data-motion={S.stamp.land ? '' : undefined}>
            <rect x={S.stamp.x} y="158" width={S.stamp.w} height="30" rx="2" />
            <rect class="in" x={S.stamp.x + 4} y="162" width={S.stamp.w - 8} height="22" rx="1" />
            <text x={S.stamp.x + S.stamp.w / 2} y="178" text-anchor="middle" style:font-size="{S.stamp.fs}px">{S.stamp.text}</text>
          </g>
        </g>
      </g>
    </g>
  </svg>
  {#if L.more && onmore}
    <button type="button" class="iso-more" data-testid="iso-impact-more" onclick={onmore}>
      {formatCount(L.more)} more in Affected consumers
    </button>
  {/if}
  {#if L.hidden}
    <p class="iso-hidden t-meta" data-testid="iso-impact-hidden">{formatCount(L.hidden)} {L.hidden === 1 ? 'consumer' : 'consumers'} you may not see</p>
  {/if}
</figure>

<style>
  .iso-impact { margin: var(--sp-3) 0 var(--sp-4); }
  .iso-impact svg { display: block; width: 100%; height: auto; margin: 0 auto; overflow: visible; }
  .iso-more { display: block; margin: var(--sp-2) auto 0; min-height: 40px; padding: 0 var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-accent); font: inherit; font-size: var(--text-sm); cursor: pointer; }
  .iso-hidden { margin: var(--sp-2) 0 0; text-align: center; color: var(--c-text-3); }
  /* Below the breakpoint where the table turns into cards the drawing is unreadable, and
     the answer line and the table already say everything it says. */
  @media (max-width: 768px) { .iso-impact { display: none; } }

  /* Every label outside the sheet sits on a halo of the page ground, so a wire or the
     sheet's shadow passing behind it never lowers its contrast. */
  .lbl { paint-order: stroke; stroke: var(--c-bg); stroke-width: 4px; stroke-linejoin: round; }
  :is(.prev, .next) { filter: drop-shadow(0 12px 14px var(--c-iso-shadow)); }

  :is(.prev, .next) { --tone: var(--c-accent); }
  .tone-err { --tone: var(--c-err); }
  .tone-warn { --tone: var(--c-warn); }
  .tone-ok { --tone: var(--c-ok); }
  .tone-neutral { --tone: var(--c-neutral); }
  .top { fill: var(--c-iso-face); }
  .side { fill: color-mix(in srgb, var(--tone) 14%, var(--c-iso-side)); }
  .side.dk { fill: color-mix(in srgb, var(--tone) 14%, var(--c-iso-side-2)); }
  .edge { fill: none; stroke: var(--tone); stroke-width: 2; stroke-linejoin: round; }
  /* Unknown is dashed: colour is never the only channel. */
  :is(.prev, .tone-neutral) .edge { stroke-dasharray: 4 4; }
  .prev .edge { opacity: 0.6; }
  .glow { fill: var(--c-accent); opacity: 0.2; filter: blur(8px); }
  text { fill: var(--c-text-3); font: 500 var(--text-xs) var(--font-mono); }
  .face { fill: var(--c-text); font: 700 var(--text-base) var(--font-display); }
  .glyph { fill: var(--tone); font: 700 var(--text-lg) var(--font-display); }
  .em { fill: var(--c-text); font-weight: 600; }
  .verdict { fill: var(--tone); font-weight: 700; font-size: var(--text-sm); }
  .tone-neutral .verdict { fill: var(--c-text-3); }
  .wire { fill: none; stroke: var(--tone); stroke-width: 2; }
  .wire.tone-err { stroke-dasharray: 5 6; }
  .wire.tone-neutral { stroke-dasharray: 2 5; }
  .chip rect { fill: var(--c-bg); stroke: var(--tone); }
  .tone-neutral .chip rect, .chip.absent rect { stroke-dasharray: 3 3; }
  .chip text { fill: var(--tone); }
  .tone-neutral .chip text { fill: var(--c-text-3); }
  :is(.ldr, .drop) { fill: none; stroke: var(--c-text-3); stroke-dasharray: 3 4; opacity: 0.7; }
  .drop { stroke: var(--c-accent); }
  .dot { fill: var(--c-accent); }
  .dot.dim { opacity: 0.6; }
  .ring { fill: none; stroke: var(--c-err); stroke-width: 1.5; opacity: 0.5; transform-box: fill-box; transform-origin: center; }
  .lift { filter: drop-shadow(0 18px 22px var(--c-iso-shadow)); }
  .paper { fill: var(--c-surface); stroke: color-mix(in srgb, var(--c-accent) 35%, var(--c-border)); }
  .fold { fill: var(--c-surface-hover); stroke: color-mix(in srgb, var(--c-accent) 35%, var(--c-border)); }
  .rule { stroke: var(--c-border); }
  .st { fill: var(--c-text); font: 700 var(--text-lg) var(--font-display); }
  .sk { text-transform: uppercase; letter-spacing: 0.08em; }
  .v { fill: var(--c-text); }
  .v.tone-err { fill: var(--c-err); }
  .v.tone-warn { fill: var(--c-warn); }
  .v.tone-ok { fill: var(--c-ok); }
  .v.tone-neutral { fill: var(--c-text-3); }
  .stamp rect { fill: color-mix(in srgb, var(--c-surface) 85%, transparent); stroke: var(--tone); stroke-width: 2.2; }
  .stamp .in { fill: none; stroke-width: 1; }
  .stamp text { fill: var(--tone); font-weight: 700; letter-spacing: 0.1em; }

  /* Motion, mapped to Ration: err only, finite, transform and dash offset only (contrast
     never dips mid-flight), tokens only. Reduced motion zeroes the tokens. */
  .c.rise { animation: iso-rise var(--motion-reveal) backwards; animation-delay: calc(min(var(--i), var(--stagger-max)) * var(--stagger-step)); }
  .stamp-in.land { transform-box: fill-box; transform-origin: center; animation: iso-land var(--motion-reveal) backwards; }
  .wire.flow { animation: iso-flow var(--motion-alarm-period) linear var(--motion-alarm-count); }
  .ring { animation: iso-ring var(--motion-alarm-period) var(--ease-in-place) var(--motion-alarm-count); }
  @keyframes iso-rise { from { transform: translateY(var(--motion-rise)); } }
  @keyframes iso-land { from { transform: scale(1.12); } }
  @keyframes iso-flow { to { stroke-dashoffset: -22; } }
  @keyframes iso-ring { from { opacity: 0.9; transform: none; } 70%, to { opacity: 0; transform: scale(1.35); } }
</style>
