<script>
  // A Stack and its rows (SPEC 3): the layers on the left, one HTML row per layer on the
  // right (version, then what it is), each wired from its layer's corner. The rows carry
  // every word, so a long version wraps or is elided in its own box (C6), never on a face.
  import Stack from './Stack.svelte';
  import { hashForHref } from '../../lib/router.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ layers: { tone: string, dash?: boolean, a: string, m?: string }[], rows: import('../../lib/entityFigures.ts').StackRow[], earlier?: number, earlierHref?: string, earlierNote?: string }} */
  let { layers, rows, earlier = 0, earlierHref = '', earlierNote = '' } = $props();
</script>

<div class="stackfig">
  <div class="stackfig-s"><Stack {layers} {earlier} /></div>
  <ul class="stack-rows">
    {#each rows as r (r.a)}
      <li class="stack-row" data-a="l{r.a}">
        {#if r.ref}
          <a class="id mono" href={hashForHref(r.ref.href)} title={r.label}><bdi class:fit={elidable(r.label)}>{r.label}</bdi></a>
        {:else}
          <b class="id mono" title={r.label}><bdi class:fit={elidable(r.label)}>{r.label}</bdi></b>
        {/if}
        {#if r.note}<span class="t-meta">{r.note}</span>{/if}
      </li>
    {/each}
    {#if earlier}
      <li class="stack-row" data-a="lplinth"><a href={earlierHref}>{earlier === 1 ? '1 earlier revision' : `${earlier.toLocaleString('en-US')} earlier revisions`}</a>{#if earlierNote}<span class="t-meta">{earlierNote}</span>{/if}</li>
    {/if}
  </ul>
</div>
