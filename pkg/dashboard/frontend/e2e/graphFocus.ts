import { expect, type Page } from '@playwright/test';
import { appReady } from './ready';

// A service focus on the Operational graph is the fleet map's left-to-right diagram
// (509-S12). Its edges are drawn on a canvas, so a spec reads them from the answer the
// diagram was drawn from; the neighbourhood view stays for revision, target, owner and
// external focus, and is reached from a service's revision.

/** mapFocus opens the map in focus on a service and returns its focus answer. */
export async function mapFocus(page: Page, service: string, hops = 1) {
  await appReady(page, '/#/fleet');
  await page.goto(`/#/fleet/graph/service/${encodeURIComponent(service)}${hops > 1 ? '?hops=2' : ''}`);
  await expect(page.getByTestId('fleet-map-focus')).toBeVisible({ timeout: 20_000 });
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return page.evaluate(async ([s, h]) => (await (await fetch(`/api/fleet/map/focus?service=${encodeURIComponent(s as string)}&hops=${h}`)).json()) as any, [service, hops] as const);
}

/** revisionGraphUrl is the neighbourhood of one revision of a service, in the revision perspective. */
export async function revisionGraphUrl(page: Page, service: string): Promise<string> {
  // Only the engine, not appReady: on a phone viewport the sidebar link it waits for is
  // folded into the drawer, and the fetch below queues behind __pactoReady anyway.
  await page.goto('/#/fleet');
  await page.evaluate(() => (window as unknown as { __pactoReady?: Promise<void> }).__pactoReady);
  const key = await page.evaluate(async (s) => (((await (await fetch(`/api/fleet/entities?kinds=revision&service=${encodeURIComponent(s)}&limit=1`)).json()).entities ?? [])[0]?.key ?? '') as string, service);
  expect(key, `a revision of ${service}`).not.toBe('');
  return `/#/fleet/graph/revision/${encodeURIComponent(key)}?perspective=revision`;
}
