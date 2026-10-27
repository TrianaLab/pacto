<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryChangeUrl, deliveryHistoryUrl, deliveryInboxUrl, serviceUrl } from '../lib/router.ts';
  import { deliverySession } from '../lib/deliverySession.svelte.ts';
  import { typeLabel, shortDigest, shortDigests, andList, APPROVING_DOCS, cannotApproveHere } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import DeliveryLead from '../components/DeliveryLead.svelte';
  import Pager from '../components/Pager.svelte';
  import ListFilter from '../components/ListFilter.svelte';
  import CappedList from '../components/CappedList.svelte';
  import Fold from '../components/Fold.svelte';
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount, countOf } from '../lib/format.ts';

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
  // The other teams a shared question names: the reader's own groups are left out.
  const myGroups = $derived(deliverySession.data?.caller?.groups ?? []);
  const sharedWith = $derived.by(() => {
    const all = [...new Set(shared.flatMap((it) => it.owners ?? []))];
    const others = all.filter((t) => !myGroups.includes(t));
    return others.length ? others : all;
  });
  const sharedTitle = $derived(sharedWith.length ? `Shared with ${andList(sharedWith, 3)}` : 'Shared with other teams');
  // Counted on this page: the server pages both kinds together.
  const onPage = $derived(!!inbox?.heldPage && inbox.heldPage.total > held.length);
  // The one-line answer: what only you can decide, then how much more is shared.
  const lead = $derived.by(() => {
    if (!inbox || !held.length) return null;
    const more = shared.length ? `${formatCount(shared.length)} ${mine.length ? 'more are' : (shared.length === 1 ? 'is' : 'are')} shared with ${andList(sharedWith, 3) || 'other teams'}${onPage ? ' on this page' : ''}.` : '';
    return mine.length
      ? { head: `${countOf(mine.length, 'decision')} only you can make${onPage ? ' on this page' : ''}.`, rest: more }
      : { head: 'Nothing only you can answer.', rest: more };
  });
  // One line per held change: what breaks and for whom. Without named services, the
  // first sentence of the server's question says why a person is needed.
  const breaksWord = (it) => it.decision === 'accept-unknown' ? 'Unknown for' : 'Breaks';
  const firstSentence = (q) => shortDigests(q ?? '').split(/(?<=[.?])\s/)[0];
  const nothing = $derived(!!inbox && total(inbox.heldPage, held) + total(inbox.frozenPage, frozen) + total(inbox.erroredPage, errored) === 0);
  // A link to another page of one section keeps the others where they are.
  const hrefFor = (section) => (off, lim) => deliveryInboxUrl({
    ...query, [section]: off, limit: limitParam(lim, limit, inbox?.[`${section}Page`]?.limit ?? lim),
  });
</script>

{#snippet row(it)}
  <li class="pl-row dl-row">
    <div class="dl-main">
      <a class="dl-title" href={deliveryChangeUrl(it.namespace, it.name)}>
        {typeLabel(it.type)} on {it.namespace}/{it.stage}{#if it.to}: {it.fromVersion && it.toVersion ? `${it.fromVersion} to ${it.toVersion}` : it.toVersion || shortDigest(it.to)}{/if}
      </a>
      <p class="dl-facts t-body-2">
        {#if it.consumers?.length}
          <span class="dl-affects">{breaksWord(it)} <CappedList items={it.consumers} max={3} inline={true} label="Affected services">{#snippet item(c)}<a href={serviceUrl(c)}>{c}</a>{/snippet}</CappedList></span>
        {:else if it.question}
          <span class="dl-question">{firstSentence(it.question)}</span>
        {/if}
        <span class="dl-who">{#if it.owners?.length}{andList(it.owners, 3)} may answer{:else if deliverySession.data?.requireOwnerTeam}Nobody may answer: no team is declared, so only break-glass can override{:else}Any person may answer{/if}</span>
      </p>
    </div>
    <a class="btn btn-primary btn-sm dl-decide" href={deliveryChangeUrl(it.namespace, it.name)} aria-label={`Decide: ${typeLabel(it.type)} on ${it.namespace}/${it.stage}`}>Decide</a>
  </li>
{/snippet}

<div class="product-page">
  <PageHeader
    title="Inbox"
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

    {#if lead}<DeliveryLead head={lead.head} rest={lead.rest} testid="inbox-answer" />{/if}
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
          {#if shared.length}
            <!-- Questions several teams own: any of them may answer, so they fold behind
                 their count, even when nothing is yours alone. -->
            {#if !mine.length}<p class="t-body-2 dl-none" data-testid="inbox-none-mine">Nothing only you can answer.</p>{/if}
            <Fold title={sharedTitle} level={3} meta={`${formatCount(shared.length)}${onPage ? ' on this page' : ''} · any of them may answer`} testid="inbox-shared">
              <ul class="pl-list" data-testid="inbox-held-shared">
                {#each shared as it (it.namespace + '/' + it.name)}{@render row(it)}{/each}
              </ul>
            </Fold>
          {/if}
          <!-- Only when there is more than one page: the answer line already says how many. -->
          {#if onPage || (inbox.heldPage?.offset ?? 0) > 0}<Pager page={inbox.heldPage} href={hrefFor('held')} label="Pages of held changes" noun="held changes" testid="inbox-held-pager" />{/if}
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
  .dl-row { display: flex; align-items: center; gap: var(--sp-3); padding: var(--sp-2) var(--sp-3); overflow-wrap: break-word; }
  .dl-main { flex: 1 1 auto; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .dl-decide { flex: none; }
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
