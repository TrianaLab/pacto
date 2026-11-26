<script>
  import { tick } from 'svelte';
  import { api } from '../lib/api.ts';
  import { draftLine, draftMark } from '../lib/declare.ts';
  import { boundsText, closestText, createFieldError, createReturn, createSignUrl, ENV_FIELD, fieldKey, moreText, offeringPicks, offeringRefused, params, perEnvField, perEnvTyped, placeRefusals, signatureText } from '../lib/createService.ts';
  import { zip } from '../lib/zip.ts';
  import { formatCount } from '../lib/format.ts';
  import { hostSignsIn, signIn } from '../lib/session.svelte.ts';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import Fold from './Fold.svelte';
  import ProvenanceMark from './ProvenanceMark.svelte';
  import RefusalNotice from './RefusalNotice.svelte';

  // Create service (515-S6): create a service from the declared golden path pathKey,
  // where the path is already shown. Essentials first (name, owner, namespace and the
  // offerings the path requests); the plan is asked for only on Review, never on page
  // view. The plan shows the version, the repository, who signs and every refusal at
  // its field with its code, the closest value and the CLI and MCP lines. A person
  // signs with a fresh login (the inputs ride across it keyed by the intent) or saves a
  // draft; after a publish the handover is theirs: the commands and a zip of the files.
  // canCreate false (no registry, a read-only host, the doc export) renders no form and
  // the commands (`commands`) expanded, as the page always showed them. With a
  // configured portal (portalCreateURL) "Open in Backstage" leads and the form is second.
  // A created draft's review link names the draft (draftTag), its service (draftName)
  // and its repository when not the default (draftRepository): the form starts with
  // them and Review opens that draft.
  let { pathKey = '', pathName = '', pathVersion = '', requested = [], canCreate = false, portalCreateURL = '', signed = '', draftName = '', draftTag = '', draftRepository = '', commands = undefined } = $props();
  const uid = $props.id();

  let here = $state(false);
  const showForm = $derived(canCreate && (!portalCreateURL || here || !!signed || !!draftTag));
  let name = $state('');
  let owner = $state('');
  let namespace = $state('');
  let repository = $state('');
  let revision = $state('');
  let once = $state({});
  let perEnv = $state({});
  let env = $state('');
  let touched = $state(false);
  const fieldError = $derived(createFieldError(name.trim(), owner.trim(), namespace.trim()));
  // A per-environment value with no environment named would be dropped: refuse it here.
  const envError = $derived(!env.trim() && perEnvTyped(perEnv) ? 'Name the environment these values are for' : '');
  let envTouched = $state(false);

  let answer = $state(null);
  let error = $state(null);
  let loading = $state(false);
  let busy = $state(false);
  let result = $state(null);
  let refusal = $state(null);
  let movedText = $state('');
  let draft = $state(null);
  let planHeading = $state(null);
  let resultBox = $state(null);

  const p = $derived(answer?.plan ?? null);
  const offerings = $derived(answer?.offerings ?? []);
  const shown = $derived(offerings.filter((o) => o.requested));
  const others = $derived(offerings.filter((o) => !o.requested));
  const placed = $derived(placeRefusals(answer?.refusals, offerings));
  const signing = $derived(answer?.signing ?? { mode: 'none' });
  const write = $derived(answer?.write ?? { available: false });
  const fresh = $derived(signing.mode === 'fresh-login');
  const writes = $derived(!!p?.intentDigest && !answer?.refusals?.length && !answer?.refusal && write.available);
  const backSigned = $derived(fresh && !!signed && !!answer?.signedIn && p?.intentDigest === signed && !result && !refusal);
  const signHref = $derived(fresh && writes && answer?.signTicket ? createSignUrl(answer.signTicket, pathKey, p.intentDigest) : '');
  const done = $derived(!!result && !result.draft);
  const drafts = $derived(answer?.drafts ?? []);
  const tunable = $derived(shown.concat(others).flatMap((o) => params(o).filter(perEnvField).map((x) => ({ o, x }))));
  const zipped = $derived(!!result?.files?.length && !result.files.some((f) => f.truncated) && (result.filesTotal ?? result.files.length) <= result.files.length);
  const keepKey = (intent) => `pacto-create:${intent}`;

  // inputs is every request's body: the essentials, the values typed and the draft accepted.
  function inputs() {
    return {
      path: pathKey, name: name.trim(), owner: owner.trim() || undefined, namespace: namespace.trim(),
      repository: repository.trim() || undefined, revision: revision.trim() || undefined,
      offerings: offeringPicks(offerings, once, env.trim(), perEnv), fromDraft: draft || undefined,
    };
  }

  async function load(extra = {}) {
    loading = true;
    error = null;
    try {
      answer = await api.fleetCreateService({ ...inputs(), ...extra });
    } catch (e) {
      answer = null;
      error = e;
    } finally {
      loading = false;
    }
  }

  async function review(e) {
    e?.preventDefault();
    touched = envTouched = true;
    if (fieldError.field || envError || loading) return;
    result = refusal = null;
    movedText = '';
    await load();
    // A review link's draft opens once the plan lists it among the person's drafts.
    const linked = draftTag && !draft ? drafts.find((d) => d.tag === draftTag && d.digest) : null;
    if (linked) {
      draft = { tag: linked.tag, digest: linked.digest };
      await load();
    }
    await tick();
    planHeading?.focus();
  }

  // A review link fills the name and repository it names, once.
  let linkedIn = false;
  $effect(() => {
    if (linkedIn || !draftTag) return;
    linkedIn = true;
    if (!name) name = draftName;
    if (!repository) repository = draftRepository;
  });

  // Back from a signing login: the inputs it was started with, planned again for that intent.
  let resumed = false;
  $effect(() => {
    if (!signed || resumed || !canCreate) return;
    resumed = true;
    let kept = null;
    try { kept = JSON.parse(sessionStorage.getItem(keepKey(signed)) || 'null'); } catch { kept = null; }
    if (!kept) return;
    ({ name, owner, namespace, repository, revision, once, perEnv, env, draft } = kept);
    load({ intentDigest: signed }).then(() => {
      if (!p || !fresh) return;
      if (!answer.signedIn) movedText = 'Your signing login expired or was used: review and sign again';
      else if (p.intentDigest !== signed) movedText = 'The plan changed while you signed in, so sign again';
    });
  });

  function startSign(e) {
    // The planned revision rides along, so the plan after the login is the one signed.
    const kept = { name, owner, namespace, repository, revision: revision.trim() || p.revision, once, perEnv, env, draft };
    try { sessionStorage.setItem(keepKey(p.intentDigest), JSON.stringify(kept)); } catch { /* the person fills the form again */ }
    if (hostSignsIn()) {
      e.preventDefault();
      signIn(createReturn(pathKey, p.intentDigest), true);
    }
  }

  async function send(mode) {
    if (busy || loading) return;
    result = refusal = null;
    movedText = '';
    busy = true;
    try {
      // The write sends back the revision the plan answered, so it publishes the reviewed one.
      const out = await api.fleetCreateServiceAccept({ ...inputs(), revision: revision.trim() || p.revision || undefined, intentDigest: p.intentDigest, mode });
      if (out.refusal?.code === 'PlanStale' && out.plan) {
        answer = out.plan;
        movedText = 'The plan changed since you read it. Read it again';
      } else if (out.refusal || out.refusals?.length) {
        refusal = out.refusal ?? { code: out.refusals[0].code, message: out.refusals[0].message };
      } else {
        result = out;
        try { if (signed) sessionStorage.removeItem(keepKey(signed)); } catch { /* nothing kept */ }
        await tick();
        resultBox?.focus();
      }
    } catch (e) {
      refusal = e;
    } finally {
      busy = false;
    }
  }

  function acceptDraft(d) {
    if (loading) return;
    draft = { tag: d.tag, digest: d.digest };
    review();
  }

  function download() {
    const url = URL.createObjectURL(new Blob([zip(result.files)], { type: 'application/zip' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = `${p?.service || name.trim()}.zip`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  const setOnce = (o, path, v) => { once = { ...once, [o]: { ...once[o], [path]: v } }; };
  const setEnv = (o, path, v) => { perEnv = { ...perEnv, [o]: { ...perEnv[o], [path]: v } }; };
  const inputId = (k) => `${uid}-${encodeURIComponent(k).replace(/%/g, '_')}`;
</script>

{#snippet refusalLine(r)}
  <div class="cs-refusal" data-testid="create-refusal" data-code={r.code}>
    <p class="cs-p"><strong>{r.code}</strong>: <bdi>{r.message}</bdi>{#if closestText(r)}<span class="cs-closest" data-testid="create-closest">, {closestText(r)}</span>{/if}</p>
    {#if r.cli}<div class="cs-cmd"><span class="t-meta">CLI</span> <CopyableIdentifier value={r.cli} label="CLI command" /></div>{/if}
    {#if r.mcp}<div class="cs-cmd"><span class="t-meta">MCP</span> <CopyableIdentifier value={`pacto_create_service ${r.mcp}`} label="MCP arguments" /></div>{/if}
  </div>
{/snippet}

{#snippet valueInput(o, x, k, value, set)}
  {@const at = placed.at[k] ?? []}
  <div class="cs-param" data-testid="create-param" data-field={x.path}>
    {#if x.sensitive}
      <span class="cs-plabel cs-whole"><bdi>{x.path}</bdi> <span class="t-meta cs-bounds" data-testid="create-sensitive">sensitive: set it through a Secret reference, never here</span></span>
    {:else}
      <label class="cs-plabel" for={inputId(k)}><bdi>{x.path}</bdi> <span class="t-meta cs-bounds">{boundsText(x)}{x.tier === 'tunable' ? ', varies per environment' : ''}</span></label>
    {/if}
    {#if !x.sensitive && (x.enum?.length || x.type === 'boolean')}
      <select class="input" id={inputId(k)} value={value ?? ''} onchange={(e) => set(o.offering, x.path, e.currentTarget.value)} aria-invalid={at.length ? 'true' : undefined} aria-describedby={at.length ? `${inputId(k)}-r` : undefined}>
        <option value="">{x.default !== undefined ? `default (${typeof x.default === 'string' ? x.default : JSON.stringify(x.default)})` : 'as the golden path sets it'}</option>
        {#each x.enum?.length ? x.enum : [true, false] as v, i (i)}{@const s = typeof v === 'string' ? v : JSON.stringify(v)}<option value={s}>{s}</option>{/each}
      </select>
    {:else if !x.sensitive}
      <input class="input" id={inputId(k)} value={value ?? ''} oninput={(e) => set(o.offering, x.path, e.currentTarget.value)} inputmode={x.type === 'integer' || x.type === 'number' ? 'decimal' : undefined}
        placeholder={x.default !== undefined ? String(x.default) : ''} autocomplete="off" aria-invalid={at.length ? 'true' : undefined} aria-describedby={at.length ? `${inputId(k)}-r` : undefined} />
    {/if}
    {#if at.length}<div id={`${inputId(k)}-r`} class="cs-at">{#each at as r, i (i)}{@render refusalLine(r)}{/each}</div>{/if}
  </div>
{/snippet}

{#snippet fileText(f, note)}
  <Fold title={f.path} level={5} meta={f.truncated ? (f.text ? 'truncated' : 'not text') : ''} testid="create-file">
    {#if note}<p class="t-meta cs-p">{note}</p>{/if}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (a long line scrolls inside the box; focus lets a keyboard reader scroll it) -->
    <pre class="cs-yaml" tabindex="0" aria-label={f.path}><code>{f.text ?? ''}</code></pre>
  </Fold>
{/snippet}

{#snippet offering(o)}
  <div class="cs-offering" data-testid="create-offering" data-offering={o.offering}>
    <p class="cs-p cs-oname"><bdi>{o.offering}</bdi>{#if o.version}{' '}<span class="t-meta">{o.version}</span>{/if}{#if o.classes?.length}{' '}<span class="t-meta">class {o.classes.join(' | ')}{(o.classesTotal ?? 0) > o.classes.length ? ` (+${o.classesTotal - o.classes.length} more)` : ''}</span>{/if}</p>
    {#each placed.at[fieldKey(o.offering, '')] ?? [] as r, i (i)}{@render refusalLine(r)}{/each}
    {#each params(o) as x (x.path)}
      {@render valueInput(o, x, fieldKey(o.offering, x.path), once[o.offering]?.[x.path], setOnce)}
    {/each}
    {#if moreText(params(o).length, o.parametersTotal, 'fields')}<p class="t-meta cs-p" data-testid="create-more-fields">{moreText(params(o).length, o.parametersTotal, 'fields')}: set them with pacto init --values or pacto_create_service</p>{/if}
    {#if o.guarantees?.length}
      <Fold title="Guarantees" level={5} meta={formatCount(o.guarantees.length)} testid="create-guarantees">
        {#each o.guarantees as g (g.interface)}<p class="cs-p"><bdi>{g.interface}</bdi>: {g.text}</p>{/each}
      </Fold>
    {/if}
  </div>
{/snippet}

<div class="cs" data-testid="create-service">
  {#if !canCreate}
    {@render commands?.()}
  {:else}
    {#if portalCreateURL && !done}
      <div class="cs-actions" data-testid="create-portal">
        <a class="btn btn-sm btn-primary" href={portalCreateURL} target="_blank" rel="noopener noreferrer" data-testid="create-open-portal">Open in Backstage</a>
        {#if !showForm}<button type="button" class="btn btn-sm" onclick={() => (here = true)} data-testid="create-here">Create here</button>{/if}
      </div>
    {/if}
    {#if showForm}
      <form class="cs-form" onsubmit={review} novalidate data-testid="create-form">
        <p class="cs-p cs-path">Golden path <bdi title={pathName}>{pathName}</bdi>{#if pathVersion}{' '}<span class="t-meta">{pathVersion}</span>{/if}</p>
        <label class="cs-field"><span>Name</span><input class="input" bind:value={name} required maxlength="63" autocomplete="off" aria-invalid={touched && fieldError.field === 'name' ? 'true' : undefined} aria-describedby={`${uid}-err`} data-testid="create-name" /></label>
        <label class="cs-field"><span>Owner team</span><input class="input" bind:value={owner} maxlength="253" autocomplete="off" aria-invalid={touched && fieldError.field === 'owner' ? 'true' : undefined} aria-describedby={`${uid}-err`} data-testid="create-owner" /></label>
        <label class="cs-field"><span>Namespace</span><input class="input" bind:value={namespace} required maxlength="63" autocomplete="off" aria-invalid={touched && fieldError.field === 'namespace' ? 'true' : undefined} aria-describedby={`${uid}-err`} data-testid="create-namespace" /></label>
        {#if draftTag && !draft && !result}
          <p class="cs-p cs-wide" data-testid="create-draft-link">Reviewing <bdi>{draftTag}</bdi> of <bdi>{draftName}</bdi>: name the namespace you create it in, then Review.</p>
        {/if}
        {#if requested.length && !offerings.length}
          <p class="cs-p cs-wide t-meta" data-testid="create-requested">Offerings it consumes: {requested.join(', ')}. Review to set their values.</p>
        {/if}
        <div class="cs-wide">
          <Fold title="More details" level={4} testid="create-more">
            <label class="cs-field"><span>Repository <span class="t-meta">(defaults to the registry and the name)</span></span><input class="input" bind:value={repository} maxlength="512" autocomplete="off" data-testid="create-repository" /></label>
            <label class="cs-field"><span>Revision <span class="t-meta">(defaults to pacto-create, you and the time)</span></span><input class="input" bind:value={revision} maxlength="128" autocomplete="off" data-testid="create-revision" /></label>
          </Fold>
        </div>
        <button type="submit" class="btn btn-sm" aria-disabled={loading ? 'true' : undefined} data-testid="create-review">{answer ? 'Review again' : 'Review'}</button>
        <p class="t-meta cs-p cs-wide" id={`${uid}-err`} role={touched && fieldError.message ? 'alert' : undefined}>{touched ? fieldError.message : ''}</p>
      </form>

      {#if loading && !answer}
        <p class="t-meta cs-p">Planning the service…</p>
      {:else if error}
        <RefusalNotice {error} />
      {:else if answer}
        <h4 class="cs-h" tabindex="-1" bind:this={planHeading} data-testid="create-plan-heading">{p ? `Plan: ${p.service} ${p.version}` : 'Nothing can be published as it stands'}</h4>
        {#if answer.refusal}
          <RefusalNotice error={answer.refusal} />
          {#if answer.refusal.command}<div class="cs-cmd" data-testid="create-refusal-command"><span class="t-meta">Next</span> <CopyableIdentifier value={answer.refusal.command} label="command" /></div>{/if}
        {/if}
        {#each placed.rest as r, i (i)}{@render refusalLine(r)}{/each}
        {#if p}
          {#if draft}<p class="cs-p">Accepting <bdi>{draft.tag}</bdi>.</p>{/if}
          <dl class="cs-facts" data-testid="create-plan">
            <div><dt>Golden path</dt><dd><bdi title={p.pathName}>{p.pathName}</bdi> {p.pathVersion}, <span data-testid="create-signature">{signatureText(p.signature)}</span> <ProvenanceMark label="Golden path signature" text={`${p.signature?.detail || 'No signature check ran'}. Pinned at ${p.path}`} /></dd></div>
            <div><dt>Version</dt><dd>{p.version}, contract only, nothing delivered</dd></div>
            <div><dt>Repository</dt><dd><bdi title={p.bundleRepo}>{p.bundleRepo}</bdi></dd></div>
            <div><dt>Signer</dt><dd data-testid="create-signer">{#if done}You, <bdi title={signing.identity}>{signing.identity}</bdi>{:else if signHref || backSigned}You, <bdi title={signing.identity}>{signing.identity}</bdi>, after you sign in again{:else}You do not sign here{#if signing.reason || write.reason}: <bdi>{signing.reason || write.reason}</bdi>{/if}{/if}</dd></div>
            <div><dt>Revision</dt><dd><bdi title={p.revision}>{p.revision}</bdi></dd></div>
            {#if p.owner}<div><dt>Owner</dt><dd><bdi title={p.owner}>{p.owner}</bdi></dd></div>{/if}
          </dl>
          {#each p.notes ?? [] as n, i (i)}<p class="t-meta cs-p">{n}</p>{/each}
          {#if p.policyNote}<p class="t-meta cs-p">{p.policyNote}</p>{/if}
        {/if}
        {#if offerings.length && answer.consumableCompleteness === 'partial'}<p class="t-meta cs-p" data-testid="create-partial">The offerings list is partial{answer.consumableAsOf ? ` as of ${answer.consumableAsOf}` : ''}: an offering not listed may exist.</p>{/if}
        {#each shown as o (o.offering)}{@render offering(o)}{/each}
        {#if others.length}
          <Fold title="Add an offering" level={4} meta={formatCount(others.length)} open={others.some((o) => offeringRefused(placed.at, o.offering))} testid="create-other-offerings">
            {#each others as o (o.offering)}{@render offering(o)}{/each}
          </Fold>
        {/if}
        {#if tunable.length}
          <Fold title="Vary per environment" level={4} open={!!placed.at[ENV_FIELD] || (envTouched && !!envError) || tunable.some(({ o, x }) => placed.at[fieldKey(o.offering, x.path, '*')])} testid="create-per-env">
            <label class="cs-field" for={inputId(ENV_FIELD)}><span>Environment</span></label>
            <input class="input" id={inputId(ENV_FIELD)} bind:value={env} maxlength="63" autocomplete="off" data-testid="create-env" aria-invalid={placed.at[ENV_FIELD] || (envTouched && envError) ? 'true' : undefined} aria-describedby={`${uid}-env-err`} />
            <p class="t-meta cs-p" id={`${uid}-env-err`} role={envTouched && envError ? 'alert' : undefined} data-testid="create-env-error">{envTouched ? envError : ''}</p>
            {#each placed.at[ENV_FIELD] ?? [] as r, i (i)}{@render refusalLine(r)}{/each}
            {#each tunable as { o, x } (o.offering + '/' + x.path)}
              {@render valueInput(o, x, fieldKey(o.offering, x.path, '*'), perEnv[o.offering]?.[x.path], setEnv)}
            {/each}
          </Fold>
        {/if}
        {#if movedText}<p class="cs-p" role="status" data-testid="create-moved">{movedText}</p>{/if}
        {#if p && !done}
          <div class="cs-actions">
            {#if backSigned && writes}
              <button type="button" class="btn btn-sm btn-primary" aria-disabled={busy ? 'true' : undefined} onclick={() => send('sign')} data-testid="create-publish">Publish {p.version}</button>
            {:else if signHref}
              <a class="btn btn-sm btn-primary" href={signHref} onclick={startSign} data-testid="create-sign"><span class="cs-nowrap">Sign as</span> <bdi>{signing.identity}</bdi></a>
            {:else if writes && !draft}
              <button type="button" class="btn btn-sm" aria-disabled={busy ? 'true' : undefined} onclick={() => send('draft')} data-testid="create-save-draft">Save draft</button>
            {:else if !write.available}
              <p class="t-meta cs-p" data-testid="create-no-write">{write.reason}</p>
            {/if}
          </div>
        {/if}
        {#if refusal}
          <RefusalNotice error={refusal} />
          {#if refusal.command}<CopyableIdentifier value={refusal.command} label="command" />{/if}
        {/if}
        {#if p}
          <Fold title="pacto.yaml" level={4} meta={p.contract?.truncated ? 'truncated' : ''} testid="create-contract">
            <!-- svelte-ignore a11y_no_noninteractive_tabindex (a long line scrolls inside the box; focus lets a keyboard reader scroll it) -->
            <pre class="cs-yaml" tabindex="0" aria-label="pacto.yaml"><code>{p.contract?.text ?? ''}</code></pre>
          </Fold>
          {#if p.files?.length || p.valuesFile}
            <Fold title="Files" level={4} meta={formatCount((p.filesTotal ?? p.files?.length ?? 0) + (p.valuesFile ? 1 : 0))} testid="create-files">
              {#if p.valuesFile}{@render fileText(p.valuesFile, 'Read by pacto init, beside the folder')}{/if}
              {#each p.files ?? [] as f (f.path)}{@render fileText(f, '')}{/each}
              {#if (p.filesTotal ?? 0) > (p.files?.length ?? 0)}<p class="t-meta cs-p">{moreText(p.files?.length ?? 0, p.filesTotal, 'files')}</p>{/if}
            </Fold>
          {/if}
        {/if}
        {#if drafts.length}
          <Fold title="Your drafts" level={4} meta={formatCount(answer.draftsTotal || drafts.length)} testid="create-drafts">
            <ul class="cs-list">
              {#each drafts as d (d.tag)}
                <li data-testid="create-draft">
                  {#if d.state !== 'unreadable'}<ProvenanceMark start label={`Who proposed ${d.tag}`} text={draftMark(d)} />{/if}
                  <bdi class="cs-grow">{draftLine(d)}</bdi>
                  {#if d.state !== 'unreadable' && d.digest && draft?.tag !== d.tag}<button type="button" class="btn btn-sm" onclick={() => acceptDraft(d)} aria-label={`Review ${d.tag}`}>Review</button>{/if}
                </li>
              {/each}
            </ul>
          </Fold>
        {/if}
      {/if}

      {#if result}
        <div class="cs-result" role="status" tabindex="-1" bind:this={resultBox} data-testid="create-result">
          {#if result.draft}
            <p class="cs-p">Saved <bdi>{result.tag}</bdi>. A person who may create the service reviews and signs it here.</p>
            {#if result.acceptUrl}<a href={result.acceptUrl}>Review the draft</a>{/if}
          {:else}
            <p class="cs-p">Published <bdi title={result.published}>{result.published}</bdi>{#if result.signed}{' '}by <bdi>{signing.identity}</bdi>{/if}{#if p?.revision}{' '}(<bdi>{p.revision}</bdi>){/if}.</p>
            <p class="t-meta cs-p">{result.appearsWithin || 'It shows in the fleet once a fleet source reads the organization index'}, declared at {p?.version ?? '0.1.0'} with nothing delivered.</p>
            {#if result.index}<p class="t-meta cs-p">{result.index}</p>{/if}
            {#if result.initCli}<div class="cs-cmd"><span class="t-meta">Start the folder</span> <CopyableIdentifier value={result.initCli} label="pacto init command" /></div>{/if}
            {#if result.valuesFile}{@render fileText(result.valuesFile, 'Save it beside the folder first: pacto init reads it')}{/if}
            {#if result.gitCli}<div class="cs-cmd"><span class="t-meta">Optional, with your own Git token</span> <CopyableIdentifier value={result.gitCli} label="gh repo create command" /></div>{/if}
            {#if zipped}
              <button type="button" class="btn btn-sm" onclick={download} data-testid="create-zip">Download zip</button>
            {:else if result.pullCli}
              <div class="cs-cmd" data-testid="create-pull"><span class="t-meta">Pull the files</span> <CopyableIdentifier value={result.pullCli} label="pacto pull command" /></div>
            {/if}
          {/if}
        </div>
      {/if}
    {/if}
    {#if commands}
      <Fold title="Run it yourself" level={4} testid="create-yourself">
        {#if p?.initCli}<div class="cs-cmd"><span class="t-meta">This plan</span> <CopyableIdentifier value={p.initCli} label="pacto init command" /></div>{/if}
        {#if p?.valuesFile}{@render fileText(p.valuesFile, 'Save it beside the folder first: pacto init reads it')}{/if}
        {@render commands()}
      </Fold>
    {/if}
  {/if}
</div>

<style>
  .cs { container-type: inline-size; display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .cs-p { margin: 0; overflow-wrap: anywhere; }
  .cs-h { margin: 0; font-size: var(--text-base); }
  .cs-form { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 18ch), 1fr)); align-items: end; gap: var(--sp-2); min-inline-size: 0; }
  .cs-path, .cs-wide { grid-column: 1 / -1; }
  .cs-field { display: flex; flex-direction: column; gap: 2px; min-inline-size: 0; }
  .cs-field .input, .cs-param .input { inline-size: 100%; }
  .cs-facts { margin: 0; display: flex; flex-direction: column; }
  .cs-facts > div { display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px var(--sp-3); padding: var(--sp-1) 0; min-inline-size: 0; }
  @container (min-width: 28rem) {
    .cs-facts > div { grid-template-columns: minmax(12ch, 1fr) minmax(16ch, 3fr); }
  }
  .cs-facts dt { color: var(--c-text-2); }
  .cs-facts dd { margin: 0; overflow-wrap: anywhere; min-inline-size: 0; }
  .cs-offering { display: flex; flex-direction: column; gap: var(--sp-2); padding: var(--sp-2); border: 1px solid var(--c-border); border-radius: var(--radius-sm); min-inline-size: 0; }
  .cs-oname { font-weight: 500; }
  .cs-param { display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px; min-inline-size: 0; }
  @container (min-width: 36rem) {
    .cs-param { grid-template-columns: minmax(16ch, 1fr) minmax(14ch, 1fr); align-items: center; }
    .cs-at { grid-column: 1 / -1; }
  }
  .cs-plabel { overflow-wrap: anywhere; min-inline-size: 0; }
  /* A sensitive field has no input: its line takes the whole row. */
  .cs-whole { grid-column: 1 / -1; }
  /* The bounds move to the next line whole after a long field name, never split at a range's dash. */
  .cs-bounds { display: inline-block; }
  .cs-refusal { border-inline-start: 3px solid var(--c-warn-border); padding-inline-start: var(--sp-2); display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0; }
  .cs-at { display: flex; flex-direction: column; gap: var(--sp-1); }
  .cs-cmd { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-inline-size: 0; }
  .cs-actions { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .cs-actions :global(.btn) { overflow-wrap: anywhere; max-inline-size: 100%; }
  .cs-nowrap { white-space: nowrap; flex: none; }
  .cs-result { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .cs-list { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: var(--sp-1); }
  .cs-list li { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-inline-size: 0; }
  .cs-grow { flex: 1 1 20ch; min-inline-size: 0; overflow-wrap: anywhere; }
  .cs-yaml { margin: 0; overflow: auto; max-block-size: 24rem; max-inline-size: 100%; padding: var(--sp-2); border: 1px solid var(--c-border); border-radius: var(--radius-sm); font-family: var(--font-mono); font-size: var(--text-xs); }
  @media (pointer: coarse) {
    .cs :global(.btn), .cs .input, .cs-result a { min-block-size: 44px; }
  }
</style>
