<script>
  import { sourceTooltip } from '../lib/format.ts';

  let { source, align = undefined } = $props();
</script>

<span class="source-dot source-dot-{source}" data-tip={sourceTooltip(source)} data-tip-align={align}></span>
