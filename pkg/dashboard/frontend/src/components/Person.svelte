<script>
  // A person as the cluster knows them. The display name may be an ID-token
  // claim the person set themselves, so the verifiable username (or
  // issuer#sub) is printed next to it whenever the two differ, never only in a
  // tooltip. A service account is named by its namespace and name, both read
  // from the username itself, so its long username can go to the tooltip.
  // The name wraps after @ . / # and never inside a word. The username stays
  // on one line, cut in the middle when it is long (its end tells two apart),
  // with a copy button for the whole of it.
  import { personLabel } from '../lib/delivery.ts';
  import { breakableIdentifierHtml } from '../lib/format.ts';
  import CopyableIdentifier from './CopyableIdentifier.svelte';

  let { user, people = null, me = null } = $props();
  const name = $derived(personLabel(user, people, me));
  const sa = $derived(/^system:serviceaccount:([^:]+):([^:]+)$/.exec(user ?? ''));
  const own = $derived(!!sa && name === sa[2]);
</script>

{#if own}<span title={user}>{sa[1]}/{sa[2]}</span>{:else}<span class="person">{@html breakableIdentifierHtml(name)}{#if user && name !== user}{' '}<span class="person-id"><CopyableIdentifier value={user} label="username" /></span>{/if}</span>{/if}

<style>
  .person { overflow-wrap: break-word; }
  .person-id { display: inline-flex; max-width: 100%; min-width: 0; vertical-align: middle; font-weight: normal; }
</style>
