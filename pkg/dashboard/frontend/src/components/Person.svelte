<script>
  // A person as the cluster knows them. The display name may be an ID-token
  // claim the person set themselves, so the verifiable username (or
  // issuer#sub) is printed next to it whenever the two differ, never only in a
  // tooltip. A service account is named by its namespace and name, both read
  // from the username itself, so its long username can go to the tooltip.
  import { personLabel } from '../lib/delivery.ts';

  let { user, people = null, me = null } = $props();
  const name = $derived(personLabel(user, people, me));
  const sa = $derived(/^system:serviceaccount:([^:]+):([^:]+)$/.exec(user ?? ''));
  const own = $derived(!!sa && name === sa[2]);
</script>

{#if own}<span title={user}>{sa[1]}/{sa[2]}</span>{:else}{name}{#if user && name !== user}{' '}<span class="person-id">({user})</span>{/if}{/if}

<style>
  .person-id { color: var(--c-text-2); font-weight: normal; overflow-wrap: anywhere; }
</style>
