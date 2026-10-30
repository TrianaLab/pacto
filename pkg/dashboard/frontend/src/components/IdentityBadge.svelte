<script>
  // A single-source status/identity pill. Every rich entity page renders its
  // status, revision-match certainty, content-retrievability and source-health
  // states through this one badge, so the `.tag`/tone markup is never reinvented
  // per page. Labels/tones come from lib/entityLabels.ts.
  // `describe` repeats a title that says more than the label as visually hidden text,
  // so a screen reader or a touch screen reaches what a hover title alone would hide.
  let { label = '', tone = 'neutral', title = '', describe = false } = $props();
</script>

<span class="tag tone-{tone}" {title}>{label}</span>{#if describe && title}<span class="visually-hidden">{title}</span>{/if}

<style>
  .tag {
    display: inline-block; font-size: var(--text-xs); font-weight: 600; padding: 2px 8px;
    border-radius: var(--radius-xs); color: var(--tone-c, var(--c-text));
    background: var(--c-surface-inset); border: 1px solid var(--tone-c, var(--c-border));
  }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-info { --tone-c: var(--c-info); }
  .tone-neutral { --tone-c: var(--c-neutral); }
</style>
