<script>
  // Plinth (SPEC 3, iso): where a target runs. `count` slabs (one per thing running
  // there) on a wider base, so depth is a count, never decoration (G4). No words on a
  // face (C1); it anchors on the top slab's right corner (`a`).
  import { plinthGeometry } from '../../lib/figure.ts';

  /** @type {{ count: number, tone?: 'accent' | 'neutral', a: string, m?: string }} */
  let { count, tone = 'accent', a, m = '' } = $props();
  const g = $derived(plinthGeometry(count));
</script>

<svg class="stack plinth" viewBox="0 0 {g.W} {g.H}" aria-hidden="true" focusable="false" style:max-width="{g.W}px">
  <g class="slab mk t-neutral" data-m={m ? `${m}-base` : undefined}>
    <path class="side" d={g.base.left} />
    <path class="side dk" d={g.base.right} />
    <path class="top" d={g.base.top} />
    <path class="edge" d={g.base.edge} />
  </g>
  {#each [...g.slabs].reverse() as s, i (i)}
    <g class="slab mk t-{tone}" data-m={m || undefined}>
      <path class="side" d={s.left} />
      <path class="side dk" d={s.right} />
      <path class="top" d={s.top} />
      <path class="edge" d={s.edge} />
    </g>
  {/each}
  <circle r="1" cx={g.anchor.x} cy={g.anchor.y} fill="none" data-a={a} />
</svg>
