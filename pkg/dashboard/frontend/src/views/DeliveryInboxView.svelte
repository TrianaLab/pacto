<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryChangeUrl, deliveryHistoryUrl, deliveryInboxUrl, serviceUrl } from '../lib/router.ts';
  import { deliverySession } from '../lib/deliverySession.svelte.ts';
  import { typeLabel, decisionLabel, shortDigest, shortDigests, APPROVING_DOCS, cannotApproveHere } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import MoveFirstList from '../components/MoveFirstList.svelte';
  import Pager from '../components/Pager.svelte';
  import ListFilter from '../components/ListFilter.svelte';
  import CappedList from '../components/CappedList.svelte';
  import Fold from '../components/Fold.svelte';
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';

  // What waits on me or my team: held changes whose question names one of my
  // teams, then frozen Stages and changes that could not finish. Each
  // section is paged on the server, and its place is in the URL.
  let { held: heldAt = '', frozen: frozenAt = '', errored: erroredAt = '', limit = '', q = '', refreshTick = 0 } = $props();

  const query = $derived({ held: urlInt(heldAt), frozen: urlInt(frozenAt), errored: urlInt(erroredAt), limit: urlInt(limit), q });
  const loader = createProductLoader(() => api.deliveryInbox({
    held: query.held || undefined, frozen: query.frozen || undefined, errored: query.errored || undefined,
    limit: query.limit || undefined, q: q || undefined,
  }));
  const question = $derived(JSON.stringify(query));
  // The same question without its page: turning a page keeps the rows on screen.
  const listQuestion = $derived(q);
  $effect(() => { loader.sync(`${question}@@${refreshTick}`, question, listQuestion); });
  onDestroy(() => loader.destroy());

  const answer = $derived(loader.answer(question, listQuestion));
  const inbox = $derived(answer.data);
  const held = $derived(inbox?.held ?? []);
  const frozen = $derived(inbox?.frozen ?? []);
  const errored = $derived(inbox?.errored ?? []);
  const total = (p, list) => p?.total ?? list.length;
  // A question one team owns is yours alone; one several teams own, any of them may answer.
  // Split on this page only: the server pages them together.
  const mine = $derived(held.filter((it) => (it.owners?.length ?? 0) <= 1));
  const shared = $derived(held.filter((it) => (it.owners?.length ?? 0) > 1));
  const nothing = $derived(!!inbox && total(inbox.heldPage, held) + total(inbox.frozenPage, frozen) + total(inbox.erroredPage, errored) === 0);
  // A link to another page of one section keeps the others where they are.
  const hrefFor = (section) => (off, lim) => deliveryInboxUrl({
    ...query, [section]: off, limit: limitParam(lim, limit, inbox?.[`${section}Page`]?.limit ?? lim),
  });
</script>

{#snippet row(it)}
  <li class="pl-row pl-row--stack dl-row">
    <a class="dl-title" href={deliveryChangeUrl(it.namespace, it.name)}>
      {typeLabel(it.type)} on {it.namespace}/{it.stage}{#if it.to}: {it.fromVersion && it.toVersion ? `${it.fromVersion} to ${it.toVersion}` : it.toVersion || shortDigest(it.to)}{/if}
    </a>
    {#if it.question}<p class="dl-question t-body">{shortDigests(it.question)}</p>{/if}
    <MoveFirstList order={it.order} testid="inbox-move-first" />
    <!-- Labels, not sentences: what is asked, who may answer and what it affects, on one line that wraps when full. -->
    <p class="dl-facts t-body-2">
      <span class="badge badge-neutral">{decisionLabel(it.decision) || 'A decision'}</span>
      <span>{#if it.owners?.length}{it.owners.join(', ')} may answer{:else if deliverySession.data?.requireOwnerTeam}Nobody may answer: no team is declared, so only break-glass can override{:else}Any person may answer{/if}</span>
      {#if it.consumers?.length}
        <span class="dl-affects">Affects <CappedList items={it.consumers} max={3} inline={true} label="Affected services">{#snippet item(c)}<a href={serviceUrl(c)}>{c}</a>{/snippet}</CappedList></span>
      {/if}
    </p>
  </li>
{/snippet}

<div class="product-page">
  <PageHeader
    title="Inbox"
    count={inbox ? `${formatCount(total(inbox.heldPage, held))} waiting for a decision` : ''}
    asOf={inbox?.asOf}
    revalidating={!!inbox && loader.loading}
    paging={answer.paging}
  />
  <DeliveryNav current="delivery-inbox" />
  <ListFilter value={q} href={(v) => deliveryInboxUrl({ q: v })} label="Filter by namespace, environment, service or change"
    placeholder="Namespace, environment, service or change" testid="inbox-filter" />

  {#if !inbox && loader.loading}
    <EmptyState loading={true} rows={3} />
  {:else if !inbox && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if inbox}
    <ListingNote listing={inbox} error={loader.error} onRetry={() => loader.refresh()} />

    {#if nothing}
      <div class="state-box" data-testid="inbox-empty">
        {#if q}
          <h2>Nothing matches the filter</h2>
          <p>Nothing waiting on you matches “{q}”.</p>
        {:else}
          <h2>{inbox.completeness === 'partial' ? 'Nothing waits on you that you can see' : 'Nothing waits on you'}</h2>
          <p>No change is held for your teams, and none of your Stages is frozen or stuck.</p>
        {/if}
      </div>
    {:else}
      <section aria-labelledby="inbox-held">
        <h2 id="inbox-held" class="t-section-title">Waiting for your decision</h2>
        {#if held.length && cannotApproveHere(deliverySession.data?.caller)}
          <p class="t-body-2" data-testid="inbox-no-auth-time">Approving is not possible on this cluster: it does not report when you signed in. <a href={APPROVING_DOCS} target="_blank" rel="noopener noreferrer">Where approving works</a></p>
        {/if}
        {#if held.length === 0}
          <p class="t-body-2 dl-none">No change is held for your teams.</p>
        {:else}
          {#if mine.length}
            <ul class="pl-list" data-testid="inbox-held">
              {#each mine as it (it.namespace + '/' + it.name)}{@render row(it)}{/each}
            </ul>
          {/if}
          {#if shared.length && mine.length}
            <!-- Questions several teams own: any of them may answer, so they fold behind their count. -->
            <Fold title="Shared with other teams" level={3} meta={`${formatCount(shared.length)}${inbox.heldPage && inbox.heldPage.total > held.length ? ' on this page' : ''} · any of the teams may answer`} testid="inbox-shared">
              <ul class="pl-list" data-testid="inbox-held-shared">
                {#each shared as it (it.namespace + '/' + it.name)}{@render row(it)}{/each}
              </ul>
            </Fold>
          {:else if shared.length}
            <!-- Nothing here is yours alone, so the shared ones are what waits: no fold to open. -->
            <ul class="pl-list" data-testid="inbox-held-shared">
              {#each shared as it (it.namespace + '/' + it.name)}{@render row(it)}{/each}
            </ul>
          {/if}
          <Pager page={inbox.heldPage} href={hrefFor('held')} label="Pages of held changes" noun="held changes" testid="inbox-held-pager" />
        {/if}
      </section>

      {#if !frozen.length && !errored.length}
        <p class="t-meta dl-calm" data-testid="inbox-calm">{inbox.completeness === 'partial' ? 'Nothing frozen and nothing failed that you can see.' : 'Nothing frozen and nothing failed.'}</p>
      {/if}
      <!-- Frozen Stages and changes that could not finish fold behind their count:
           the decisions above are what the reader came for. A link into one of their pages
           opens it. -->
      {#if frozen.length}
        <details class="disclosure dl-more" open={query.frozen > 0} data-testid="inbox-frozen-section">
          <summary class="t-section-title"><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span><h2 id="inbox-frozen" class="dl-more-title">Frozen Stages</h2> <span class="t-meta dl-count">{formatCount(total(inbox.frozenPage, frozen))}</span></summary>
          <p class="t-body-2 dl-note">Nothing moves in these until a person unfreezes them.</p>
          <ul class="pl-list" data-testid="inbox-frozen">
            {#each frozen as row (row.namespace + '/' + row.stage)}
              <li class="pl-row dl-line">
                <a class="dl-title" href={deliveryHistoryUrl({ namespace: row.namespace, stage: row.stage })}>{row.service} in {row.environment}</a>
                <span class="t-body-2 dl-why">{row.frozenReason ? shortDigests(row.frozenReason) : 'Frozen'}</span>
              </li>
            {/each}
          </ul>
          <Pager page={inbox.frozenPage} href={hrefFor('frozen')} label="Pages of frozen Stages" noun="frozen Stages" testid="inbox-frozen-pager" />
        </details>
      {/if}

      {#if errored.length}
        <details class="disclosure dl-more" open={query.errored > 0} data-testid="inbox-errored-section">
          <summary class="t-section-title"><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span><h2 id="inbox-errored" class="dl-more-title">Changes that could not finish</h2> <span class="t-meta dl-count">{formatCount(total(inbox.erroredPage, errored))}</span></summary>
          <p class="t-body-2 dl-note">Each Stage stays frozen until a person looks at it.</p>
          <ul class="pl-list" data-testid="inbox-errored">
            {#each errored as it (it.namespace + '/' + it.name)}
              <li class="pl-row dl-line">
                <a class="dl-title" href={deliveryChangeUrl(it.namespace, it.name)}>{typeLabel(it.type)} on {it.namespace}/{it.stage}</a>
                <span class="t-body-2 dl-why">{shortDigests(it.reason) || 'Pacto could not tell how it ended'}</span>
              </li>
            {/each}
          </ul>
          <Pager page={inbox.erroredPage} href={hrefFor('errored')} label="Pages of changes that could not finish" noun="changes" testid="inbox-errored-pager" />
        </details>
      {/if}
    {/if}
  {/if}
</div>

<style>
  .dl-row { gap: 2px; padding: var(--sp-2) var(--sp-3); overflow-wrap: break-word; }
  /* One line per frozen Stage or stuck change: what, then why. The why wraps
     under it rather than clipping, so the whole reason is always there to read. */
  .dl-line { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0 var(--sp-3); padding: var(--sp-1) var(--sp-3); overflow-wrap: anywhere; }
  .dl-line .dl-title { flex: 0 1 auto; }
  .dl-why { flex: 1 1 20rem; color: var(--c-text-2); }
  .dl-more > summary { display: flex; align-items: baseline; gap: var(--sp-2); cursor: pointer; }
  .dl-more-title { margin: 0; font: inherit; }
  .dl-count { font-variant-numeric: tabular-nums; }
  .dl-note { margin: var(--sp-1) 0 var(--sp-2); color: var(--c-text-3); }
  .dl-title { font-weight: 600; color: var(--c-accent); }
  .dl-question { margin: 0; }
  /* Centred, not baseline: the "Show all" button in the affected services is taller than text. */
  .dl-facts { display: flex; flex-wrap: wrap; align-items: center; gap: 2px var(--sp-3); margin: 0; color: var(--c-text-2); }
  .dl-affects { min-width: 0; }
  .dl-calm { margin: 0; color: var(--c-text-3); }
  .dl-facts a { text-decoration: underline; }
  .dl-none { color: var(--c-text-3); }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
