<script>
  import ShowAll from '../components/ShowAll.svelte';
  import { cap } from '../lib/cap.svelte.ts';
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import SectionState from './SectionState.svelte';
  import IdentityBadge from '../components/IdentityBadge.svelte';
  import { stageBadge } from '../lib/behavior.ts';

  // stages is how each validation stage ended, so a stage that did not run is never
  // read as a pass.
  let { validation, stages = [], open = $bindable(false), id = '' } = $props();

  // Only render when there are issues to show. Conditions live in Overview, so
  // they must not make this (errors/warnings-only) section render empty.
  let hasContent = $derived(
    (stages?.length > 0) ||
    (validation?.errors?.length > 0) ||
    (validation?.warnings?.length > 0)
  );
  // Three hundred errors show the first ten; the rest are one press away.
  const errs = cap(10);
  const warns = cap(10);
</script>

{#if hasContent}
  <CollapsibleSection title="Validation" bind:open {id}>
    {#if stages?.length > 0}
      <ul class="stages" aria-label="Validation stages" data-testid="validation-stages">
        {#each stages as st (st.name)}{@const b = stageBadge(st)}<li><IdentityBadge label={b.label} tone={b.tone} title={b.title} /></li>{/each}
      </ul>
    {/if}
    {#if validation?.errors?.length > 0}
      <div class="subsection">
        <h3 style="color:var(--c-err)">Errors</h3>
        {#each errs.slice(validation.errors) as issue, i (i)}
          <div class="insight insight-critical">
            <code>{issue.path}</code> <strong>[{issue.code}]</strong> {issue.message}
          </div>
        {/each}
        <ShowAll cap={errs} total={validation.errors.length} noun="errors" />
      </div>
    {/if}
    {#if validation?.warnings?.length > 0}
      <div class="subsection">
        <h3 style="color:var(--c-warn)">Warnings</h3>
        {#each warns.slice(validation.warnings) as issue, i (i)}
          <div class="insight insight-warning">
            <code>{issue.path}</code> <strong>[{issue.code}]</strong> {issue.message}
          </div>
        {/each}
        <ShowAll cap={warns} total={validation.warnings.length} noun="warnings" />
      </div>
    {/if}
  </CollapsibleSection>
{:else}
  <SectionState title="Validation" bind:open {id} />
{/if}

<style>
  .stages { list-style: none; margin: 0; padding: 0; display: flex; gap: var(--sp-1); flex-wrap: wrap; }
  .subsection { margin-top: var(--sp-4); }
  .subsection h3 { margin-bottom: var(--sp-2); }
</style>
