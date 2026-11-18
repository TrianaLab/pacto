<script>
  import { classSourceLabel, accessSourceLabel, timeLabel } from '../lib/delivery.ts';
  import ProvenanceMark from './ProvenanceMark.svelte';

  // How an approver was classified and what let them decide, with who
  // checked it and when. A record from an earlier promoter kept none of it,
  // and the line says so rather than guess.
  // An approval with a signed record carries one mark saying who signed it.
  let { a } = $props();
  const signed = $derived(!a.signer ? ''
    : a.signer.kind === 'recorder'
      ? `Signed by the recorder for ${a.signer.onBehalfOf?.user ?? a.by}, whom the cluster's admission checked.`
      : `Signed by ${a.by} with a keyless certificate.`);
</script>

<span class="aa t-meta" data-testid="approval-access">{classSourceLabel(a.classifiedBy)}{#if a.access}; may <bdi>{a.access}</bdi>, {accessSourceLabel(a.accessSource)}{#if a.accessAt}{' '}at <time datetime={a.accessAt}>{timeLabel(a.accessAt)}</time>{/if}{:else if a.classifiedBy}; access not recorded{/if}{#if signed}{' '}<ProvenanceMark label="How this approval was signed" text={`${signed}${a.record ? ` Record ${a.record}.` : ''}`} />{/if}</span>

<style>
  .aa { display: block; color: var(--c-text-2); overflow-wrap: anywhere; }
</style>
