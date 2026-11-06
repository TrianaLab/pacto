<script>
  import IdentityBadge from './IdentityBadge.svelte';
  import { phaseTone, provisioningTitle } from '../lib/provisioning.ts';

  // What providers report about the resource one offering use asked for, one badge
  // per environment. The row shows the phase; the title carries who reported it,
  // through which source, signed or not, when, for which consumer revision and the
  // external reference, so a long reference never widens the row.
  let { statuses = [] } = $props();
</script>

{#each statuses as st, i (`${st.environment ?? ''}:${i}`)}
  <span class="pb" data-testid="provisioning-badge">
    <IdentityBadge label={st.environment ? `${st.environment}: ${st.phase}` : st.phase} tone={phaseTone(st.phase)} title={provisioningTitle(st)} describe />
  </span>
{/each}

<style>
  .pb { display: inline-flex; min-width: 0; max-width: 100%; }
</style>
