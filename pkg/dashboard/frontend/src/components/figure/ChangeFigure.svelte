<script>
  // The Delivery Change figure (SPEC 7): "Why did the gate decide this, and where is the
  // change now?". Stations in causal order (lib/changeFigure.ts), wires solid up to where
  // the change got and dashed after, and a token resting where it is now. The walk covers
  // the stations it reached (A1b); it plays on its own only for an errored or held change.
  import Figure from './Figure.svelte';
  import Rail from './Rail.svelte';
  import { changeModel } from '../../lib/changeFigure.ts';

  /** @type {{ data: import('../../lib/api.ts').DeliveryOperation, namespace: string }} */
  let { data, namespace } = $props();
  const m = $derived(changeModel(data, namespace));
</script>

<Figure id="change" testid="change-figure" question="Why did the gate decide this, and where is the change now?"
  alt={m.alt} wires={m.wires} steps={m.steps} walk token={m.token} rowMin={m.stations.length * 150}
  autoplay={m.autoplay} autoKey={m.key}>
  {#snippet stage()}<Rail stations={m.stations} token={{ tone: m.tone, at: m.at }} />{/snippet}
  {#snippet caption()}{m.summary}{' '}<span class="fig-ref">{m.figRef}</span>{/snippet}
</Figure>
