<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryChangeUrl, deliveryHistoryUrl, deliveryInboxUrl } from '../lib/router.ts';
  import { deliveryServiceUrl } from '../lib/deliveryLinks.svelte.ts';
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
  import { urlInt, limitParam } from '../lib/paging.ts';
  import { formatCount, countOf } from '../lib/format.ts';

  // What waits on me or my team: held changes whose question names one of my
  // teams, then frozen Stages and changes that could not finish. Each
  // section is paged on the server, and its place is in the URL.
  let { held: heldAt = '', frozen: frozenAt = '', errored: erroredAt = '', limit = '', q = '', show = '', refreshTick = 0 } = $props();

  const query = $derived({ held: urlInt(heldAt), frozen: urlInt(frozenAt), errored: urlInt(erroredAt), limit: urlInt(limit), q, show });
  // One section shown whole, with its pager: the frozen Stages or the changes
  // that could not finish. Otherwise each is a short preview, so a page has
  // one pager at most. A link into a later page of one of them shows it.
  const whole = $derived(['frozen', 'errored'].includes(show) ? show : query.frozen ? 'frozen' : query.errored ? 'errored' : '');
  const PREVIEW = 5;
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
  // A question one team owns waits on that team; one several teams own, any of them may answer.
  // The server pages them together, so on a page they are split for reading only.
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
  // Every count below is over every page: the server sends the totals.
  const heldTotal = $derived(total(inbox?.heldPage, held));
  const frozenTotal = $derived(total(inbox?.frozenPage, frozen));
  const erroredTotal = $derived(total(inbox?.erroredPage, errored));
  const sharedTotal = $derived(inbox?.heldShared ?? shared.length);
  const morePages = $derived(!!inbox?.heldPage && inbox.heldPage.total > held.length);
  // Whose work this is: the reader's teams, or the reader when they name none.
  const whose = $derived(myGroups.length ? andList(myGroups, 3) : 'you');
  // The one-line answer: what waits for a decision, who else may answer, then what is frozen or failed.
  const lead = $derived.by(() => {
    if (!inbox) return null;
    const bad = [erroredTotal && `${formatCount(erroredTotal)} failed`, frozenTotal && `${formatCount(frozenTotal)} frozen`].filter(Boolean);
    const trouble = bad.length ? `${capital(andList(bad))}.` : '';
    if (!heldTotal) return bad.length ? { head: `${capital(andList(bad))}.`, rest: 'Nothing waits for a decision.' } : null;
    const others = !sharedTotal ? '' : sharedTotal === heldTotal
      ? `Other teams may also answer ${heldTotal === 1 ? 'it' : 'each of them'}.`
      : `Other teams may also answer ${formatCount(sharedTotal)} of them.`;
    return { head: `${countOf(heldTotal, 'change')} ${heldTotal === 1 ? 'waits' : 'wait'} for ${whose} to decide.`, rest: [others, trouble].filter(Boolean).join(' ') };
  });
  // A title repeated from the line above is cut to what differs: the rest is
  // still read out, hidden on screen.
  const repeats = (list, i, key) => i > 0 && key(list[i - 1]) === key(list[i]);
  const changeOn = (it) => `${typeLabel(it.type)} on ${it.namespace}/${it.stage}`;
  const moveOf = (it) => (it.fromVersion && it.toVersion ? `${it.fromVersion} to ${it.toVersion}` : it.toVersion || shortDigest(it.to));
  // One line per held change: what breaks and for whom. Without named services, the
  // first sentence of the server's question says why a person is needed.
  const breaksWord = (it) => it.decision === 'accept-unknown' ? 'Unknown for' : 'Breaks';
  const firstSentence = (q) => shortDigests(q ?? '').split(/(?<=[.?])\s/)[0];
  const capital = (t) => t.charAt(0).toUpperCase() + t.slice(1);
  const frozenRows = $derived(whole === 'frozen' ? frozen : frozen.slice(0, PREVIEW));
  const erroredRows = $derived(whole === 'errored' ? errored : errored.slice(0, PREVIEW));
  const nothing = $derived(!!inbox && total(inbox.heldPage, held) + total(inbox.frozenPage, frozen) + total(inbox.erroredPage, errored) === 0);
  // A link to another page of one section keeps the others where they are.
  const hrefFor = (section) => (off, lim) => deliveryInboxUrl({
    ...query, [section]: off, limit: limitParam(lim, limit, inbox?.[`${section}Page`]?.limit ?? lim),
  });
</script>

{#snippet row(it, again)}
  <li class="pl-row dl-row">
    <div class="dl-main">
      <a class="dl-title" class:dl-again={again && it.to} href={deliveryChangeUrl(it.namespace, it.name)}>
        {#if again && it.to}<span class="visually-hidden">{`${changeOn(it)}: `}</span>{moveOf(it)}{:else}{it.to ? `${changeOn(it)}: ${moveOf(it)}` : changeOn(it)}{/if}
      </a>
      <p class="dl-facts t-body-2">
        {#if it.consumers?.length}
          <span class="dl-affects">{breaksWord(it)} <CappedList items={it.consumers} max={3} inline={true} label="Affected services">{#snippet item(c)}<a href={deliveryServiceUrl(c)}>{c}</a>{/snippet}</CappedList></span>
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
    count={inbox ? `${formatCount(heldTotal)} waiting for a decision` : ''}
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
      {#if whole}
        <p class="t-body-2"><a href={deliveryInboxUrl({ q })} data-testid="inbox-back">Back to the inbox</a></p>
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
              {#each mine as it, i (it.namespace + '/' + it.name)}{@render row(it, repeats(mine, i, changeOn))}{/each}
            </ul>
          {/if}
          {#if shared.length}
            <!-- Questions several teams own, in the open like the rest: the pager
                 turns them with the others. Each row says who else may answer. -->
            {#if mine.length}<h3 class="dl-sub" data-testid="inbox-shared">{sharedTitle} <span class="t-meta">any of them may answer</span></h3>{/if}
            <ul class="pl-list" data-testid="inbox-held-shared">
              {#each shared as it, i (it.namespace + '/' + it.name)}{@render row(it, repeats(shared, i, changeOn))}{/each}
            </ul>
          {/if}
          <!-- Only when there is more than one page: the answer line already says how many. -->
          {#if morePages || (inbox.heldPage?.offset ?? 0) > 0}<Pager page={inbox.heldPage} href={hrefFor('held')} label="Pages of held changes" noun="held changes" testid="inbox-held-pager" />{/if}
        {/if}
      </section>
      {/if}

      {#if !frozen.length && !errored.length}
        <p class="t-meta dl-calm" data-testid="inbox-calm">Nothing frozen and nothing failed in the services {myGroups.length ? `${whose} ${myGroups.length === 1 ? 'owns' : 'own'}` : 'you own'}{inbox.completeness === 'partial' ? ' that you can see' : ''}.</p>
      {/if}
      <!-- Frozen Stages and changes that could not finish fold behind their count:
           the decisions above are what the reader came for. Each shows a few
           and links to all of them, so the page keeps one pager. -->
      {#if frozen.length && whole !== 'errored'}
        <details class="disclosure dl-more" open={whole === 'frozen'} data-testid="inbox-frozen-section">
          <summary><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span><h2 id="inbox-frozen" class="dl-more-title">Frozen Stages</h2> <span class="t-meta dl-count">{formatCount(frozenTotal)}</span></summary>
          <p class="t-body-2 dl-note">Nothing moves in these until a person unfreezes them.</p>
          <ul class="pl-list" data-testid="inbox-frozen">
            {#each frozenRows as row, i (row.namespace + '/' + row.stage)}
              {@const again = repeats(frozenRows, i, (r) => r.service)}
              <li class="pl-row dl-line">
                <a class="dl-title" class:dl-again={again} href={deliveryHistoryUrl({ namespace: row.namespace, stage: row.stage })}>{#if again}<span class="visually-hidden">{`${row.service} `}</span>{`in ${row.environment}`}{:else}{`${row.service} in ${row.environment}`}{/if}</a>
                <span class="t-body-2 dl-why">{row.frozenReason ? shortDigests(row.frozenReason) : 'Frozen'}</span>
              </li>
            {/each}
          </ul>
          {#if whole === 'frozen'}
            <Pager page={inbox.frozenPage} href={hrefFor('frozen')} label="Pages of frozen Stages" noun="frozen Stages" testid="inbox-frozen-pager" />
          {:else if frozenTotal > frozenRows.length}
            <a class="btn btn-sm dl-all" href={deliveryInboxUrl({ q, limit: query.limit || undefined, show: 'frozen' })} data-testid="inbox-frozen-all">Show all {formatCount(frozenTotal)} frozen Stages</a>
          {/if}
        </details>
      {/if}

      {#if errored.length && whole !== 'frozen'}
        <details class="disclosure dl-more" open={whole === 'errored'} data-testid="inbox-errored-section">
          <summary><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span><h2 id="inbox-errored" class="dl-more-title">Changes that could not finish</h2> <span class="t-meta dl-count">{formatCount(erroredTotal)}</span></summary>
          <p class="t-body-2 dl-note">Each Stage stays frozen until a person looks at it.</p>
          <ul class="pl-list" data-testid="inbox-errored">
            {#each erroredRows as it, i (it.namespace + '/' + it.name)}
              {@const again = repeats(erroredRows, i, (r) => r.stage)}
              <li class="pl-row dl-line">
                <a class="dl-title" class:dl-again={again} href={deliveryChangeUrl(it.namespace, it.name)}>{`${typeLabel(it.type)} on ${it.namespace}`}{#if again}<span class="visually-hidden">{`/${it.stage}`}</span>{:else}{`/${it.stage}`}{/if}</a>
                <span class="t-body-2 dl-why">{shortDigests(it.reason) || 'Pacto could not tell how it ended'}</span>
              </li>
            {/each}
          </ul>
          {#if whole === 'errored'}
            <Pager page={inbox.erroredPage} href={hrefFor('errored')} label="Pages of changes that could not finish" noun="changes" testid="inbox-errored-pager" />
          {:else if erroredTotal > erroredRows.length}
            <a class="btn btn-sm dl-all" href={deliveryInboxUrl({ q, limit: query.limit || undefined, show: 'errored' })} data-testid="inbox-errored-all">Show all {formatCount(erroredTotal)} changes that could not finish</a>
          {/if}
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
  .dl-count { font-variant-numeric: tabular-nums; }
  .dl-note { margin: var(--sp-1) 0 var(--sp-2); color: var(--c-text-3); }
  .dl-title { font-weight: 600; color: var(--c-accent); }
  /* A title cut to what differs from the line above sits under it, indented. */
  .dl-again { padding-left: var(--sp-4); font-weight: 500; }
  .dl-question { margin: 0; }
  /* Centred, not baseline: the "Show all" button in the affected services is taller than text. */
  .dl-facts { display: flex; flex-wrap: wrap; align-items: center; gap: 2px var(--sp-3); margin: 0; color: var(--c-text-2); }
  .dl-affects { min-width: 0; }
  .dl-calm { margin: 0; color: var(--c-text-3); }
  .dl-facts a { text-decoration: underline; }
  .dl-none { color: var(--c-text-3); }
  .dl-sub { display: flex; align-items: baseline; gap: var(--sp-2); margin: var(--sp-3) 0 var(--sp-1); font: 600 var(--text-sm) / 1.4 var(--font-sans, inherit); color: var(--c-text); }
  .dl-all { align-self: flex-start; margin-top: var(--sp-2); }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
