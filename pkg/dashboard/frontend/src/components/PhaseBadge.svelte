<script>
  import { phaseBadgeClass } from '../lib/helpers.js';
  let { phase = 'Unknown' } = $props();
</script>

<span class="badge {phaseBadgeClass(phase)}"><span class="badge-dot"></span>{phase || 'Unknown'}</span>
