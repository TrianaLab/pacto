<script>
  // A person as the cluster knows them. The display name may be an ID-token
  // claim the person set themselves, so the verifiable username (or
  // issuer#sub) is printed next to it whenever the two differ, never only in a
  // tooltip.
  import { personLabel } from '../lib/delivery.ts';

  let { user, people = null, me = null } = $props();
  const name = $derived(personLabel(user, people, me));
</script>

{name}{#if user && name !== user}{' '}<span class="person-id">({user})</span>{/if}

<style>
  .person-id { color: var(--c-text-2); font-weight: normal; overflow-wrap: anywhere; }
</style>
