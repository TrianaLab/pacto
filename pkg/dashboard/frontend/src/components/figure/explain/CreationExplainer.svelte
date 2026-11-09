<script>
  // "What does creating a service involve?" (SPEC 7, golden paths): the mechanism of
  // creating a service from a golden path, drawn as a rail of five stations. An
  // explainer: neutral and accent only, no states and no data from the fleet. On a golden
  // path page the last station says what the path requests (`requires`); on the list it
  // stays general (null). Its walk plays only on Replay (A1b).
  import Figure from '../Figure.svelte';
  import Rail from '../Rail.svelte';
  import { creationExplainModel } from '../../../lib/goldenFigures.ts';

  /** @type {{ requires?: string[] | null, hidden?: number }} */
  let { requires = null, hidden = 0 } = $props();
  const m = $derived(creationExplainModel(requires, hidden));
</script>

<Figure id="creation-explain" kind="explain" testid="creation-explainer" question="What does creating a service involve?" alt={m.alt} wires={m.wires} steps={m.steps} walk
  rowMin={m.stations.length * 150}>
  {#snippet stage()}<Rail stations={m.stations} />{/snippet}
  {#snippet caption()}This explains the mechanism with pacto init --from or pacto_create. It shows no data from your fleet.{/snippet}
</Figure>
