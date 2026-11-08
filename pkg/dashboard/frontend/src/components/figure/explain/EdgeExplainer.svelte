<script>
  // "How does an edge read in the graph?" (SPEC 17, r4): three consumers wired into one
  // service, one per dependency-edge style, so the legend can be read against drawn edges before
  // the canvas. An explainer: neutral and accent only, no data from the fleet. Static.
  import Figure from '../Figure.svelte';
  import Node from '../Node.svelte';
  import HubNode from '../HubNode.svelte';
  import { edgeModel } from '../../../lib/explainers.ts';

  const m = edgeModel();
</script>

<Figure id="edges" kind="explain" testid="edge-explainer" question="How does an edge read in the graph?" alt={m.alt} wires={m.wires}>
  {#snippet stage()}
    <div class="evfig">
      <ul class="evfig-s" aria-label="Edge styles">
        {#each m.rows as r (r.style)}
          <li>
            <span class="evfig-t"><b>{r.label}</b><span class="t-meta">{r.sub}</span></span>
            <Node shape="square" tone="neutral" a={r.a} m={r.a} />
          </li>
        {/each}
      </ul>
      <div class="evfig-h"><HubNode kind="service" a="gh" /><span class="evfig-hl"><b>A service</b><span class="t-meta">and three consumers</span></span></div>
    </div>
  {/snippet}
  {#snippet caption()}This explains how a dependency edge reads; it shows no data from your fleet. On the canvas below, color adds the reconciliation state and a bar at the arrow marks a refused guarantee. Runs and configuration reference links are dashed too.{/snippet}
</Figure>
