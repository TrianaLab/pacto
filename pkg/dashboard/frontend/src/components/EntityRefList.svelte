<script>
  import EntityLink from './EntityLink.svelte';
  import TargetEnv from './TargetEnv.svelte';
  import { formatDate } from '../lib/dateFormat.ts';

  // Renders a list of product EntityRefs as canonical EntityLink rows. Every rich
  // entity page's related-entity previews (revisions, deployments, dependencies,
  // dependents, targets, contributed entities, owner's entities) route through this
  // one list, so related-entity linking is never reinvented.
  // showKind=false where the section title already names the kind (a narrow row has no
  // room to say it twice).
  // env: rows are targets, laid out in the columns the target list uses, with the
  // environment as its own cell (the columns ellipsize, so `clip` is not needed).
  // showDate: a revision row says when a source fetched it, when one did. Revision rows
  // keep wrapping, since their version is at the end of the name.
  let { items = [], showStatus = true, showKind = true, clip = false, env = false, showDate = false } = $props();
</script>

<ul class="ref-list" class:ref-clip={clip && !env} class:pl-list={env} class:pl-list--cols={env}>
  {#each items as ref (ref.kind + '::' + ref.key)}
    <li class="ref-row" class:pl-row={env}>
      {#if env}
        <EntityLink {ref} {showStatus} {showKind}><TargetEnv env={ref.environment} /></EntityLink>
      {:else}
        <EntityLink {ref} {showStatus} {showKind} />
      {/if}
      {#if showDate && ref.fetchedAt}<span class="ref-date">Fetched {formatDate(ref.fetchedAt)}</span>{/if}
    </li>
  {/each}
</ul>

<style>
  .ref-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .ref-row {
    padding: var(--sp-1) var(--sp-2); border-radius: var(--radius-xs);
  }
  .ref-row:hover { background: var(--c-surface-hover); }
  .ref-date { margin-left: var(--sp-2); font-size: var(--text-xs); color: var(--c-text-3); white-space: nowrap; }
  /* clip: a long name keeps one line and clips; the whole name is in its title and in
     the link's accessible name. Only for names whose start tells them apart (a target
     leads with its scope); a revision's version is at the end, so revisions wrap.
     Three classes beat EntityIdentity's own label rule. */
  .ref-clip .ref-row :global(.entity-link) { display: flex; max-width: 100%; }
  .ref-clip .ref-row :global(.entity-identity) { display: flex; flex-wrap: nowrap; max-width: 100%; }
  .ref-clip .ref-row :global(.ei-kind), .ref-clip .ref-row :global(.ei-status) { flex-shrink: 0; }
  .ref-clip .ref-row :global(.ei-label) { min-width: 0; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
