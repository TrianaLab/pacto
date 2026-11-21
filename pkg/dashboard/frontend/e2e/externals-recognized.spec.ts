import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// An external only traces reveal, against the built WASM demo: notification-worker
// calls api.twilio.com, no contract declares it, and the catalog classifies the
// callee as twilio. The cluster collector also infers from notification-worker's
// pod spec that it calls Stripe, so its graph draws an inferred edge.

const T = 20_000;

test.describe('WASM demo — externals recognized from traces and configuration', () => {
  test('twilio lists only its trace sources and notification-worker shows the inferred edge', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/externals/twilio');

    const rows = page.getByTestId('external-source');
    // One observed row per trace source: the trace file and the service graph the
    // dashboard reads from Prometheus both saw the call.
    const allObserved = rows.and(page.locator('[data-provenance="observed"]'));
    await expect(allObserved).toHaveCount(2, { timeout: T });
    await expect(allObserved.filter({ has: page.locator('[data-cell="where"]', { hasText: 'detected-service-graph' }) })).toHaveCount(1);
    const observed = allObserved.filter({ has: page.locator('[data-cell="where"]', { hasText: 'otel-traces' }) });
    await expect(observed).toHaveCount(1);
    await expect(observed.locator('[data-cell="service"]')).toContainText('notification-worker');
    await expect(page.locator('[data-testid="external-source"][data-provenance="declared"]')).toHaveCount(0);
    // No contract declares it, so the consumer reads as observed, never optional,
    // and the trace row's host is unknown, never "none".
    await expect(page.getByTestId('external-consumer-use')).toHaveText(['observed only']);
    await expect(observed.locator('[data-cell="host"]')).toContainText('not recorded');

    // The observation window sits under More details.
    await page.getByTestId('external-more-details').click();
    await expect(observed.locator('[data-cell="details"]')).toContainText('first seen');
    await expect(observed.locator('[data-cell="details"]')).toContainText('last seen');

    await observed.getByRole('link', { name: 'notification-worker' }).click();
    await expect(page).toHaveURL(/#\/fleet\/services\/notification-worker/);

    await page.goto('/#/fleet/graph/service/notification-worker');
    const legend = page.getByTestId('graph-legend');
    await expect(legend).toBeVisible({ timeout: T });
    await expect(legend.getByRole('button', { name: 'Observed, not expected' })).toBeVisible();
    await expect(legend.getByRole('button', { name: 'Inferred: one end has no contract, or read from configuration' })).toBeVisible();

    await page.getByTestId('graph-textalt').locator('summary').click();
    const inferred = page.getByTestId('graph-edge').filter({ hasText: /stripe/i });
    await expect(inferred).toHaveCount(1, { timeout: T });
    await expect(inferred.getByTestId('edge-difference')).toContainText('Inferred from configuration');
    // The trace-only call to twilio is drift, not a declared dependency.
    const traced = page.getByTestId('graph-edge').filter({ hasText: /twilio/i });
    await expect(traced.getByTestId('edge-difference')).toContainText('Observed, not expected');
  });
});
