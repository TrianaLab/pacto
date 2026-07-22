/** Derive node-drawer content from in-memory fleet + graph data. Pure. */

export interface DrawerDep {
  name: string;
  type?: string;
  required?: boolean;
}

export interface DrawerData {
  name: string;
  status: string;
  version: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  owner: any;
  sources: string[];
  blastRadius: number;
  dependencies: DrawerDep[];
  dependents: string[];
  external: boolean;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function nodeDrawerData(name: string, services: any[], graphData: any): DrawerData | null {
  if (!name || !graphData?.nodes) return null;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const node = graphData.nodes.find((n: any) => n.serviceName === name || n.id === name);
  if (!node) return null;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const svc = (services || []).find((s: any) => s.name === name);

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const dependencies: DrawerDep[] = (node.edges || []).map((e: any) => ({
    name: e.targetName || e.targetId,
    type: e.type,
    required: e.required,
  }));

  const dependents: string[] = graphData.nodes
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    .filter((n: any) => (n.edges || []).some((e: any) => e.targetId === node.id))
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    .map((n: any) => n.serviceName);

  return {
    name,
    status: node.status,
    version: node.version || svc?.version || '',
    owner: svc?.owner,
    sources: svc?.sources || (svc?.source ? [svc.source] : []),
    blastRadius: svc?.blastRadius || 0,
    dependencies,
    dependents,
    external: node.status === 'external',
  };
}
