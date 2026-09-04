<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import MarkdownView from '../MarkdownView.svelte';
  import { methodClass } from '../lib/format.ts';

  let { capabilities = [], skills = [], open = $bindable(false), id = '', source = '' } = $props();

  let count = $derived((capabilities?.length || 0) + (skills?.length || 0));
  let hasContent = $derived(count > 0);
  let expanded = $state({});

  function toggleSkill(i) {
    expanded = { ...expanded, [i]: !expanded[i] };
  }
</script>

{#if hasContent}
  <CollapsibleSection title="Agent Capabilities" {count} bind:open {id} {source}>
    {#if capabilities?.length > 0}
      <div class="table-wrap">
      <table class="cap-table">
        <thead><tr><th>Method</th><th>Path</th><th>Tool</th><th>Summary</th></tr></thead>
        <tbody>
          {#each capabilities as tool}
            <tr>
              <td><span class="badge {methodClass(tool.method)}">{tool.method}</span></td>
              <td><code>{tool.path}</code></td>
              <td>
                <code class="tool-name">{tool.name}</code>
                {#if tool.mutating}
                  <span class="badge badge-warn" data-tip="Mutating operation — exposed to agents only with --allow-writes">write</span>
                {/if}
              </td>
              <td class="muted">{tool.summary || ''}</td>
            </tr>
          {/each}
        </tbody>
      </table>
      </div>
    {/if}

    {#if skills?.length > 0}
      <div class="skills">
        <h4>Skills</h4>
        {#each skills as skill, i}
          <div class="detail-card">
            <button type="button" class="detail-card-header" onclick={() => toggleSkill(i)}>
              <span class="expand-icon" data-motion class:open={expanded[i]}>
                <svg viewBox="0 0 12 12" fill="none"><path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </span>
              <span class="pill pill-skill">skill</span>
              <span class="skill-name">{skill.name}</span>
            </button>
            {#if expanded[i]}
              <div class="detail-card-body">
                <MarkdownView content={skill.content} />
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </CollapsibleSection>
{/if}

<style>
  .cap-table { margin-bottom: var(--sp-2); }
  .cap-table th { font-size: var(--text-xs); }
  .tool-name { font-weight: 600; }
  .muted { color: var(--c-text-2); }

  .skills { margin-top: var(--sp-3); }
  .skills h4 { margin-bottom: var(--sp-2); font-size: var(--text-sm); font-weight: 600; }

  .detail-card {
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    background: var(--c-surface);
    margin-bottom: var(--sp-2);
  }
  .detail-card-header {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    width: 100%;
    padding: var(--sp-3);
    background: none;
    border: none;
    font: inherit;
    color: var(--c-text);
    text-align: left;
    cursor: pointer;
  }
  .detail-card-header:hover { background: var(--c-surface-hover, var(--c-surface-inset)); border-radius: var(--radius-sm); }
  .expand-icon {
    display: inline-flex;
    color: var(--c-text-3);
    transform: rotate(-90deg);
    flex-shrink: 0;
  }
  .expand-icon.open { transform: rotate(0deg); }
  .expand-icon svg { width: 12px; height: 12px; }
  .pill-skill { background: var(--c-accent-bg); color: var(--c-accent); font-size: var(--text-xs); flex-shrink: 0; }
  .skill-name { font-weight: 600; }
  .detail-card-body { padding: 0 var(--sp-3) var(--sp-3); }
</style>
