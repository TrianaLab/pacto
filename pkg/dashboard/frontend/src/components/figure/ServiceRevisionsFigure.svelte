<script>
  // The service page's revisions (SPEC 7): "Which revisions exist?". Up to five layers,
  // newest on top and the current one in accent; the rest fold into the plinth, whose row
  // links to the full, paged history. Counts come from the backend total.
  import Figure from './Figure.svelte';
  import StackRows from './StackRows.svelte';
  import { serviceStackModel } from '../../lib/entityFigures.ts';
  import { countOf, formatCount } from '../../lib/format.ts';
  import { inService } from '../../lib/identityContext.ts';

  /** @type {{ revisions: any, currentKey: string, self: string, allHref: string }} */
  let { revisions, currentKey, self, allHref } = $props();
  const label = (/** @type {any} */ r) => inService(r, self).label ?? r.key ?? '';
  const m = $derived(serviceStackModel(revisions, currentKey, label));
</script>

<Figure id="revisions" testid="service-revisions-figure" question="Which revisions exist?" alt={m.alt} wires={m.wires}>
  {#snippet stage()}<StackRows layers={m.layers} rows={m.rows} earlier={m.earlier} earlierHref={allHref} />{/snippet}
  {#snippet aside()}
    <dl class="facts">
      <dt>Revisions</dt><dd><a href={allHref}>{formatCount(m.total)}</a></dd>
      {#if m.newest}<dt>Newest</dt><dd class="mono"><bdi>{m.newest}</bdi></dd>{/if}
      {#if m.oldest && m.total > 1}<dt>Oldest</dt><dd class="mono"><bdi>{m.oldest}</bdi></dd>{/if}
      <dt>Drawn</dt><dd>The newest {countOf(m.layers.length, 'revision')}{m.earlier ? `; the ${formatCount(m.earlier)} earlier ones are on the plinth` : ''}</dd>
    </dl>
  {/snippet}
  {#snippet caption()}{countOf(m.total, 'revision')}, newest first.{' '}<span class="fig-ref">The accent layer is the revision in use.</span> The full history is listed under All revisions.{/snippet}
</Figure>
