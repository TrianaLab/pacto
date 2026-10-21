<script>
  import { deliveryHistoryUrl } from '../lib/router.ts';
  import { shortDigest } from '../lib/delivery.ts';

  // What must move before a held promote can ship: each consumer running here
  // whose declared range does not accept the new version, its Stage and the
  // verified version upstream that can go first, when there is one. Pacto only
  // says it; a person or an agent moves the other service.
  let { order = null, testid = 'move-first' } = $props();

  const moveFirst = $derived(order?.moveFirst ?? []);
  const unknown = $derived(order?.unknown ?? []);
  const stageUrl = (s) => deliveryHistoryUrl({ namespace: s.namespace, stage: s.name });
</script>

{#if moveFirst.length}
  <div class="mf" data-testid={testid}>
    <ul class="mf-list">
      {#each moveFirst as m (m.service + (m.domain ?? ''))}
        <li class="mf-item">
          <p class="mf-who t-body"><strong>Update {m.service}{#if m.version}{' ' + m.version}{/if} first</strong>{#if m.accepts}<span class="t-body-2">: it accepts only {m.accepts}</span>{/if}</p>
          <p class="mf-msg t-body-2">{m.message}</p>
          {#if m.stage || m.candidate}
            <ul class="mf-links t-body-2">
              {#if m.stage}
                <li>Where it runs here: <a href={stageUrl(m.stage)}>Stage {m.stage.namespace}/{m.stage.name}</a></li>
              {/if}
              {#if m.candidate}
                <li>Ready to go first: <a href={stageUrl(m.candidate.verifiedOn)}>{m.service} {m.candidate.version || shortDigest(m.candidate.digest)}, verified on {m.candidate.verifiedOn.namespace}/{m.candidate.verifiedOn.name}</a></li>
              {/if}
            </ul>
          {/if}
        </li>
      {/each}
    </ul>
    {#if unknown.length}
      <div class="mf-unknown t-body-2" data-testid="{testid}-unknown">
        <p>This list may be incomplete:</p>
        <ul>{#each unknown as u (u)}<li>{u}</li>{/each}</ul>
      </div>
    {/if}
  </div>
{/if}

<style>
  .mf { display: flex; flex-direction: column; gap: var(--sp-2); }
  .mf-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .mf-item { background: var(--c-surface); border: 1px solid var(--c-border); border-left: 3px solid var(--c-warn); border-radius: var(--radius-sm); padding: var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-1); }
  .mf-who, .mf-msg, .mf-unknown p { margin: 0; }
  .mf-links, .mf-unknown ul { margin: 0; padding-left: var(--sp-5); }
  .mf a { color: var(--c-accent); text-decoration: underline; }
</style>
