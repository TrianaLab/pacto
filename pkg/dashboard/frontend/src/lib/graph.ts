/**
 * D3 force-directed graph renderer.
 * Returns { destroy, zoomIn, zoomOut, resetView, applyFilter }.
 */
import * as d3 from 'd3';
import { reasonLabel, reasonTooltip } from './format.ts';

const STATUS_COLORS: Record<string, string> = {
  Compliant: '#34d399',
  Warning: '#fbbf24',
  NonCompliant: '#f87171',
  Unknown: '#64748b',
  Reference: '#64748b',
  external: '#475569',
};

/** Reason-specific stroke colors for external nodes. */
const REASON_COLORS: Record<string, string> = {
  non_oci_ref: '#475569',   // neutral gray — expected
  auth_failed: '#f87171',   // red — needs action
  no_semver_tags: '#fbbf24', // yellow — warning
  not_found: '#fbbf24',     // yellow — warning
  discovering: '#818cf8',   // accent — transient
};

const NODE_W = 164;
const NODE_H = 42;

export interface GraphEdge {
  targetId: string;
  required?: boolean;
  type?: string;
}

export interface GraphNode {
  id: string;
  serviceName: string;
  status: string;
  reason?: string;    // why unresolved: non_oci_ref, auth_failed, no_semver_tags, not_found, discovering
  edges?: GraphEdge[];
  // D3 simulation adds these at runtime
  x?: number;
  y?: number;
  fx?: number | null;
  fy?: number | null;
}

export interface GraphData {
  nodes: GraphNode[];
}

interface SimLink {
  source: string | GraphNode;
  target: string | GraphNode;
  required?: boolean;
  type: string;
}

export interface GraphControls {
  nodes: GraphNode[];
  destroy: () => void;
  zoomIn: () => void;
  zoomOut: () => void;
  resetView: () => void;
  applyFilter: (fn: ((n: GraphNode) => boolean) | null) => void;
}

interface RenderOptions {
  onNavigate?: (name: string) => void;
  focusId?: string;
  filterFn?: (n: GraphNode) => boolean;
}

export function renderGraph(container: HTMLElement, graphData: GraphData, { onNavigate, focusId, filterFn }: RenderOptions = {}): GraphControls {
  const nodes: GraphNode[] = (graphData.nodes || []).map((n) => ({ ...n }));
  const links: SimLink[] = [];
  const nodeMap = new Map(nodes.map((n) => [n.id, n]));

  for (const node of nodes) {
    for (const edge of node.edges || []) {
      if (nodeMap.has(edge.targetId)) {
        links.push({
          source: node.id,
          target: edge.targetId,
          required: edge.required,
          type: edge.type || 'dependency',
        });
      }
    }
  }

  const rect = container.getBoundingClientRect();
  const width = rect.width || 800;
  const height = rect.height || 500;

  // Clear
  container.innerHTML = '';

  const svg = d3.select(container)
    .append('svg')
    .attr('width', '100%')
    .attr('height', '100%')
    .attr('viewBox', `0 0 ${width} ${height}`)
    .style('font-family', 'var(--font-sans)');

  // Defs for arrow markers
  const defs = svg.append('defs');

  // Resolve CSS variable to actual color for marker fill (CSS vars don't work inside markers)
  const markerColor = getComputedStyle(container).getPropertyValue('--c-text-3').trim() || '#94a3b8';

  defs.append('marker')
    .attr('id', 'arrow')
    .attr('viewBox', '0 0 10 6')
    .attr('refX', 10).attr('refY', 3)
    .attr('markerWidth', 10).attr('markerHeight', 8)
    .attr('orient', 'auto')
    .append('path')
    .attr('d', 'M0,0 L10,3 L0,6 Z')
    .attr('fill', markerColor);

  defs.append('marker')
    .attr('id', 'arrow-ref')
    .attr('viewBox', '0 0 10 6')
    .attr('refX', 10).attr('refY', 3)
    .attr('markerWidth', 10).attr('markerHeight', 8)
    .attr('orient', 'auto')
    .append('path')
    .attr('d', 'M0,0 L10,3 L0,6 Z')
    .attr('fill', 'var(--c-accent, #818cf8)');

  // Glow filter for focused node
  const focusGlow = defs.append('filter').attr('id', 'focus-glow');
  focusGlow.append('feDropShadow')
    .attr('dx', 0).attr('dy', 0)
    .attr('stdDeviation', 4)
    .attr('flood-color', 'var(--c-accent, #818cf8)')
    .attr('flood-opacity', 0.5);

  const g = svg.append('g');

  // D3 zoom registers wheel/touchstart as non-passive because it calls
  // preventDefault() to stop page scroll while zooming the graph.
  // The container's `touch-action: none` CSS handles touch on mobile.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const zoom = d3.zoom<SVGSVGElement, unknown>()
    .scaleExtent([0.2, 4])
    .on('zoom', (e) => g.attr('transform', e.transform));
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (svg as any).call(zoom);

  // Prevent zoom's click suppression from blocking node clicks
  svg.on('dblclick.zoom', null);

  // Pin focused node to center so it's always clearly visible
  if (focusId) {
    const focusNode = nodes.find((n) => n.id === focusId || n.serviceName === focusId);
    if (focusNode) {
      focusNode.fx = width / 2;
      focusNode.fy = height / 2;
    }
  }

  const sim = d3.forceSimulation(nodes as d3.SimulationNodeDatum[])
    .force('link', d3.forceLink(links as d3.SimulationLinkDatum<d3.SimulationNodeDatum>[]).id((d: any) => d.id).distance(200))
    .force('charge', d3.forceManyBody().strength(-500))
    .force('center', d3.forceCenter(width / 2, height / 2))
    .force('collision', d3.forceCollide().radius(NODE_W / 2 + 14));

  // Links
  const linkG = g.append('g').attr('class', 'links');
  const linkEls = linkG.selectAll('line')
    .data(links)
    .join('line')
    .attr('stroke', (d) => d.type === 'reference' ? 'var(--c-accent)' : 'var(--c-text-3)')
    .attr('stroke-width', (d) => d.required ? 2 : 1)
    .attr('stroke-dasharray', (d) => {
      if (d.type === 'reference') return '6,3';
      return d.required ? 'none' : '4,3';
    })
    .attr('marker-end', (d) => d.type === 'reference' ? 'url(#arrow-ref)' : 'url(#arrow)')
    .attr('opacity', 0.6);

  // Nodes — track drag movement to distinguish click from drag
  const nodeG = g.append('g').attr('class', 'nodes');
  let dragMoved = false;

  const nodeEls = nodeG.selectAll<SVGGElement, GraphNode>('g')
    .data(nodes)
    .join('g')
    .attr('cursor', (d) => d.status === 'external' ? 'grab' : 'pointer')
    .call(d3.drag<SVGGElement, GraphNode>()
      .on('start', (e, d) => {
        dragMoved = false;
        if (!e.active) sim.alphaTarget(0.3).restart();
        d.fx = d.x; d.fy = d.y;
      })
      .on('drag', (e, d) => {
        dragMoved = true;
        d.fx = e.x; d.fy = e.y;
      })
      .on('end', (e, d) => {
        if (!e.active) sim.alphaTarget(0);
        d.fx = null; d.fy = null;
        // Navigate on click (no drag movement)
        if (!dragMoved && d.status !== 'external' && onNavigate) {
          onNavigate(d.serviceName);
        }
      })
    );

  /** Returns the stroke/dot color for a node, accounting for reason on external nodes. */
  function nodeColor(d: GraphNode): string {
    if (d.status === 'external' && d.reason && REASON_COLORS[d.reason]) {
      return REASON_COLORS[d.reason];
    }
    return STATUS_COLORS[d.status] || STATUS_COLORS.Unknown;
  }

  // Node rect
  nodeEls.append('rect')
    .attr('width', NODE_W).attr('height', NODE_H)
    .attr('x', -NODE_W / 2).attr('y', -NODE_H / 2)
    .attr('rx', 6)
    .attr('fill', 'var(--c-surface)')
    .attr('stroke', (d) => nodeColor(d))
    .attr('stroke-width', (d) => d.serviceName === focusId ? 3 : 1.5)
    .attr('stroke-dasharray', (d) => d.reason === 'discovering' ? '4,3' : null)
    .attr('filter', (d) => d.serviceName === focusId ? 'url(#focus-glow)' : null);

  // Status indicator — icon varies by reason for external nodes
  nodeEls.each(function (d: GraphNode) {
    const el = d3.select(this);
    const cx = -NODE_W / 2 + 14;
    if (d.status === 'external' && d.reason === 'auth_failed') {
      // Lock icon (SVG path)
      el.append('text')
        .attr('x', cx).attr('y', 1)
        .attr('text-anchor', 'middle')
        .attr('dominant-baseline', 'central')
        .attr('font-size', '12px')
        .attr('fill', REASON_COLORS.auth_failed)
        .text('🔒');
    } else if (d.status === 'external' && d.reason === 'discovering') {
      // Pulsing dot for discovering state
      const dot = el.append('circle')
        .attr('cx', cx).attr('cy', 0).attr('r', 5)
        .attr('fill', REASON_COLORS.discovering);
      // Simple CSS-like pulse via repeated transition
      function pulse() {
        dot.transition().duration(800).attr('opacity', 0.3)
          .transition().duration(800).attr('opacity', 1)
          .on('end', pulse);
      }
      pulse();
    } else if (d.status === 'external' && (d.reason === 'no_semver_tags' || d.reason === 'not_found')) {
      // Warning triangle
      el.append('text')
        .attr('x', cx).attr('y', 1)
        .attr('text-anchor', 'middle')
        .attr('dominant-baseline', 'central')
        .attr('font-size', '12px')
        .attr('fill', REASON_COLORS[d.reason])
        .text('⚠');
    } else {
      // Default dot
      el.append('circle')
        .attr('cx', cx).attr('cy', 0).attr('r', 5)
        .attr('fill', nodeColor(d));
    }
  });

  // Label
  nodeEls.append('text')
    .attr('x', -NODE_W / 2 + 26).attr('y', 1)
    .attr('dominant-baseline', 'middle')
    .attr('fill', 'var(--c-text)')
    .attr('font-size', '13px')
    .attr('font-weight', '500')
    .text((d) => {
      const name = d.serviceName || d.id;
      return name.length > 18 ? name.slice(0, 17) + '…' : name;
    });

  // Native SVG tooltip — reason-aware for external nodes
  nodeEls.append('title')
    .text((d) => {
      const name = d.serviceName || d.id;
      if (d.status === 'external') {
        return `${name} — ${reasonTooltip(d.reason)}`;
      }
      return `${name} — ${d.status || 'Unknown'}`;
    });

  // Build adjacency (bidirectional) and reverse-dependency map (who depends on X)
  const adjacency = new Map<string, Set<string>>();
  const dependedOnBy = new Map<string, Set<string>>(); // targetId -> set of sourceIds that depend on it
  nodes.forEach((n) => {
    adjacency.set(n.id, new Set());
    dependedOnBy.set(n.id, new Set());
  });
  links.forEach((l) => {
    const sid = typeof l.source === 'object' ? (l.source as GraphNode).id : l.source;
    const tid = typeof l.target === 'object' ? (l.target as GraphNode).id : l.target;
    adjacency.get(sid)?.add(tid);
    adjacency.get(tid)?.add(sid);
    // source depends on target, so target being broken impacts source
    dependedOnBy.get(tid)?.add(sid);
  });

  // BFS upstream: find all nodes that depend on `startId` (directly or transitively)
  function blastRadiusBFS(startId: string): Set<string> {
    const impacted = new Set<string>();
    const queue = [startId];
    while (queue.length) {
      const id = queue.shift()!;
      for (const depId of dependedOnBy.get(id) || []) {
        if (!impacted.has(depId) && depId !== startId) {
          impacted.add(depId);
          queue.push(depId);
        }
      }
    }
    return impacted;
  }

  // Resolve marker color for blast-radius highlight
  const blastColor = getComputedStyle(container).getPropertyValue('--c-err').trim() || '#f87171';
  const warnColor = getComputedStyle(container).getPropertyValue('--c-warn').trim() || '#fbbf24';

  nodeEls
    .on('mouseenter', (_, d) => {
      const hasIssues = d.status === 'Warning' || d.status === 'NonCompliant';
      const neighbors = adjacency.get(d.id) || new Set<string>();
      const impacted = hasIssues ? blastRadiusBFS(d.id) : new Set<string>();
      const highlight = new Set([d.id, ...neighbors, ...impacted]);

      nodeEls.transition().duration(150)
        .attr('opacity', (n) => highlight.has(n.id) ? 1 : 0.15);

      // Pulse the stroke of impacted nodes to show blast radius
      if (hasIssues && impacted.size > 0) {
        const pulseColor = d.status === 'NonCompliant' ? blastColor : warnColor;
        nodeEls.select('rect')
          .transition().duration(150)
          .attr('stroke', (n: any) => {
            if (impacted.has(n.id)) return pulseColor;
            return nodeColor(n);
          })
          .attr('stroke-width', (n: any) => {
            if (impacted.has(n.id)) return 2.5;
            return n.serviceName === focusId ? 2.5 : 1.5;
          });
      }

      linkEls.transition().duration(150)
        .attr('opacity', (l) => {
          const sid = typeof l.source === 'object' ? (l.source as GraphNode).id : l.source;
          const tid = typeof l.target === 'object' ? (l.target as GraphNode).id : l.target;
          return highlight.has(sid) && highlight.has(tid) ? 0.8 : 0.05;
        })
        .attr('stroke-width', (l) => {
          const sid = typeof l.source === 'object' ? (l.source as GraphNode).id : l.source;
          const tid = typeof l.target === 'object' ? (l.target as GraphNode).id : l.target;
          const connected = (sid === d.id || tid === d.id) || (impacted.has(sid) && impacted.has(tid));
          return connected ? (l.required ? 2.5 : 1.5) : (l.required ? 2 : 1);
        });
    })
    .on('mouseleave', () => {
      nodeEls.transition().duration(150).attr('opacity', 1);
      nodeEls.select('rect')
        .transition().duration(150)
        .attr('stroke', (n: any) => nodeColor(n))
        .attr('stroke-width', (n: any) => n.serviceName === focusId ? 2.5 : 1.5);
      linkEls.transition().duration(150)
        .attr('opacity', 0.6)
        .attr('stroke-width', (l) => l.required ? 2 : 1);
    });

  // Apply filter if provided
  function applyFilter(fn: ((n: GraphNode) => boolean) | null): void {
    if (!fn) {
      nodeEls.attr('opacity', 1);
      linkEls.attr('opacity', 0.6);
      return;
    }
    const hidden = new Set<string>();
    nodes.forEach((n) => { if (fn(n)) hidden.add(n.id); });
    nodeEls.attr('opacity', (d) => hidden.has(d.id) ? 0.1 : 1);
    linkEls.attr('opacity', (d) => {
      const sid = typeof d.source === 'object' ? (d.source as GraphNode).id : d.source;
      const tid = typeof d.target === 'object' ? (d.target as GraphNode).id : d.target;
      return hidden.has(sid) || hidden.has(tid) ? 0.05 : 0.6;
    });
  }

  if (filterFn) applyFilter(filterFn);

  // Clip line endpoint to target node's rectangle boundary
  function clipToRect(sx: number, sy: number, tx: number, ty: number, hw: number, hh: number): { x: number; y: number } {
    const dx = tx - sx;
    const dy = ty - sy;
    const len = Math.sqrt(dx * dx + dy * dy);
    if (len === 0) return { x: tx, y: ty };
    const nx = dx / len;
    const ny = dy / len;
    // Find intersection with rect edges
    const scaleX = Math.abs(nx) > 1e-6 ? hw / Math.abs(nx) : Infinity;
    const scaleY = Math.abs(ny) > 1e-6 ? hh / Math.abs(ny) : Infinity;
    const scale = Math.min(scaleX, scaleY);
    return { x: tx - nx * scale, y: ty - ny * scale };
  }

  sim.on('tick', () => {
    linkEls.each(function (d: any) {
      const clipped = clipToRect(d.source.x, d.source.y, d.target.x, d.target.y, NODE_W / 2, NODE_H / 2);
      d3.select(this)
        .attr('x1', d.source.x).attr('y1', d.source.y)
        .attr('x2', clipped.x).attr('y2', clipped.y);
    });
    nodeEls.attr('transform', (d: any) => `translate(${d.x},${d.y})`);
  });

  return {
    nodes,
    destroy: () => { sim.stop(); container.innerHTML = ''; },
    zoomIn: () => (svg as any).transition().duration(300).call(zoom.scaleBy, 1.4),
    zoomOut: () => (svg as any).transition().duration(300).call(zoom.scaleBy, 0.7),
    resetView: () => (svg as any).transition().duration(300).call(zoom.transform, d3.zoomIdentity),
    applyFilter,
  };
}

/**
 * Extract a subgraph centered on focusId via BFS.
 */
export function extractSubgraph(graphData: GraphData | null, focusId: string | null): GraphData | null {
  if (!graphData?.nodes?.length || !focusId) return null;
  const nodeMap = new Map(graphData.nodes.map((n) => [n.id, n]));
  const focus = nodeMap.get(focusId);
  if (!focus) return null;

  const visited = new Set([focusId]);
  const queue = [focusId];
  // Gather direct edges from focus + edges pointing to focus
  while (queue.length) {
    const id = queue.shift()!;
    const node = nodeMap.get(id);
    if (!node) continue;
    for (const edge of node.edges || []) {
      if (!visited.has(edge.targetId) && nodeMap.has(edge.targetId)) {
        visited.add(edge.targetId);
        queue.push(edge.targetId);
      }
    }
  }
  // Also add nodes that point TO any visited node
  for (const node of graphData.nodes) {
    for (const edge of node.edges || []) {
      if (visited.has(edge.targetId)) visited.add(node.id);
    }
  }

  const subNodes = graphData.nodes.filter((n) => visited.has(n.id));
  return subNodes.length > 1 ? { nodes: subNodes } : null;
}
