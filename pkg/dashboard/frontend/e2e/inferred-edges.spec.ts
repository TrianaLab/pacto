import { test, expect } from '@playwright/test';
import { appReady } from './ready';
import { mapFocus } from './graphFocus';

// Inferred edges and a contract taking over, against the built WASM demo
// (examples/demo/source_inferred_edges.go): the kernel observer sees nightly-export, a
// CronJob no contract describes, call search-indexer, legacy-billing and event-bus; and
// experiment-service's Pacto target runs as the experiments Deployment, which ran with
// no contract for days before the Pacto was created.

const T = 20_000;
const REPLACED = 'k8s:prod%2FDeployment%2Fexperiments';

test.describe('WASM demo: inferred edges and replaced services', () => {
  test('the map in focus on search-indexer draws a call one end of which has no contract as inferred', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    // The diagram is a canvas: its edges are read from the answer it was drawn from.
    const focus = await mapFocus(page, 'search-indexer');
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const edge = (focus.edges ?? []).filter((e: any) => `${e.from.label} ${e.to.label}`.includes('nightly-export'));
    expect(edge).toHaveLength(1);
    // An inferred runtime call keeps observed=false: its provenance is inferred, not observed.
    expect(edge[0]).toMatchObject({ inferred: true, expected: false, provenance: 'inferred' });
    // The caller sits in the left column, the service in the centre.
    await expect(page.getByTestId('fleet-map-focus')).toContainText('search-indexer');
  });

  test('the service page carries one muted Replaces line with its details in the tooltip', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services/experiment-service');

    const line = page.getByTestId('service-replaces');
    await expect(line).toContainText(/Replaces k8s:prod\/Deployment\/experiments · (\d+ minutes?|just now)/, { timeout: T });
    await expect(line.locator('bdi')).toHaveAttribute('title', 'k8s:prod/Deployment/experiments');
    await line.getByRole('button', { name: 'About Replaces' }).click();
    await expect(line.getByRole('note')).toContainText('Linked by the Pacto target prod/Deployment/experiment-service, which runs as the workload.');
  });

  test('the key of the replaced inferred service opens the declared service with one notice line', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, `/#/fleet/services/${encodeURIComponent(REPLACED)}`);

    const notice = page.getByTestId('entity-replaced-from');
    await expect(notice).toHaveCount(1, { timeout: T });
    await expect(notice).toContainText(/^k8s:prod\/Deployment\/experiments was replaced by experiment-service .*, linked by Pacto prod\/Deployment\/experiment-service$/);
    await expect(page.getByTestId('inferred-workload')).toHaveCount(0);
    await expect(page.getByTestId('service-contract')).toBeVisible();
  });

  test('the offering page links an uncontracted caller to its inferred service', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/offerings/messaging');

    const row = page.getByTestId('observed-consumer').filter({ hasText: 'nightly-export' });
    await expect(row).toHaveCount(1, { timeout: T });
    await expect(row).toContainText('uncontracted');
    await row.getByRole('link', { name: /nightly-export/ }).click();
    await expect(page.getByTestId('inferred-workload')).toBeVisible({ timeout: T });
  });
});
