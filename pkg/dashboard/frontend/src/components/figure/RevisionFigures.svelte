<script>
  // The revision page's figures (SPEC 7): where validation stopped (a Rail of the stages
  // in run order, walked one station at a time, A1b), and the revision between its
  // neighbours (a Stack: the next one above, this one, the previous one below).
  import Figure from './Figure.svelte';
  import Rail from './Rail.svelte';
  import StackRows from './StackRows.svelte';
  import { validationModel, revisionStackModel } from '../../lib/entityFigures.ts';
  import { inService } from '../../lib/identityContext.ts';

  /** @type {{ stages: any[], revision: any, self: string, service: string, historyHref: string }} */
  let { stages, revision, self, service, historyHref } = $props();
  const v = $derived(validationModel(stages));
  const label = (/** @type {any} */ r) => inService(r, service).label ?? r.key ?? '';
  const s = $derived(revisionStackModel({ key: self, version: revision.version, label: revision.version || self }, revision.previous, revision.next, label));
</script>

{#if stages.length}
  <Figure id="validation" testid="revision-validation-figure" question={v.question} alt={v.alt} wires={v.wires} steps={v.steps} walk
    rowMin={v.stations.length * 150} autoplay={v.autoplay} autoKey={v.key}>
    {#snippet stage()}<Rail stations={v.stations} />{/snippet}
    {#snippet caption()}{v.summary}{/snippet}
  </Figure>
{/if}

<Figure id="around" testid="revision-around-figure" question="What is around this revision?" alt={s.alt} wires={s.wires}>
  {#snippet stage()}<StackRows layers={s.layers} rows={s.rows} />{/snippet}
  {#snippet aside()}
    <dl class="facts">
      <dt>Previous</dt><dd class="mono">{#if revision.previous}<bdi>{label(revision.previous)}</bdi>{:else}none{/if}</dd>
      <dt>Next</dt><dd class="mono">{#if revision.next}<bdi>{label(revision.next)}</bdi>{:else}none{/if}</dd>
      {#if historyHref}<dt>History</dt><dd><a href={historyHref}>All revisions of this service</a></dd>{/if}
    </dl>
  {/snippet}
  {#snippet caption()}{s.summary}{' '}<span class="fig-ref">Dashed layers are the neighbours; the accent layer is this revision.</span>{/snippet}
</Figure>
