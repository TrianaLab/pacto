<script>
  import IdentityBadge from './IdentityBadge.svelte';
  import { lifecycleBadge, lifecycleText } from '../lib/behavior.ts';

  // A deprecated interface's state and its schedule. The badge turns from warn to err
  // once the sunset has passed, and its words change with it, so the state is never
  // carried by color alone. `today` is a parameter so tests do not read the clock.
  let { lifecycle = null, today = undefined, detail = true } = $props();
  const badge = $derived(lifecycleBadge(lifecycle, today));
</script>

{#if badge}
  <IdentityBadge label={badge.label} tone={badge.tone} title={badge.title} />
  {#if detail}<span class="lc-text">{lifecycleText(lifecycle)}</span>{/if}
{/if}

<style>
  .lc-text { font-size: var(--text-sm); color: var(--c-text-2); overflow-wrap: anywhere; }
</style>
