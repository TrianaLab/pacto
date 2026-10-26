<script>
  import { deliveryHistoryUrl } from '../lib/router.ts';
  import { shortDigest } from '../lib/delivery.ts';
  import CappedList from './CappedList.svelte';
  import DigestText from './DigestText.svelte';

  // What must move before a held promote can ship: each consumer running here
  // whose declared range does not accept the new version, its Stage and the
  // verified version upstream that can go first, when there is one. Pacto only
  // says it; a person or an agent moves the other service. One sentence per
  // consumer, built from the plan's fields: the plan's own message repeats
  // the same facts with full digests.
  let { order = null, testid = 'move-first' } = $props();

  const moveFirst = $derived(order?.moveFirst ?? []);
  const unknown = $derived(order?.unknown ?? []);
  const stageUrl = (s) => deliveryHistoryUrl({ namespace: s.namespace, stage: s.name });
</script>

{#if moveFirst.length}
  <div class="mf" data-testid={testid}>
    <CappedList items={moveFirst} max={3} label="Services to update first">
      {#snippet item(m)}
        <div class="mf-item t-body-2">
          <strong class="t-body">Update {m.service} first</strong>{#if m.accepts}: {m.version ? `${m.service} ${m.version} runs here and accepts` : 'it accepts'} only {m.accepts}{/if}{#if m.candidate}; <a href={stageUrl(m.candidate.verifiedOn)}>{m.service} {m.candidate.version || shortDigest(m.candidate.digest)}, verified on {m.candidate.verifiedOn.namespace}/{m.candidate.verifiedOn.name}</a> {m.candidate.accepts ? 'accepts the new version' : 'no longer depends on it'}, so {#if m.stage}promote it on <a href={stageUrl(m.stage)}>Stage {m.stage.namespace}/{m.stage.name}</a>{:else}ship it first{/if}.{:else if m.unknown}, and Pacto cannot tell what would move it first: {m.unknown}.{:else}, and no version verified upstream can move first yet{#if m.stage}{' '}(it runs on <a href={stageUrl(m.stage)}>Stage {m.stage.namespace}/{m.stage.name}</a>){/if}.{/if}
        </div>
      {/snippet}
    </CappedList>
    {#if unknown.length}
      <div class="mf-unknown t-body-2" data-testid="{testid}-unknown">
        <p>This list may be incomplete:</p>
        <CappedList items={unknown} max={5}>{#snippet item(u)}<DigestText text={u} />{/snippet}</CappedList>
      </div>
    {/if}
  </div>
{/if}

<style>
  .mf { display: flex; flex-direction: column; gap: var(--sp-2); }
  .mf > :global(.cl) { list-style: none; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .mf-item { background: var(--c-surface); border: 1px solid var(--c-border); border-left: 3px solid var(--c-warn); border-radius: var(--radius-sm); padding: var(--sp-3); }
  .mf-unknown p { margin: 0; }
  /* Unknown reasons may carry names that never break on their own. */
  .mf-item, .mf-unknown { overflow-wrap: break-word; }
  .mf a { color: var(--c-accent); text-decoration: underline; }
</style>
