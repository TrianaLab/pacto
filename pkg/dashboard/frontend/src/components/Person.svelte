<script>
  // A person as the cluster knows them. The display name may be an ID-token
  // claim the person set themselves, so the verifiable username (or
  // issuer#sub) is printed next to it whenever the two differ, never only in a
  // tooltip. A service account is named by its namespace and name, both read
  // from the username itself, so its long username can go to the tooltip.
  // Emails and issuer#subject usernames wrap after @ . / # and never inside a word.
  import { personLabel } from '../lib/delivery.ts';
  import { breakableIdentifierHtml } from '../lib/format.ts';

  let { user, people = null, me = null } = $props();
  const name = $derived(personLabel(user, people, me));
  const sa = $derived(/^system:serviceaccount:([^:]+):([^:]+)$/.exec(user ?? ''));
  const own = $derived(!!sa && name === sa[2]);
</script>

{#if own}<span title={user}>{sa[1]}/{sa[2]}</span>{:else}<span class="person">{@html breakableIdentifierHtml(name)}{#if user && name !== user}{' '}<span class="person-id">({@html breakableIdentifierHtml(user)})</span>{/if}</span>{/if}

<style>
  .person { overflow-wrap: break-word; }
  .person-id { color: var(--c-text-2); font-weight: normal; }
</style>
