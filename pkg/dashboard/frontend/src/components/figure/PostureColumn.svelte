<script>
  // PostureColumn (SPEC 3, r5): one flat stacked column, worst on top, height
  // proportional to share with a 5px floor. Every drawn stratum is wired to an HTML row
  // (swatch, label, count, share) that links to the list filtered to it; the column is
  // the only bar. An empty stratum draws nothing but keeps its row with 0. Its wires
  // come from postureColumn (lib/fleetFigures.ts), which the caller hands to Figure.
  import { postureColumn, POSTURE_W } from '../../lib/fleetFigures.ts';
  import { formatCount } from '../../lib/format.ts';
  import { sharePct } from '../../lib/figure.ts';

  /** @type {{ segments: import('../../lib/distributions.ts').Segment[], total: number }} */
  let { segments, total } = $props();
  const m = $derived(postureColumn(segments, total));
</script>

<div class="pfig">
  <svg class="pcol" data-a="pcol" width={POSTURE_W + 4} height={m.H} viewBox="0 0 {POSTURE_W + 4} {m.H}" aria-hidden="true" focusable="false">
    {#each m.strata as s (s.i)}
      <rect class="pc mk t-{s.tone}" class:unk={s.hatch} data-m="p{s.i}" x="2" y={s.y} width={POSTURE_W} height={s.h} rx="2" />
      <circle r="1" fill="none" cx={POSTURE_W + 2} cy={s.y + s.h / 2} data-a="pk{s.i}" />
    {/each}
    {#if !m.strata.length}<rect class="pc unk" x="2" y="2" width={POSTURE_W} height="6" rx="2" />{/if}
  </svg>
  <span></span>
  <ul class="prows">
    {#each segments as s, i (s.label)}
      <li class="prow" data-a="pl{i}">
        <span class="psw t-{s.tone}" class:unk={s.hatch} aria-hidden="true"></span>
        <span class="prow-l">{s.label}</span>
        {#if s.href && s.value}
          <a class="prow-n" href={s.href}><b>{formatCount(s.value)}</b> · {sharePct(s.value, total)}</a>
        {:else}
          <span class="prow-n" data-nofilter><b>{formatCount(s.value)}</b> · {sharePct(s.value, total)}</span>
        {/if}
      </li>
    {/each}
  </ul>
</div>
