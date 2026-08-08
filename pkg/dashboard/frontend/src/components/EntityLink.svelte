<script>
  import { hashForHref, fleetEntityUrl } from '../lib/router.ts';
  import EntityIdentity from './EntityIdentity.svelte';

  // The ONE way a component links to an entity. It prefers the authoritative backend
  // href the product API already built from the exact key (ProductRef.href); only
  // when a ref lacks one does it fall back to the centralized (kind, key) builder.
  // No component assembles a /fleet/... path itself.
  let { ref = {}, showStatus = true } = $props();

  const href = $derived(
    ref.href ? hashForHref(ref.href) : (ref.kind && ref.key ? fleetEntityUrl(ref.kind, ref.key) : '#/fleet'),
  );
</script>

<a class="entity-link" {href}>
  <EntityIdentity {ref} {showStatus} />
</a>

<style>
  .entity-link {
    display: inline-flex; text-decoration: none; color: inherit; min-width: 0;
    border-radius: var(--radius-xs);
  }
  .entity-link:hover :global(.ei-label) { text-decoration: underline; color: var(--c-accent); }
  .entity-link:focus-visible { outline: 2px solid var(--c-accent); outline-offset: 2px; }
</style>
