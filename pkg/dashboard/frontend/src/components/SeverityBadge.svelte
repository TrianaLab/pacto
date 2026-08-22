<script>
  import { severityLabel, severityTone } from '../lib/entityLabels.ts';
  import IdentityBadge from './IdentityBadge.svelte';

  // How much a finding or triage item MATTERS, which is not what state an entity is
  // IN. Every product surface that grades severity — triage, findings, the overview
  // preview, an owner's queue — routed it through the compliance badge, which has no
  // case for these words: an `error` printed as a grey lowercase "error", so the row
  // the screen exists to surface was the least visible thing on it. One component, so
  // the next surface to grade severity cannot get it wrong again.
  let { severity = '' } = $props();
</script>

<IdentityBadge label={severityLabel(severity)} tone={severityTone(severity)} />
