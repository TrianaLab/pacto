<script>
  // The posture figure (SPEC 7: Services, owner and domain pages): "How compliant are
  // these services?". One PostureColumn over the backend's compliance tally of the whole
  // population (never a page), each row linking to the list filtered to it (T3). Ambient:
  // no narration. Unknown is the grey hatch and is never counted as compliant.
  import Figure from './Figure.svelte';
  import PostureColumn from './PostureColumn.svelte';
  import { postureColumn } from '../../lib/fleetFigures.ts';
  import { formatCount } from '../../lib/format.ts';

  /** @type {{ id?: string, question: string, segments: import('../../lib/distributions.ts').Segment[], total: number, scope: string, compact?: boolean, testid?: string, aside?: import('svelte').Snippet | null }} */
  let { id = 'posture', question, segments, total, scope, compact = false, testid = 'posture-figure', aside = null } = $props();
  const wires = $derived(postureColumn(segments, total).wires);
  const said = $derived(segments.filter((s) => s.value).map((s) => `${formatCount(s.value)} ${s.label.toLowerCase()}`).join(', '));
</script>

<Figure {id} {testid} {question} {compact} {wires} {aside} alt={`${scope}: ${said || 'none'}, of ${formatCount(total)}.`}>
  {#snippet stage()}<PostureColumn {segments} {total} />{/snippet}
  {#snippet caption()}{scope}: {said || 'none'}. Unknown means no evidence; it is never counted as compliant.{/snippet}
</Figure>
