<script>
  // Ghost (SPEC 3): what the reader may not see. A hatched node and a count, never a
  // name, never inside a total and never a link (no list can name them). It takes the
  // lane's name and count columns, and its note wraps on its own line (C8).
  import Node from './Node.svelte';
  import { formatCount } from '../../lib/format.ts';

  // A lane with no count (the sources Pacto could not read) passes n null and its own glyph.
  /** @type {{ k: number, n: number | null, label?: string, glyph?: any, note: string }} */
  let { k, n, label = 'Hidden from you', glyph = 'hidden', note } = $props();
</script>

<div class="lane ghost" data-a="lane-{k}">
  <Node tone="neutral" hatch {glyph} a="ln-{k}" m="ln-{k}" />
  <span class="lane-name" class:words={label.length <= 20}>{label}</span>
  {#if n != null}<span class="lane-count" data-nofilter><b>{formatCount(n)}</b></span>{/if}
</div>
<p class="lane-sub t-meta">{note}</p>
