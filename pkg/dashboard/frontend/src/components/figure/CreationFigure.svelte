<script>
  // The creation checklist of a service created from a golden path, as a Rail (SPEC 7,
  // P6): one station per step, the plugin steps folded into one, so it keeps at most 8.
  // A step is done only on evidence the fleet holds; Unknown is the grey hatch and never
  // reads as done. The list under the figure holds every step and its detail (parity).
  // Static: the checklist is derived now, so no step is a recorded transition (A6).
  import Figure from './Figure.svelte';
  import Rail from './Rail.svelte';
  import { creationRailModel } from '../../lib/goldenFigures.ts';

  /** @type {{ steps: import('../../lib/goldenFigures.ts').CreationStep[] }} */
  let { steps } = $props();
  const m = $derived(creationRailModel(steps));
</script>

<Figure id="creation" testid="creation-figure" question="Which creation steps does Pacto see evidence for?" alt={m.alt} wires={m.wires} rowMin={m.stations.length * 150}>
  {#snippet stage()}<Rail stations={m.stations} />{/snippet}
  {#snippet caption()}{m.alt} Derived from what Pacto observes now; Unknown means no evidence, never done.{/snippet}
</Figure>
