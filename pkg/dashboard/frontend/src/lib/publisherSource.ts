/**
 * publisherSourceLabel names what accepted a version: the one place for these words.
 * ponytail: its own module, not delivery.ts, because delivery.ts is in the first load
 * and its budget (pkg/dashboard/server_test.go) has no room; only lazy views use it.
 */
export function publisherSourceLabel(source: string | undefined): string {
  return ['rule', 'pin', 'policy', 'inferred', 'release'].includes(source ?? '') ? source! : 'no publisher check';
}
