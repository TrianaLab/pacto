// cytoscape-fcose ships no type declarations; it's a Cytoscape layout extension
// registered via cytoscape.use(). We only need the default export to exist.
declare module 'cytoscape-fcose' {
  import type { Ext } from 'cytoscape';
  const ext: Ext;
  export default ext;
}
