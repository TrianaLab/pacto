<script>
  // "Connect an agent": one collapsed line on Data sources saying where an AI agent
  // reaches this installation over MCP. The host answers installation.mcp: served, the
  // URL is the address this reader reached plus /mcp (the host the chart derives too);
  // not served, the host's own text says where an MCP server lives.
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import { session } from '../lib/session.svelte.ts';

  let { mcp = undefined, origin = location.origin } = $props();

  const DOCS = 'https://pacto.run/latest/mcp-integration/';
  const url = $derived(`${origin}/mcp`);
  // The read-only token the chart's NOTES mint: a pacto-viewer session names its namespace.
  const namespace = $derived(/^system:serviceaccount:([^:]+):pacto-viewer$/.exec(session.data?.user ?? '')?.[1] ?? '<namespace>');
  const snippet = $derived(`{ "mcpServers": { "pacto": { "type": "http", "url": ${JSON.stringify(url)},
    "headers": { "Authorization": "Bearer \${PACTO_TOKEN}" } } } }`);
</script>

{#if mcp}
  <details class="disclosure connect-agent" data-testid="connect-agent">
    <summary>
      <span class="disclosure-caret" data-motion aria-hidden="true"></span>Connect an agent
      <!-- Text only: a control inside a summary is nested interactive content, so the copy control sits in the body. -->
      {#if mcp.served}<span class="ca-url" title={url}><bdi>{url}</bdi></span>{/if}
    </summary>
    {#if mcp.served}
      <p class="t-meta ca-copy">MCP URL <CopyableIdentifier value={url} label="MCP URL" /></p>
      <p class="t-meta">Agents ask Pacto over MCP about context (services, owners and dependencies), the impact of a change and delivery. Mint a read-only token, then add this server to your MCP client (.mcp.json):</p>
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (SPEC T2: a scrolling region takes focus so the keyboard can scroll it) -->
      <pre class="ca-code" role="region" aria-label="MCP client setup" tabindex="0"><code>export PACTO_TOKEN=$(kubectl -n {namespace} create token pacto-viewer --audience pacto-dashboard --duration 1h)

{snippet}</code></pre>
      <p class="t-meta">That token reads the fleet. An agent that proposes changes uses its own ServiceAccount; see <a href={DOCS} target="_blank" rel="noopener">Connect an agent over MCP</a>.</p>
    {:else}
      <p class="t-meta" data-testid="connect-agent-source">{mcp.source}</p>
      <p class="t-meta"><a href={DOCS} target="_blank" rel="noopener">Connect an agent over MCP</a></p>
    {/if}
  </details>
{/if}

<style>
  .connect-agent { margin-block: var(--sp-3); min-inline-size: 0; }
  .connect-agent > summary { min-inline-size: 0; }
  .ca-url {
    min-inline-size: 0; max-inline-size: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    font-family: var(--font-mono); font-size: var(--text-xs); color: var(--c-text-3);
  }
  .ca-copy { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-inline-size: 0; }
  .ca-code {
    overflow-x: auto; max-width: 100%; margin: var(--sp-2) 0;
    padding: var(--sp-2); border-radius: var(--radius-xs);
    background: var(--c-surface-inset); font-family: var(--font-mono); font-size: var(--text-xs);
  }
</style>
