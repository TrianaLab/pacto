<script>
  import { statusClass, statusLabel } from '../lib/format.ts';

  let { status } = $props();
</script>

<span class="badge badge-{statusClass(status)}">
  <span class="badge-dot"></span>{statusLabel(status)}
</span>
