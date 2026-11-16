<script>
  import { classSourceLabel, accessSourceLabel, timeLabel } from '../lib/delivery.ts';

  // How an approver was classified and what let them decide, with who
  // checked it and when. A record from an earlier promoter kept none of it,
  // and the line says so rather than guess.
  let { a } = $props();
</script>

<span class="aa t-meta" data-testid="approval-access">{classSourceLabel(a.classifiedBy)}{#if a.access}; may <bdi>{a.access}</bdi>, {accessSourceLabel(a.accessSource)}{#if a.accessAt}{' '}at <time datetime={a.accessAt}>{timeLabel(a.accessAt)}</time>{/if}{:else if a.classifiedBy}; access not recorded{/if}</span>

<style>
  .aa { display: block; color: var(--c-text-2); overflow-wrap: anywhere; }
</style>
