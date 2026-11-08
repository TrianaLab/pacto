<script>
  // "How does an edge read in the graph?" (SPEC 17, r4): four consumers wired into one
  // service, one per wire style, so the legend can be read against drawn edges before
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
      <div class="evfig-h"><HubNode kind="service" a="gh" /><span class="evfig-hl"><b>A service</b><span class="t-meta">and four consumers</span></span></div>
    </div>
  {/snippet}
  {#snippet caption()}This explains the edge styles; it shows no data from your fleet. The canvas below draws the whole graph with the same styles.{/snippet}
</Figure>
