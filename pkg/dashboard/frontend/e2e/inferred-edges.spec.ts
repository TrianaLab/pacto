import { test, expect } from '@playwright/test';
import { appReady } from './ready';

// Inferred edges and a contract taking over, against the built WASM demo
// (examples/demo/source_inferred_edges.go): the kernel observer sees nightly-export, a
// CronJob no contract describes, call search-indexer, legacy-billing and event-bus; and
// experiment-service's Pacto target runs as the experiments Deployment, which ran with
// no contract for days before the Pacto was created.

const T = 20_000;
// The demo is a hub with members (514-S3), so an inferred key names its cluster.
const HUB = '5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11';
const REPLACED = `k8s:${HUB}%2Fprod%2FDeployment%2Fexperiments`;

test.describe('WASM demo: inferred edges and replaced services', () => {
  test('the graph draws a call one end of which has no contract as inferred and says so in the drawer', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    // Ready on the Overview: on the graph route the breadcrumb names the graph too.
    await appReady(page, '/#/fleet');
    await page.goto('/#/fleet/graph/service/search-indexer');

    const legend = page.getByTestId('graph-legend');
    await expect(legend).toBeVisible({ timeout: T });
    await expect(legend.getByRole('button', { name: 'Inferred: one end has no contract, or read from configuration' })).toBeVisible();

    await page.getByTestId('graph-textalt').locator('summary').click();
    const edge = page.getByTestId('graph-edge').filter({ hasText: 'nightly-export' });
    await expect(edge).toHaveCount(1, { timeout: T });
    await expect(edge.getByTestId('edge-difference')).toContainText('Seen at runtime, no contract');
    await edge.click();
    const drawer = page.getByTestId('graph-drawer');
    await expect(drawer).toContainText('Seen at runtime, no contract');
    await expect(drawer.getByTestId('edge-difference-description')).toContainText(/^No contract describes k8s:5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11\/shop\/CronJob\/nightly-export\. Seen by k8s-production\. No contract declares this call/);
  });

  test('the service page carries one muted Replaces line with its details in the tooltip', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, '/#/fleet/services/experiment-service');

    const line = page.getByTestId('service-replaces');
    await expect(line).toContainText(/Replaces k8s:5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11\/prod\/Deployment\/experiments · (\d+ minutes?|just now)/, { timeout: T });
    await expect(line.locator('bdi')).toHaveAttribute('title', `k8s:${HUB}/prod/Deployment/experiments`);
    await line.getByRole('button', { name: 'About Replaces' }).click();
    await expect(line.getByRole('note')).toContainText('Linked by the Pacto target 5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11/prod/Deployment/experiment-service, which runs as the workload.');
  });

  test('the key of the replaced inferred service opens the declared service with one notice line', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await appReady(page, `/#/fleet/services/${encodeURIComponent(REPLACED)}`);

    const notice = page.getByTestId('entity-replaced-from');
    await expect(notice).toHaveCount(1, { timeout: T });
    await expect(notice).toContainText(/^k8s:5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11\/prod\/Deployment\/experiments was replaced by experiment-service .*, linked by Pacto 5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11\/prod\/Deployment\/experiment-service$/);
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
