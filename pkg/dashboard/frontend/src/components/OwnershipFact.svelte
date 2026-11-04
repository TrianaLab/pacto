<script>
  // THE OWNER VALUE, in the one place every entity page reads it from.
  //
  // Three pages rendered `ownership.ref ? link : (ownership.owner || 'Unowned')`
  // independently, and all three were wrong the same way: an owner block of contacts
  // alone has no canonical identity and no display name, so it fell through to
  // "Unowned" — telling an operator to go and find a team that had already written
  // down how to reach them. The backend has said `declared` since the ownership
  // classification landed; nothing was reading it.
  //
  // So there are four states, not two:
  //   nothing declared        -> No declared owner
  //   cannot tell             -> Owner unknown (no readable contract, no source that
  //                              reads the contract its targets run; service only)
  //   declared, identifiable  -> the canonical owner, as a link
  //   declared, no identity   -> declared, and there is no owner page to go to
  //
  // The third state deliberately does NOT invent a name out of an email address or a
  // chat channel: an OwnerKey is what the product routes, groups and ranks by, and a
  // fabricated one puts a name on the fleet that nobody chose. The declaration's
  // actual content is read on the contract revision, which is the entity that
  // declared it.
  import EntityLink from './EntityLink.svelte';

  // unownedHref: where the two unowned states lead (the service page's own `unowned`
  // attention item). declaredIn: the revision whose owner block the service takes, where
  // its contacts are shown; contacts are never copied onto a service.
  let { ownership, unownedHref = '', declaredIn = null } = $props();
  const unknownTitle = "Pacto could not read this service's contracts, so it cannot tell whether an owner is declared. Pacto still counts it as unowned.";
</script>

{#if !ownership?.declared}
  {@const label = ownership?.unknown ? 'Owner unknown' : 'No declared owner'}
  {@const title = ownership?.unknown ? unknownTitle : undefined}
  {#if unownedHref}
    <a class="of-none" href={unownedHref} {title}>{label}</a>
  {:else}
    <span class="of-none" {title}>{label}</span>
  {/if}
{:else if ownership.ref}
  <EntityLink ref={ownership.ref} showStatus={false} showKind={false} />
{:else}
  <span class="of-unidentified">
    <span>Ownership declared</span>
    <span class="of-note">No Team or DRI identity</span>
  </span>
{/if}
{#if ownership?.declared && declaredIn}
  <span class="of-note" data-testid="owner-declared-in" title="Its contacts are on that revision's page">Declared in <EntityLink ref={declaredIn} showStatus={false} showKind={false} /></span>
{/if}

<style>
  .of-none { color: var(--c-text-3); }
  .of-unidentified { display: inline-flex; flex-direction: column; gap: 1px; }
  /* Quieter than the value it qualifies: the fact is that ownership WAS declared, and
     the missing identity is why this row is text rather than a link. */
  .of-note { color: var(--c-text-3); font-size: var(--text-xs); }
</style>
