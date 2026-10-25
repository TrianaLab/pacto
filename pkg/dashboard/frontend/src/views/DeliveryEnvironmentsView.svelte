<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { deliveryHistoryUrl, serviceUrl } from '../lib/router.ts';
  import { shortDigest } from '../lib/delivery.ts';
  import PageHeader from '../components/PageHeader.svelte';
  import DeliveryNav from '../components/DeliveryNav.svelte';
  import DeliveryProblem from '../components/DeliveryProblem.svelte';
  import ListingNote from '../components/ListingNote.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ProposePanel from '../components/ProposePanel.svelte';

  // Every service in every environment: the version it runs and whether that
  // version is verified, failed, frozen or waiting. Promote and unfreeze start
  // from here, through a plan.
  let { refreshTick = 0, miraURL = '' } = $props();

  const loader = createProductLoader(() => api.deliveryEnvironments());
  $effect(() => { loader.sync(String(refreshTick)); });
  onDestroy(() => loader.destroy());

  const data = $derived(loader.data);
  const rows = $derived(data?.environments ?? []);
  const environments = $derived([...new Set(rows.map((r) => r.environment))].sort());
  const services = $derived([...new Set(rows.map((r) => r.service))].sort());
  const cell = (svc, env) => rows.filter((r) => r.service === svc && r.environment === env);

  // The plan being looked at: one request at a time.
  // Each request is a new object, so a second click re-plans in a new panel;
  // closing it sends focus back to the button that opened it.
  let proposing = $state(null);
  let proposingVersion = $state('');
  let opener = null;
  function open(e, req, version = '') { opener = e.currentTarget; proposingVersion = version; proposing = req; }
  function promote(r, e) { open(e, { namespace: r.namespace, stage: r.stage, type: 'delivery.pacto.trianalab.io/promote', to: r.candidate, from: r.pointer || undefined }, r.candidateVersion); }
  function unfreeze(r, e) { open(e, { namespace: r.namespace, stage: r.stage, type: 'delivery.pacto.trianalab.io/unfreeze' }); }
  function closePlan() { proposing = null; opener?.focus(); }
</script>

<div class="product-page">
  <PageHeader
    title="Environments"
    subtitle="Every service in every environment: the version it runs and whether that version is verified, failed, frozen or waiting. A Stage is one service in one environment."
    count={data ? `${environments.length} environment${environments.length === 1 ? '' : 's'}, ${rows.length} Stage${rows.length === 1 ? '' : 's'}` : ''}
    asOf={data?.asOf}
    revalidating={!!data && loader.loading}
  />
  <DeliveryNav current="delivery-environments" />

  {#if proposing}
    {#key proposing}
      <ProposePanel request={proposing} toVersion={proposingVersion} {miraURL} onClose={closePlan} />
    {/key}
  {/if}

  {#if !data && loader.loading}
    <EmptyState loading={true} />
  {:else if !data && loader.error}
    <DeliveryProblem error={loader.error} onRetry={() => loader.refresh()} />
  {:else if data}
    <ListingNote listing={data} error={loader.error} onRetry={() => loader.refresh()} />
    {#if rows.length === 0}
      <div class="state-box" data-testid="environments-empty">
        <h2>No environments yet</h2>
        <p>No Stage exists {data.completeness === 'partial' ? 'that you can see' : 'in this cluster'}. A Stage is one service in one environment, created with the pacto-delivery chart.</p>
      </div>
    {:else}
      <div class="ev-scroll">
        <table class="ev-table" data-testid="environments-board">
          <caption class="visually-hidden">Services by environment</caption>
          <thead>
            <tr><th scope="col">Service</th>{#each environments as env (env)}<th scope="col">{env}</th>{/each}</tr>
          </thead>
          <tbody>
            {#each services as svc (svc)}
              <tr>
                <th scope="row"><a href={serviceUrl(svc)}>{svc}</a></th>
                {#each environments as env (env)}
                  <td>
                    {#each cell(svc, env) as r (r.namespace + '/' + r.stage)}
                      <div class="ev-cell" data-testid="env-cell" data-stage={`${r.namespace}/${r.stage}`}>
                        <a class="ev-version" href={deliveryHistoryUrl({ namespace: r.namespace, stage: r.stage })}
                          title={r.pointer || 'nothing deployed yet'}>{r.version || shortDigest(r.pointer) || 'Nothing yet'}</a>
                        <span class="ev-badges">
                          {#if r.verified}<span class="badge badge-ok">Verified</span>{/if}
                          {#if r.failed}<span class="badge badge-err">Failed</span>{/if}
                          {#if r.frozen}<span class="badge badge-warn" title={r.frozenReason || 'Nothing moves here until a person unfreezes it'}>Frozen</span>{/if}
                          {#if r.autoHold}<span class="badge badge-info" title="A person or agent rolled this environment back or promoted it back to a version it already verified, so automation does not promote here until a person unfreezes it or promotes a version it has not verified yet">Auto-hold</span>{/if}
                          {#if r.held}<span class="badge badge-warn">{r.held} waiting</span>{/if}
                          {#if r.pointer && !r.verified && !r.failed}<span class="badge badge-neutral">Not verified</span>{/if}
                        </span>
                        {#if r.ready && !r.ready.startsWith('True')}<span class="t-meta ev-ready">Cannot promote: {r.ready.replace(/^False: /, '')}</span>{/if}
                        {#if r.actions?.length}
                          <span class="ev-actions">
                            {#if r.actions.includes('promote')}
                              <button type="button" class="btn btn-sm" onclick={(e) => promote(r, e)} aria-label={`Promote ${svc} in ${env} to ${r.candidateVersion || shortDigest(r.candidate)}`}>Promote {r.candidateVersion || shortDigest(r.candidate)}</button>
                            {/if}
                            {#if r.actions.includes('unfreeze')}
                              <button type="button" class="btn btn-sm" onclick={(e) => unfreeze(r, e)} aria-label={`Unfreeze ${svc} in ${env}`}>Unfreeze</button>
                            {/if}
                          </span>
                        {/if}
                      </div>
                    {:else}
                      <span class="t-meta">Not delivered here</span>
                    {/each}
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>

<style>
  .ev-scroll { overflow-x: auto; }
  .ev-table { background: var(--c-surface); border-collapse: collapse; width: 100%; font-size: var(--text-sm); }
  .ev-table th, .ev-table td { border-bottom: 1px solid var(--c-border); padding: var(--sp-2) var(--sp-3); text-align: left; vertical-align: top; }
  .ev-table thead th { color: var(--c-text-3); font-weight: 600; }
  .ev-table th a, .ev-version { color: var(--c-accent); }
  .ev-cell { display: flex; flex-direction: column; gap: var(--sp-1); }
  .ev-cell + .ev-cell { margin-top: var(--sp-2); }
  .ev-version { font-weight: 600; }
  .ev-badges, .ev-actions { display: flex; gap: var(--sp-1); flex-wrap: wrap; }
  .ev-ready { color: var(--c-warn); }
  .state-box { display: flex; flex-direction: column; align-items: center; gap: var(--sp-2); padding: var(--sp-8) var(--sp-4); text-align: center; color: var(--c-text-3); }
  .state-box h2 { color: var(--c-text-2); }
</style>
