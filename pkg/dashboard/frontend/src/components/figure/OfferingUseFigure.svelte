<script>
  // The offering use detail (SPEC 7, A6): "Is this request ready?". One request of one
  // use on its rail. A provider reports only the current phase, so the narration is one
  // step: the current station appears with its reported time; earlier stations are at
  // rest for their order only. The reader picked the request, so it plays (A2).
  import Figure from './Figure.svelte';
  import Rail from './Rail.svelte';
  import { useDetailModel } from '../../lib/entityFigures.ts';
  import { formatDateTime } from '../../lib/dateFormat.ts';

  /** @type {{ consumer: string, request: import('../../lib/entityFigures.ts').UseRequest, useKey: string }} */
  let { consumer, request, useKey } = $props();
  const m = $derived(useDetailModel(request, request.asOf ? `at ${formatDateTime(request.asOf)}` : 'at an unknown time'));
  const where = $derived([request.environment, request.entry].filter(Boolean).join(' '));
</script>

<Figure id="use" testid="offering-use-figure" question={`Is the request of ${consumer}${where ? ` in ${where}` : ''} ready?`}
  alt={m.alt} wires={m.wires} steps={m.steps} rowMin={m.stations.length * 150} autoplay autoKey={{ use: useKey, ...m.key }}>
  {#snippet stage()}<Rail stations={m.stations} />{/snippet}
  {#snippet caption()}{m.summary} <span class="t-meta">From the provider status in the fleet snapshot.</span>{/snippet}
</Figure>
