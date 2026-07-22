<script>
  import { categoryIconInner } from '../lib/categoryIcons.ts';
  // `category` = the raw readiness category slug. Renders its stroke icon,
  // inheriting text color (currentColor). Hover title + aria-label carry the
  // full name so the icon is never meaning-by-shape-alone.
  let { category = '', size = 16 } = $props();
</script>

<svg
  class="cat-icon"
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="1.8"
  stroke-linecap="round"
  stroke-linejoin="round"
  role="img"
  aria-label={category}
><title>{category}</title>{@html categoryIconInner(category)}</svg>

<style>
  .cat-icon { display: inline-block; vertical-align: middle; flex-shrink: 0; }
</style>
