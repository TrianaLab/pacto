import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { ApiError } from '../lib/api.ts';
// @ts-expect-error — Svelte components have no declaration files
import DeliveryProblem from './DeliveryProblem.svelte';
// @ts-expect-error — Svelte components have no declaration files
import DeliveryUnavailable from './DeliveryUnavailable.svelte';

let app: ReturnType<typeof mount> | null = null;
afterEach(() => {
  if (app) unmount(app);
  app = null;
  document.body.innerHTML = '';
});

describe('DeliveryProblem', () => {
  it('tells a sign-in link session it reads the fleet only, with no Retry', () => {
    app = mount(DeliveryProblem, { target: document.body, props: {
      error: new ApiError(403, 'a sign-in link reads the fleet only', 'SignInLinkReadsFleet'), onRetry: () => {} } });
    flushSync();
    expect(document.body.textContent).toContain('A sign-in link reads the fleet only');
    expect(document.body.textContent).toContain('paste your own token to see delivery');
    expect(document.body.querySelector('button')).toBeNull();
  });

  it('keeps the Retry for any other refusal', () => {
    app = mount(DeliveryProblem, { target: document.body, props: { error: new ApiError(403, 'forbidden'), onRetry: () => {} } });
    flushSync();
    expect(document.body.textContent).toContain('The cluster refused');
    expect(document.body.querySelector('button')).not.toBeNull();
  });

  it('says why a sign-in link session has no delivery view where Delivery is hidden', () => {
    app = mount(DeliveryUnavailable, { target: document.body, props: { reason: 'link' } });
    flushSync();
    expect(document.body.textContent).toContain('A sign-in link reads the fleet only. Sign in with your identity provider or paste your own token to see delivery.');
  });
});
