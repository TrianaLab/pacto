<script>
  import HelpTip from './HelpTip.svelte';

  // THE section grammar (requirement 12). Every titled block on a product page is one
  // of these, so a reader learns the shape once: title, count, tone, an optional
  // one-line definition, an optional closed-state summary, and -- for detail a reader
  // does not always need -- a disclosure.
  //
  // It is also the bounded-preview renderer it started as (requirement D/K/B). It
  // distinguishes an EXACT total that is KNOWN from one that is UNKNOWN: some bounded
  // backend answers deliberately omit the total because it cannot be counted without
  // violating the work bound (RelationshipsPreview / RuntimePreview from an already-
  // truncated neighborhood or an early-stopped runtime walk). When the total is known
  // it shows count-of-total; when it is unknown it NEVER synthesizes one from count,
  // scanned, page size or any other bound -- a truncated preview with an unknown total
  // says "more exist; total unknown", never "X of X".
  //
  // total: a number is the EXACT known total; null/undefined means the exact total is
  // UNKNOWN (callers must pass the raw backend value, never a `?? count` fallback).
  //
  // `level` is the heading LEVEL this section's title occupies in the page outline.
  // It exists because a preview nested inside another titled block is a subsection of
  // it, and hard-coding h2 everywhere produced pages where an h2 sat inside an h2 and
  // a screen-reader outline claimed siblings that were parent and child.
  //
  // `role` is the VISUAL role, and it is a SEPARATE dimension from `level`. A section
  // nested for outline reasons still looks like a section; only a genuinely subordinate
  // block inside one asks for 'subsection'. Neither prop sets a font size directly --
  // both resolve to the shared typography roles.
  //
  // `collapsible` + `open`: requirement 13's default-open policy is INFORMATIONAL, not
  // aesthetic. The caller decides, because only the caller knows whether this block is
  // an active failure (never collapsed) or a full operation list (collapsed until
  // asked for). Collapsing uses a real <details>, so collapsed content leaves the tab
  // order and the state is exposed to assistive tech without any ARIA of our own.
  //
  // `summary`: the one line a reader gets while the section is CLOSED. A disclosure
  // whose closed state says nothing forces everyone to open everything.
  let {
    title = '',
    level = 2,
    role = 'section',
    tone = '',
    total = null,
    count = 0,
    truncated = false,
    viewAllHref = '',
    viewAllLabel = 'View all',
    empty = 'None.',
    collapsible = false,
    open = true,
    summary = '',
    help = '',
    children,
  } = $props();

  const totalKnown = $derived(typeof total === 'number' && Number.isFinite(total));
  const titleClass = $derived(role === 'subsection' ? 't-subsection-title' : 't-section-title');
  const countText = $derived(totalKnown ? `${count} of ${total}` : `${count}`);

  // Every top-level section on a page is addressable, so the shared "On this page"
  // navigator can DISCOVER it instead of a page hand-listing its own contents and
  // drifting. Only level 2: a preview nested inside another titled block is a
  // subsection of it, and listing it as a peer would misdescribe the page outline.
  const tocId = $derived(`sec-${title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')}`);
  const toc = $derived(level === 2 && title ? { id: tocId, 'data-toc': title } : {});
</script>

{#snippet head(withHelp)}
  <svelte:element this={`h${level}`} class={titleClass}>{title}</svelte:element>
  {#if help && withHelp}<HelpTip label={title} text={help} />{/if}
  <span class="ps-head-end">
    {#if summary}<span class="ps-summary t-meta">{summary}</span>{/if}
    {#if count > 0}
      <span class="ps-count t-meta" data-testid="preview-count">{countText}</span>
    {/if}
  </span>
{/snippet}

{#snippet body(leadHelp)}
  {#if leadHelp}<p class="ps-help t-body-2">{help}</p>{/if}
  {#if count === 0}
    <p class="ps-empty t-body-2">{empty}</p>
  {:else}
    {@render children?.()}
    {#if truncated}
      <p class="ps-more t-body-2" data-testid="preview-more">
        {#if totalKnown}Showing {count} of {total}.{:else}Showing {count}. More exist; total unknown.{/if}
        {#if viewAllHref}<a href={viewAllHref}>{viewAllLabel}</a>{/if}
      </p>
    {/if}
  {/if}
{/snippet}

{#if collapsible}
  <!-- The help text moves INTO the panel here rather than sitting beside the title as a
       tip. A `<summary>` is itself the disclosure control, and a second button inside it
       would be an interactive element nested in an interactive element: the help click
       would also toggle the section. Requirement 14 asks for substantive explanation
       behind an expandable disclosure anyway, and this section already is one. -->
  <details class="ps disclosure ps-collapsible" class:ps-toned={tone} data-tone={tone} data-testid="preview-section" {open} {...toc}>
    <summary class="ps-head">
      <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
      {@render head(false)}
    </summary>
    <div class="ps-panel">{@render body(!!help)}</div>
  </details>
{:else}
  <section class="ps" class:ps-toned={tone} data-tone={tone} data-testid="preview-section" {...toc}>
    <div class="ps-head">{@render head(true)}</div>
    {@render body(false)}
  </section>
{/if}

<style>
  .ps { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); }
  /* Tone is a left edge, not a fill: a section that needs attention has to be findable
     while scrolling without turning the page into a colour chart. */
  .ps-toned { border-left-width: 3px; }
  .ps-toned[data-tone='err'] { border-left-color: var(--c-err); }
  .ps-toned[data-tone='warn'] { border-left-color: var(--c-warn); }
  .ps-toned[data-tone='ok'] { border-left-color: var(--c-ok); }
  .ps-toned[data-tone='info'] { border-left-color: var(--c-info); }

  .ps-head { display: flex; align-items: baseline; gap: var(--sp-2); flex-wrap: wrap; }
  .ps-head-end { display: flex; align-items: baseline; gap: var(--sp-3); flex-wrap: wrap; margin-left: auto; }
  .ps-count, .ps-empty, .ps-more, .ps-help { margin: 0; }
  .ps-help { margin-bottom: var(--sp-3); max-width: 72ch; }
  .ps-more { margin-top: var(--sp-2); }
  .ps-more a { color: var(--c-accent); text-decoration: none; }
  .ps-more a:hover { text-decoration: underline; }

  /* The gap between a section title and its content is the same everywhere, whether the
     section opens or is always visible. */
  .ps > .ps-head { margin-bottom: var(--sp-3); }
  .ps-panel { margin-top: var(--sp-3); }

  /* Layout, caret and hit area come from the shared `.disclosure` in components.css --
     this is the same control as every other product disclosure, not a second one. Only
     what is specific to a SECTION is set here. */
  .ps-collapsible > summary { list-style: none; flex-wrap: wrap; }
  /* The shared summary is quiet grey, which is right for "Identifier" and wrong for a
     section title: a section that happens to collapse must not rank below one that does
     not. The title keeps full text colour; the count and closed-state line stay quiet. */
  .ps-collapsible > summary :global(.t-section-title),
  .ps-collapsible > summary :global(.t-subsection-title) { color: var(--c-text); }
  .ps-collapsible > summary:hover :global(.t-section-title),
  .ps-collapsible > summary:hover :global(.t-subsection-title) { color: var(--c-accent); }
  /* Closed, the one-line gist stands in for the panel; open, the panel says it better. */
  .ps-collapsible[open] > summary .ps-summary { display: none; }
</style>
