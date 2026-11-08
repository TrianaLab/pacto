<script>
  // Stack (SPEC 3, iso): revisions stacked on a service, top first. Isometry here
  // because depth is the fact: publication order. 1 to 5 layers in a fixed-aspect cell
  // (it scales uniformly, so the 2:1 geometry never distorts); no words on a face (C1).
  // Each layer anchors on its right corner (`a`) and is a narration mark (`m`).
  import { stackGeometry } from '../../lib/figure.ts';

  /** @type {{ layers: { tone: string, dash?: boolean, a: string, m?: string }[] }} */
  let { layers } = $props();
  const g = $derived(stackGeometry(layers.length));
  // Painted bottom first, so the top layer sits over the ones under it.
  const order = $derived(g.layers.map((L, i) => ({ L, layer: layers[i] })).reverse());
</script>

<svg class="stack" viewBox="0 0 {g.W} {g.H}" aria-hidden="true" focusable="false" style:max-width="{g.W}px">
  {#each order as { L, layer } (layer.a)}
    <g class="slab mk t-{layer.tone}" class:dash={layer.dash} data-m={layer.m || undefined}>
      <path class="side" d={L.paths.left} />
      <path class="side dk" d={L.paths.right} />
      <path class="top" d={L.paths.top} />
      <path class="edge" d={L.paths.edge} />
    </g>
    <circle r="1" cx={L.anchor.x} cy={L.anchor.y} fill="none" data-a={layer.a} />
  {/each}
</svg>
