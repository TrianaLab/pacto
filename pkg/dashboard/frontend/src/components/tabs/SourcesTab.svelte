<script>
  import { navigateTo } from '../../lib/stores.js';
  import { extractServiceName } from '../../lib/helpers.js';
  import PhaseBadge from '../PhaseBadge.svelte';
  import SourcePill from '../SourcePill.svelte';

  let { agg } = $props();

  let activeSource = $state(agg?.sources?.[0]?.sourceType || '');

  function switchSource(type) {
    activeSource = type;
  }

  function svcExists(name) {
    // Simple heuristic - if we can navigate to it, it exists
    return true;
  }
</script>

{#if !agg?.sources?.length}
  <div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No source data</div></div>
{:else}
  <div class="source-tab-bar">
    {#each agg.sources as s}
      <button
        class="source-tab-item"
        class:active={s.sourceType === activeSource}
        onclick={() => switchSource(s.sourceType)}
      >
        <SourcePill type={s.sourceType} />
      </button>
    {/each}
  </div>

  {#each agg.sources as src}
    {@const sd = src.service || {}}
    {#if src.sourceType === activeSource}
      <div style="border:1px solid var(--border);border-top:none;border-radius:0 0 var(--radius) var(--radius);padding:20px;background:var(--bg-surface)">
        <div style="margin-bottom:16px;display:flex;align-items:center;gap:12px">
          <SourcePill type={src.sourceType} />
          {#if sd.version}<span class="pill pill-dim">{sd.version}</span>{/if}
          {#if sd.phase}<PhaseBadge phase={sd.phase} />{/if}
        </div>

        {#if sd.runtime}
          <div class="card"><div class="section-label">Runtime</div><table>
            <tbody>
            {#if sd.runtime.workload}<tr><td class="text-dim">Workload</td><td>{sd.runtime.workload}</td></tr>{/if}
            {#if sd.runtime.healthInterface}<tr><td class="text-dim">Health</td><td><code>{sd.runtime.healthInterface}</code></td></tr>{/if}
            </tbody>
          </table></div>
        {/if}

        {#if sd.interfaces?.length}
          <div class="card"><div class="section-label">Interfaces</div><table>
            <tbody>
            {#each sd.interfaces as ifc}
              <tr><td><strong>{ifc.name}</strong></td><td><span class="badge badge-info">{ifc.type || 'http'}</span></td><td>{#if ifc.visibility}<span class="pill pill-dim">{ifc.visibility}</span>{/if}</td></tr>
            {/each}
            </tbody>
          </table></div>
        {/if}

        {#if sd.dependencies?.length}
          <div class="card"><div class="section-label">Dependencies</div><table>
            <tbody>
            {#each sd.dependencies as dep}
              {@const depName = dep.name || extractServiceName(dep.ref)}
              <tr><td>
                <button type="button" class="dep-link" onclick={() => navigateTo('detail', depName)}>{depName}</button>
              </td><td>{#if dep.required}<span class="badge badge-info">required</span>{:else}optional{/if}</td></tr>
            {/each}
            </tbody>
          </table></div>
        {/if}

        {#if sd.resources}
          <div class="card"><div class="section-label">Resources</div><table>
            <tbody>
            {#if sd.resources.serviceExists != null}<tr><td class="text-dim">Service</td><td>{#if sd.resources.serviceExists}<span class="badge badge-ok">found</span>{:else}<span class="badge badge-critical">not found</span>{/if}</td></tr>{/if}
            {#if sd.resources.workloadExists != null}<tr><td class="text-dim">Workload</td><td>{#if sd.resources.workloadExists}<span class="badge badge-ok">found</span>{:else}<span class="badge badge-critical">not found</span>{/if}</td></tr>{/if}
            </tbody>
          </table></div>
        {/if}

        {#if sd.scaling}
          <div class="card"><div class="section-label">Scaling</div><table>
            <tbody>
            {#if sd.scaling.replicas != null}<tr><td class="text-dim">Replicas</td><td><code>{sd.scaling.replicas}</code></td></tr>{/if}
            {#if sd.scaling.min != null}<tr><td class="text-dim">Min</td><td><code>{sd.scaling.min}</code></td></tr>{/if}
            {#if sd.scaling.max != null}<tr><td class="text-dim">Max</td><td><code>{sd.scaling.max}</code></td></tr>{/if}
            </tbody>
          </table></div>
        {/if}

        {#if sd.validation}
          {@const vErrs = sd.validation.errors || []}
          {@const vWarns = sd.validation.warnings || []}
          {#if vErrs.length || vWarns.length}
            <div class="card"><div class="section-label">Validation</div>
              <div style="margin-bottom:8px">{#if sd.validation.valid}<span class="badge badge-ok">valid</span>{:else}<span class="badge badge-critical">invalid</span>{/if}</div>
              <div class="table-wrapper"><table><thead><tr><th>Severity</th><th>Code</th><th>Path</th><th>Message</th></tr></thead><tbody>
                {#each vErrs as e}<tr><td><span class="badge badge-critical">error</span></td><td><code>{e.code}</code></td><td><code>{e.path}</code></td><td>{e.message}</td></tr>{/each}
                {#each vWarns as w}<tr><td><span class="badge badge-warning">warning</span></td><td><code>{w.code}</code></td><td><code>{w.path}</code></td><td>{w.message}</td></tr>{/each}
              </tbody></table></div>
            </div>
          {/if}
        {/if}

        {#if sd.checksSummary}
          <div class="card"><div class="section-label">Checks Summary</div><table>
            <tbody><tr><td class="text-dim">Total</td><td>{sd.checksSummary.total}</td></tr>
            <tr><td class="text-dim">Passed</td><td><span class="count">{sd.checksSummary.passed}</span></td></tr>
            <tr><td class="text-dim">Failed</td><td><span class="count {sd.checksSummary.failed > 0 ? 'count-error' : 'count-zero'}">{sd.checksSummary.failed}</span></td></tr>
          </tbody></table></div>
        {/if}

        {#if sd.endpoints?.length}
          <div class="card"><div class="section-label">Runtime Probes</div>
            <div class="table-wrapper"><table><thead><tr><th>Status</th><th>Probe</th><th>Interface</th><th>URL</th><th>Code</th><th>Latency</th><th>Error</th></tr></thead><tbody>
              {#each sd.endpoints as ep}
                <tr>
                  <td>{#if ep.healthy === true}<span class="badge badge-ok">reachable</span>{:else if ep.healthy === false}<span class="badge badge-critical">failing</span>{:else}<span class="badge badge-neutral">unknown</span>{/if}</td>
                  <td>{#if ep.type}<span class="pill pill-dim">{ep.type}</span>{:else}&mdash;{/if}</td>
                  <td>{ep.interface || ''}</td>
                  <td><code>{ep.url || '\u2014'}</code></td>
                  <td>{#if ep.statusCode != null}<code>{ep.statusCode}</code>{:else}&mdash;{/if}</td>
                  <td>{ep.latencyMs != null ? ep.latencyMs + 'ms' : '\u2014'}</td>
                  <td><span class="text-dim">{ep.error || ep.message || ''}</span></td>
                </tr>
              {/each}
            </tbody></table></div>
          </div>
        {/if}

        {#if sd.ports}
          <div class="card"><div class="section-label">Ports</div><table>
            <tbody>
            {#if sd.ports.expected?.length}<tr><td class="text-dim">Expected</td><td>{#each sd.ports.expected as p}<code>{p}</code>{' '}{/each}</td></tr>{/if}
            {#if sd.ports.observed?.length}<tr><td class="text-dim">Observed</td><td>{#each sd.ports.observed as p}<code>{p}</code>{' '}{/each}</td></tr>{/if}
            {#if sd.ports.missing?.length}<tr><td class="text-dim">Missing</td><td>{#each sd.ports.missing as p}<span class="count count-error"><code>{p}</code></span>{' '}{/each}</td></tr>{/if}
            {#if sd.ports.unexpected?.length}<tr><td class="text-dim">Unexpected</td><td>{#each sd.ports.unexpected as p}<span class="count count-warning"><code>{p}</code></span>{' '}{/each}</td></tr>{/if}
            </tbody>
          </table></div>
        {/if}

        {#if sd.insights?.length}
          <div class="card"><div class="section-label">Insights</div>
            {#each sd.insights as ins}
              <div class="insight-card {({'critical':'insight-critical','warning':'insight-warning','info':'insight-info'})[ins.severity] || 'insight-info'}">
                <div class="insight-body"><div class="insight-title">{ins.title}</div>
                {#if ins.description}<div class="insight-desc">{ins.description}</div>{/if}</div>
              </div>
            {/each}
          </div>
        {/if}

        {#if sd.conditions?.length}
          <div class="card"><div class="section-label">Conditions</div><div class="conditions-grid">
            {#each sd.conditions as c}
              <div class="condition-card"><div class="condition-type">
                <span class="badge {c.status === 'True' ? 'badge-ok' : c.status === 'False' ? 'badge-critical' : 'badge-neutral'}">{c.status}</span> {c.type}
              </div>{#if c.message}<div class="condition-message">{c.message}</div>{/if}</div>
            {/each}
          </div></div>
        {/if}

        {#if !sd.runtime && !sd.interfaces?.length && !sd.dependencies?.length && !sd.resources && !sd.scaling && !sd.validation && !sd.checksSummary && !sd.endpoints?.length && !sd.ports && !sd.insights?.length && !sd.conditions?.length}
          <div style="color:var(--text-dim);font-size:var(--text-sm)">No detailed data from this source.</div>
        {/if}
      </div>
    {/if}
  {/each}
{/if}
