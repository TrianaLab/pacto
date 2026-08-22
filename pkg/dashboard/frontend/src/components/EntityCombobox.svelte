<script>
  import { onDestroy, untrack } from 'svelte';
  import { createEntitySuggest } from '../lib/entitySuggest.svelte.ts';
  import EntityIdentity from './EntityIdentity.svelte';

  // An inline search-as-you-type field over the product Entities query: the ARIA 1.2
  // combobox pattern on the input, a listbox of canonical entity suggestions beneath it.
  //
  // It is a SUGGESTION surface, not a filter that runs itself. Typing only asks the
  // backend what exists; the filter or the navigation is committed on an explicit
  // choice -- picking a suggestion, pressing Enter or submitting the form -- so a
  // keystroke never writes a browser history entry. Every suggestion carries the
  // backend's canonical key and href plus enough identity to tell two same-named
  // services in different domains apart, and nothing here is inferred from a label.
  //
  // The debounce, the bound and the stale-response guard are NOT reimplemented here:
  // they come from the shared createEntitySuggest, the same mechanics the global search
  // palette uses.
  let {
    id,
    value = $bindable(''),
    kinds = undefined,
    limit = 8,
    placeholder = '',
    label = 'Search',
    testid = 'combobox',
    onselect = null,
    // When set, Enter with no highlighted suggestion commits the typed text through
    // this callback instead of falling through to the enclosing form's submit. Fields
    // that live inside a <form> leave it unset and keep ordinary submit behaviour.
    oncommit = null,
  } = $props();

  // The searched kinds and the bound are fixed for the life of one combobox -- a field
  // that suggests services does not become a field that suggests owners -- so the
  // suggester is built once, deliberately outside reactivity.
  const suggest = untrack(() => createEntitySuggest({ kinds, limit }));
  onDestroy(() => suggest.destroy());

  let openList = $state(false);
  let activeIdx = $state(-1);

  const results = $derived(suggest.results);
  const query = $derived((value ?? '').trim());
  const expanded = $derived(openList && query.length > 0);
  const listId = $derived(`${id}-list`);
  const activeId = $derived(activeIdx >= 0 && activeIdx < results.length ? `${id}-opt-${activeIdx}` : undefined);

  // Announced to a screen reader only once a request has settled, so the count read
  // aloud is the count on screen.
  const liveMsg = $derived(
    !expanded || suggest.loading ? ''
      : results.length ? `${results.length} suggestion${results.length === 1 ? '' : 's'}`
        : 'No suggestions',
  );

  function onInput(e) {
    value = e.currentTarget.value;
    suggest.search(value);
    openList = true;
    activeIdx = -1;
  }

  function close() { openList = false; activeIdx = -1; }

  function choose(r) {
    close();
    // Adopt the chosen entity's own label. A `change` event that follows (the browser
    // fires one when a modified input loses focus) then carries the value the choice
    // already committed, so it re-commits the same thing instead of overwriting the
    // choice with whatever fragment was typed.
    if (r.label) value = r.label;
    onselect?.(r);
  }

  function onKeydown(e) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (results.length) activeIdx = openList ? (activeIdx + 1) % results.length : 0;
      openList = true;
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (results.length) activeIdx = activeIdx <= 0 ? results.length - 1 : activeIdx - 1;
      openList = true;
    } else if (e.key === 'Enter') {
      if (expanded && activeIdx >= 0 && results[activeIdx]) {
        e.preventDefault();
        choose(results[activeIdx]);
        return;
      }
      close();
      if (oncommit) { e.preventDefault(); oncommit(query); }
    } else if (e.key === 'Escape') {
      // Only swallow Escape while there is a popup to dismiss; otherwise leave it to
      // the browser (a search input clears itself) and to any enclosing dialog.
      if (expanded) { e.preventDefault(); e.stopPropagation(); close(); }
    }
  }

  function onBlur() { close(); }
  function onChange(e) { oncommit?.(e.currentTarget.value.trim()); }
</script>

<div class="cb">
  <input
    {id}
    type="search"
    class="input"
    role="combobox"
    aria-expanded={expanded}
    aria-controls={expanded ? listId : undefined}
    aria-autocomplete="list"
    aria-activedescendant={activeId}
    autocomplete="off"
    aria-label={label}
    data-testid={testid}
    {placeholder}
    {value}
    oninput={onInput}
    onkeydown={onKeydown}
    onblur={onBlur}
    onchange={onChange}
  />
  {#if expanded}
    <div class="cb-list" id={listId} role="listbox" aria-label={`${label} suggestions`} data-testid={`${testid}-list`}>
      {#if suggest.loading}
        <div class="cb-msg">Searching…</div>
      {:else if suggest.error}
        <div class="cb-msg cb-err">Suggestions unavailable.</div>
      {:else if results.length === 0}
        <div class="cb-msg" data-testid={`${testid}-empty`}>No matches for "{query}".</div>
      {:else}
        {#each results as r, i (r.kind + '::' + r.key)}
          <button
            type="button"
            id={`${id}-opt-${i}`}
            class="cb-item"
            class:active={i === activeIdx}
            role="option"
            aria-selected={i === activeIdx}
            data-testid={`${testid}-option`}
            data-key={r.key}
            onmousedown={(e) => e.preventDefault()}
            onmouseenter={() => { activeIdx = i; }}
            onclick={() => choose(r)}
          >
            <EntityIdentity ref={r} showStatus={false} showKind={false} />
          </button>
        {/each}
        {#if suggest.truncated}
          <div class="cb-msg cb-trunc">Showing {results.length} of {suggest.total}. Keep typing to narrow.</div>
        {/if}
      {/if}
    </div>
  {/if}
  <span class="visually-hidden" aria-live="polite">{liveMsg}</span>
</div>

<style>
  .cb { position: relative; flex: 1; min-width: 0; }
  .cb input { width: 100%; }
  .cb-list {
    position: absolute; z-index: 40; top: calc(100% + 4px); left: 0; right: 0;
    max-height: min(50vh, 320px); overflow-y: auto;
    background: var(--c-surface); border: 1px solid var(--c-border);
    border-radius: var(--radius-sm); box-shadow: var(--shadow-md); padding: var(--sp-1);
  }
  .cb-msg { padding: var(--sp-2) var(--sp-3); color: var(--c-text-3); font-size: var(--text-sm); }
  .cb-err { color: var(--c-err); }
  .cb-trunc { font-size: var(--text-xs); }
  .cb-item {
    display: flex; align-items: center; width: 100%; gap: var(--sp-2);
    padding: var(--sp-2) var(--sp-3); border: none; border-radius: var(--radius-xs);
    background: none; color: var(--c-text); font: inherit; text-align: left; cursor: pointer;
    min-height: var(--touch-min);
  }
  .cb-item.active { background: var(--c-surface-hover); }
</style>
