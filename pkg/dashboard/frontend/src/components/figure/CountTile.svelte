<script>
  // CountTile (SPEC 3): what a capped figure does not draw, as one line that links to
  // the paged, filtered table: "+312 more · 40 direct · 3 you may not see". The hidden
  // count is never a link and never part of the total it sits beside.
  import { formatCount } from '../../lib/format.ts';

  /** @type {{ more: number, href: string, parts?: string[], hidden?: number }} */
  let { more, href, parts = [], hidden = 0 } = $props();
</script>

<p class="count-tile">
  {#if more > 0}<a {href}>+{formatCount(more)} more</a>{/if}
  {#each parts as p (p)}<span class="count-tile-part">{p}</span>{/each}
  {#if hidden > 0}<span class="count-tile-part" data-nofilter>{formatCount(hidden)} you may not see</span>{/if}
</p>
