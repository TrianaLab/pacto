<script>
  import { sourceHealthLabel, sourceHealthTone } from '../lib/entityLabels.ts';
  import StatusBadge from './StatusBadge.svelte';
  import IdentityBadge from './IdentityBadge.svelte';

  // Not every entity's status word belongs to the same vocabulary. A service, revision
  // or target carries COMPLIANCE; a data source carries HEALTH. Routing health through
  // the compliance badge fell through to the raw lowercase value in neutral grey, so one
  // source read "unavailable" (grey chip) in its own page header and "Unavailable" (red)
  // in the fact row directly below it. This is the single place that decides, so a new
  // call site cannot reintroduce the split.
  let { kind = '', status = '' } = $props();
</script>

{#if status}
  {#if kind === 'source'}
    <IdentityBadge label={sourceHealthLabel(status)} tone={sourceHealthTone(status)} />
  {:else}
    <StatusBadge {status} />
  {/if}
{/if}
