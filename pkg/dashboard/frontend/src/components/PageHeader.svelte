<script>
  import { kindLabel } from '../lib/entityLabels.ts';
  import { abbreviateDigests } from '../lib/format.ts';
  import { identityContext, primaryLabel } from '../lib/identityContext.ts';
  import EntityStatusBadge from './EntityStatusBadge.svelte';

  // The ONE page/entity header in the product (requirement 9).
  //
  // Entity pages used to have no visible page title at all: the h1 was
  // visually-hidden for the accessibility tree, and the visible identity was the
  // same inline `EntityIdentity` a list row uses -- 14px, weight 600, which is
  // exactly what every section title on the page rendered at. The page was
  // outranked by its own contents, and a reader arriving from a list had nothing
  // that said "you are HERE now".
  //
  // Fixing that by enlarging EntityIdentity was not an option: the same component
  // renders inline in tables, cards and drawers, where a page-size label would be
  // absurd. So the page-level treatment lives here, and the two share the
  // disambiguation logic (lib/identityContext) rather than the styling.
  //
  // `ref` is a product EntityRef and drives kind, name and disambiguating context.
  // `title` overrides the name for non-entity pages (Overview, Services, ...), which
  // pass no ref at all and just want the one page-title grammar.
  //
  // `titlePrefix` is announced and titled but not painted. The kind is visible in the
  // eyebrow, so repeating "Service:" in the visible title would be the same word twice
  // -- but `document.title` is mirrored from the h1 (lib/pageTitle), and a tab reading
  // "payments-service" without its kind is ambiguous among ten Pacto tabs.
  //
  // `count` is the population this page is showing ("47 contract revisions"), already
  // pluralized by the caller because only the caller knows the noun. Every list route
  // had grown its own markup and class for it (`sv-total`, `lv-total`, `av-total`), so
  // the same fact sat at three sizes in three places (requirement 18).
  let {
    ref = null,
    title = '',
    titlePrefix = '',
    kind = '',
    status = '',
    showStatus = true,
    subtitle = '',
    eyebrow = '',
    count = '',
    countTestid = '',
    actions = [],
    children,
  } = $props();

  const heading = $derived(title || primaryLabel(ref));
  const shownKind = $derived(kind || ref?.kind || '');
  const context = $derived(ref ? identityContext(ref) : []);
  const shownStatus = $derived(status || ref?.status || '');
</script>

<header class="page-hd">
  {#if shownKind || eyebrow || context.length}
    <p class="page-hd-eyebrow t-label">
      {#if eyebrow}{eyebrow}{:else if shownKind}{kindLabel(shownKind)}{/if}
      {#each context as bit}<span class="page-hd-dot" aria-hidden="true">·</span><span class="page-hd-ctx" title={abbreviateDigests(bit) === bit ? undefined : bit}>{abbreviateDigests(bit)}</span>{/each}
    </p>
  {/if}

  <div class="page-hd-title-row">
    <h1 class="t-page-title" data-testid="page-title">{#if titlePrefix}<span class="visually-hidden">{`${titlePrefix}: `}</span>{/if}{heading}</h1>
    {#if showStatus && shownStatus}
      <EntityStatusBadge kind={shownKind} status={shownStatus} />
    {/if}
    {#if count}<span class="page-hd-count t-meta" data-testid={countTestid || undefined}>{count}</span>{/if}
  </div>

  {#if subtitle}<p class="page-hd-sub t-body-2">{subtitle}</p>{/if}

  {#if actions.length}
    <div class="page-hd-actions">
      {#each actions as act}<a class="page-hd-action" href={act.href}>{act.label}</a>{/each}
    </div>
  {/if}

  {@render children?.()}
</header>

<style>
  /* Whitespace, not another border, is what separates the page from its first
     section (requirement 10): the header owns a generous bottom gap and nothing
     else on the page is allowed to sit that far apart. */
  .page-hd { display: flex; flex-direction: column; gap: var(--sp-2); margin-bottom: var(--sp-3); }
  .page-hd-eyebrow { margin: 0; display: flex; align-items: center; gap: var(--sp-1); flex-wrap: wrap; }
  .page-hd-dot { color: var(--c-text-3); }
  .page-hd-ctx { text-transform: none; letter-spacing: normal; overflow-wrap: anywhere; }
  .page-hd-title-row { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; }
  .page-hd-title-row :global(h1) { margin: 0; }
  .page-hd-count { font-variant-numeric: tabular-nums; }
  .page-hd-sub { margin: 0; max-width: 72ch; }
  .page-hd-actions { display: flex; gap: var(--sp-2); flex-wrap: wrap; margin-top: var(--sp-1); }
  .page-hd-action {
    text-decoration: none; font-size: var(--text-sm); color: var(--c-accent);
    border: 1px solid var(--c-accent-border); background: var(--c-accent-bg);
    padding: 4px 12px; border-radius: var(--radius-sm);
    min-height: var(--touch-min); display: inline-flex; align-items: center;
  }
  .page-hd-action:hover { border-color: var(--c-accent); }
</style>
