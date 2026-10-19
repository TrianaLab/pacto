<script>
  import { sourceTooltip } from '../lib/format.ts';

  // A single-letter initial so the source is decodable without relying on color
  // (blue kubernetes vs purple oci is a known confusion pair); aria-label carries the
  // full description for screen readers.
  const INITIALS = { kubernetes: 'K', oci: 'O', local: 'L', cache: 'C' };

  let { source, align = undefined } = $props();
  const initial = $derived(INITIALS[source] || (source?.[0] || '?').toUpperCase());
</script>

<span class="source-dot source-dot-{source}" data-tip={sourceTooltip(source)} data-tip-align={align}
  role="img" aria-label={sourceTooltip(source)}>{initial}</span>
