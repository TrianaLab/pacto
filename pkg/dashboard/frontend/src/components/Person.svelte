<script>
  // A person as the cluster knows them, named for a reader: the name or email
  // from the session, a service account by its namespace and name, an
  // issuer#subject username by its subject. The username itself, often an
  // opaque identity provider subject, is in the copy button's tooltip and
  // copied whole. The name wraps after @ . / # and never inside a word.
  import { personLabel } from '../lib/session.svelte.ts';
  import { breakableIdentifierHtml } from '../lib/format.ts';
  import CopyableIdentifier from './CopyableIdentifier.svelte';

  let { user, people = null, me = null } = $props();
  const name = $derived(personLabel(user, people, me));
  const sa = $derived(/^system:serviceaccount:([^:]+):([^:]+)$/.exec(user ?? ''));
  const own = $derived(!!sa && name === sa[2]);
</script>

{#if own}<span title={user}>{sa[1]}/{sa[2]}</span>{:else}<span class="person">{@html breakableIdentifierHtml(name)}{#if user && name !== user}{' '}<span class="person-id"><CopyableIdentifier value={user} label="username" iconOnly /></span>{/if}</span>{/if}

<style>
  .person { overflow-wrap: break-word; }
  .person-id { display: inline-flex; max-width: 100%; min-width: 0; vertical-align: middle; font-weight: normal; }
</style>
