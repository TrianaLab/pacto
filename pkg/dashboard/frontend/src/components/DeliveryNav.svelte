<script>
  import { onMount } from 'svelte';
  import { loadSession } from '../lib/deliverySession.svelte.ts';
  import { deliveryInboxUrl, deliveryEnvironmentsUrl, deliveryAutonomyUrl, deliveryHistoryUrl, deliveryPublishUrl } from '../lib/router.ts';

  // The delivery section's own navigation. Who the views act as is in the
  // identity bar under the header, on every page; the session is read again
  // here so the views know what the apiserver says about the caller now.
  let { current = '' } = $props();

  onMount(() => { loadSession(); });

  const tabs = [
    { view: 'delivery-inbox', label: 'Inbox', href: deliveryInboxUrl() },
    { view: 'delivery-environments', label: 'Environments', href: deliveryEnvironmentsUrl() },
    { view: 'delivery-autonomy', label: 'Autonomy', href: deliveryAutonomyUrl() },
    { view: 'delivery-history', label: 'History', href: deliveryHistoryUrl() },
    { view: 'delivery-publish', label: 'Published', href: deliveryPublishUrl() },
  ];
</script>

<nav class="dn-tabs" aria-label="Delivery">
  {#each tabs as t (t.view)}
    <a href={t.href} class="dn-tab" aria-current={current === t.view ? 'page' : undefined}>{t.label}</a>
  {/each}
</nav>

<style>
  /* One row that scrolls on a phone, never a tab alone on a second line. */
  .dn-tabs { display: flex; gap: var(--sp-1); overflow-x: auto; scrollbar-width: thin; box-shadow: inset 0 -1px 0 var(--c-border); }
  /* On a narrow screen the last tab runs off the edge: a fade says there is more to scroll to. */
  @media (max-width: 30rem) {
    .dn-tabs { mask-image: linear-gradient(to right, #000 calc(100% - 2.5rem), transparent); padding-right: 2rem; }
  }
  .dn-tab {
    padding: var(--sp-2) var(--sp-3); color: var(--c-text-2); text-decoration: none;
    border-bottom: 2px solid transparent; font-size: var(--text-sm); font-weight: 500;
    white-space: nowrap; flex-shrink: 0;
  }
  .dn-tab:hover { color: var(--c-text); }
  .dn-tab:focus-visible { outline: 2px solid var(--c-accent); outline-offset: 2px; }
  .dn-tab[aria-current='page'] { color: var(--c-accent); border-bottom-color: var(--c-accent); }
</style>
