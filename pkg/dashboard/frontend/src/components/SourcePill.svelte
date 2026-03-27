<script>
  import { sourceTooltips } from '../lib/helpers.js';
  let { type } = $props();
</script>

<span class="source-pill source-pill-{type}" title={sourceTooltips[type] || type}>
  <span class="pill-dot"></span>{type.toUpperCase()}
</span>
