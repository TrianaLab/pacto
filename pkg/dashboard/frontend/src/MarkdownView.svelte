<script>
  import { renderMarkdown } from './lib/markdown.ts';
  import DOMPurify from 'dompurify';

  let { content = '', truncated = false } = $props();
  let html = $derived(renderMarkdown(content));
  let container = $state(null);

  // Mermaid is heavy (~MBs) and only needed for docs that contain diagrams, so
  // it is dynamically imported the first time a `mermaid` code block is seen.
  let mermaidPromise = null;
  function loadMermaid() {
    if (!mermaidPromise) {
      mermaidPromise = import('mermaid').then((m) => {
        const mermaid = m.default;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: 'strict', // no JS, no click handlers
          // Render labels as native SVG <text> rather than HTML in <foreignObject>:
          // the sanitized-SVG output keeps <text>/<tspan>, but would strip foreignObject
          // HTML, leaving empty shapes. htmlLabels:false also keeps <br/> as line breaks.
          htmlLabels: false,
          flowchart: { htmlLabels: false },
          theme: 'neutral',
          fontFamily: 'inherit',
        });
        return mermaid;
      });
    }
    return mermaidPromise;
  }

  // Monotonic guard so a render kicked off for stale content bails out once the
  // doc changes (the {@html} swap replaces the whole subtree underneath us).
  let renderSeq = 0;

  async function renderMermaid(root) {
    const blocks = root.querySelectorAll('code.language-mermaid');
    if (blocks.length === 0) return;
    const seq = ++renderSeq;
    let mermaid;
    try {
      mermaid = await loadMermaid();
    } catch {
      return; // mermaid failed to load; leave the source code blocks as-is
    }
    if (seq !== renderSeq) return;

    let i = 0;
    for (const code of blocks) {
      if (seq !== renderSeq) return;
      const pre = code.closest('pre') ?? code;
      const src = code.textContent ?? '';
      const id = `mermaid-${seq}-${i++}`;
      try {
        const { svg } = await mermaid.render(id, src);
        if (seq !== renderSeq) return;
        // Output SVG comes from semi-trusted bundle docs — sanitize before inject.
        const safe = DOMPurify.sanitize(svg, {
          USE_PROFILES: { svg: true, svgFilters: true },
          ADD_TAGS: ['style'],
        });
        const wrap = document.createElement('div');
        wrap.className = 'mermaid-diagram';
        wrap.innerHTML = safe;
        pre.replaceWith(wrap);
      } catch {
        // Invalid diagram: keep the original code block and remove mermaid's
        // stray error node (it appends `#d<id>` to <body> on parse failure).
        document.getElementById(`d${id}`)?.remove();
      }
    }
  }

  $effect(() => {
    html; // re-run whenever the rendered doc changes
    if (container) renderMermaid(container);
  });
</script>

{#if truncated}
  <div class="md-truncated">Truncated for display — open the file in the bundle for the full document.</div>
{/if}
<div class="markdown-body" bind:this={container}>{@html html}</div>

<style>
  .markdown-body {
    font-size: var(--text-sm);
    line-height: 1.6;
    color: var(--c-text);
    overflow-wrap: anywhere;
  }
  .markdown-body :global(h1),
  .markdown-body :global(h2),
  .markdown-body :global(h3) {
    margin: 0.7em 0 0.35em;
    line-height: 1.25;
  }
  .markdown-body :global(h1) { font-size: var(--text-lg, 1.1rem); }
  .markdown-body :global(p) { margin: 0.4em 0; }
  .markdown-body :global(ul),
  .markdown-body :global(ol) { margin: 0.4em 0; padding-left: 1.4em; }
  .markdown-body :global(pre) {
    background: var(--c-surface-inset);
    padding: var(--sp-2);
    border-radius: var(--radius-xs);
    overflow-x: auto;
  }
  .markdown-body :global(code) { font-size: var(--text-xs); }
  .markdown-body :global(table) { border-collapse: collapse; margin: 0.4em 0; }
  .markdown-body :global(th),
  .markdown-body :global(td) { border: 1px solid var(--c-border); padding: 3px 8px; }
  .markdown-body :global(a) { color: var(--c-accent); }
  .markdown-body :global(.mermaid-diagram) {
    margin: 0.8em 0;
    padding: var(--sp-2);
    background: var(--c-surface-inset);
    border-radius: var(--radius-xs);
    overflow-x: auto;
    text-align: center;
  }
  .markdown-body :global(.mermaid-diagram svg) {
    max-width: 100%;
    height: auto;
  }
  .md-truncated {
    font-size: var(--text-xs);
    color: var(--c-warn);
    margin-bottom: var(--sp-2);
  }
</style>
