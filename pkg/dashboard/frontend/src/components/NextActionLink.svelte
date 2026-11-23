<script lang="ts">
  import { declareTarget } from '../lib/declare.ts';
  import { nextActionWords } from '../lib/entityLabels.ts';
  import { serviceDeclareUrl } from '../lib/router.ts';

  // A next action, said once for every list that shows one. A declare action whose
  // subject is an inferred service opens that service's Declare panel; any other
  // action stays words, the CLI command a person runs.
  type Props = { action?: { verb?: string; cli?: string } | null; subject?: { kind?: string; key?: string; declaration?: string } | null; cls?: string };
  let { action = null, subject = null, cls = '' }: Props = $props();
  const key = $derived(declareTarget(action, subject));
</script>

{#if key}
  <a class="nal {cls}" href={serviceDeclareUrl(key)} data-testid="next-action-link" title={action?.cli || undefined}>Declare this service</a>
{:else if action?.verb}
  <span class={cls || undefined}>{nextActionWords(action)}</span>
{/if}

<style>
  .nal { overflow-wrap: anywhere; }
  @media (pointer: coarse) {
    .nal { display: inline-flex; align-items: center; min-block-size: 44px; }
  }
</style>
