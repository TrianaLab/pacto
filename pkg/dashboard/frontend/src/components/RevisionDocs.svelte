<script>
  import { api } from '../lib/api.ts';
  import PreviewSection from './PreviewSection.svelte';
  import MarkdownView from '../MarkdownView.svelte';
  import DocModal from '../DocModal.svelte';

  // The revision's bundle documentation, readable rather than merely listed.
  //
  // A body is fetched LAZILY, one document at a time, keyed by the CANONICAL revision
  // key plus the exact path this revision published. That keeps the entity response
  // bounded -- a revision with 200 docs costs the same to open as one with a single
  // doc -- and it keeps the read attributed to one immutable revision, so two
  // same-named services in different domains can never read each other's docs.
  //
  // Rendering goes through the shared MarkdownView, which is what restores formatted
  // prose and Mermaid diagrams instead of a path and a title.
  let { revisionKey = '', docs = null } = $props();

  const items = $derived(docs?.items ?? []);

  // path -> { content } | { error }. A path absent from the map has not been asked
  // for yet; a path present with neither field is in flight.
  let bodies = $state({});
  let modalPath = $state('');

  const modalDoc = $derived(
    modalPath && bodies[modalPath]?.content != null
      ? { title: titleFor(modalPath), path: modalPath, content: bodies[modalPath].content }
      : null,
  );

  function titleFor(path) {
    return items.find((d) => d.path === path)?.title || path;
  }

  async function read(path, retry = false) {
    if (!retry && bodies[path]) return; // already read, in flight, or failed
    bodies = { ...bodies, [path]: {} };
    try {
      const res = await api.fleetRevisionDocument(revisionKey, path);
      bodies = { ...bodies, [path]: { content: res.document?.content ?? '' } };
    } catch (e) {
      // The backend states WHY a document is unavailable (oversized, unreadable, not
      // text). Showing that sentence is the honest answer; an empty reading pane
      // would read as "this document is blank".
      bodies = { ...bodies, [path]: { error: e?.message || 'This document could not be read.' } };
    }
  }
</script>

<PreviewSection
  title="Docs"
  total={docs?.total ?? 0}
  count={docs?.count ?? 0}
  truncated={docs?.truncated}
  empty="This revision publishes no documentation."
>
  <ul class="rd-list">
    {#each items as doc (doc.path)}
      {@const body = bodies[doc.path]}
      <li>
        <details
          class="disclosure rd-doc"
          ontoggle={(e) => { if (e.currentTarget.open) read(doc.path); }}
        >
          <summary>
            <span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span>
            <span class="rd-title">{doc.title || doc.path}</span>
            {#if doc.title && doc.path}<code class="rd-path">{doc.path}</code>{/if}
          </summary>
          <div class="rd-body">
            {#if body?.error}
              <p class="rd-error" role="alert">{body.error}</p>
              <button type="button" class="rd-retry" onclick={() => read(doc.path, true)}>Try again</button>
            {:else if body?.content != null}
              <div class="rd-tools">
                <button type="button" class="rd-full" onclick={() => { modalPath = doc.path; }}>
                  Read full screen
                </button>
              </div>
              <MarkdownView content={body.content} />
            {:else}
              <p class="rd-loading" role="status">Reading {doc.path}…</p>
            {/if}
          </div>
        </details>
      </li>
    {/each}
  </ul>
</PreviewSection>

<DocModal doc={modalDoc} onClose={() => { modalPath = ''; }} />

<style>
  .rd-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .rd-doc { border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); padding: var(--sp-2) var(--sp-3); }
  .rd-title { font-weight: 600; }
  .rd-path { font-size: var(--text-xs); color: var(--c-text-3); }
  .rd-body { padding-top: var(--sp-2); }
  .rd-tools { display: flex; justify-content: flex-end; }
  .rd-full, .rd-retry { font: inherit; font-size: var(--text-sm); color: var(--c-accent); background: none; border: none; padding: 0; text-decoration: underline; cursor: pointer; }
  .rd-loading { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .rd-error { margin: 0 0 var(--sp-1); font-size: var(--text-sm); color: var(--c-warn); }
</style>
