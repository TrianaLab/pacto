<script>
  // "How does Pacto know?" (SPEC 13, P5): the mechanism behind every number on
  // Overview, drawn as a rail of four stations. An explainer: neutral and accent only,
  // no data from the fleet. The reader opens it, so its walk plays then (A2), once per
  // session; Replay plays it again.
  import Figure from '../Figure.svelte';
  import Rail from '../Rail.svelte';
  import { knowsModel } from '../../../lib/explainers.ts';

  const m = knowsModel();
</script>

<Figure id="knows" kind="explain" testid="knows-explainer" question="How does Pacto know?" alt={m.alt} wires={m.wires} steps={m.steps} walk
  rowMin={m.stations.length * 150} autoplay={true} autoKey="knows">
  {#snippet stage()}<Rail stations={m.stations} />{/snippet}
  {#snippet caption()}This explains the mechanism. It shows no data from your fleet.{/snippet}
</Figure>
