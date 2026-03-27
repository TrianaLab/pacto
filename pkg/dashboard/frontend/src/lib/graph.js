/**
 * D3 force-directed graph renderer.
 * Returns { destroy, zoomIn, zoomOut, resetView, applyFilter }.
 */
import * as d3 from 'd3';

const STATUS_COLORS = {
  Healthy: '#34d399',
  Degraded: '#fbbf24',
  Invalid: '#f87171',
  Unknown: '#64748b',
  Reference: '#64748b',
  external: '#475569',
};

const NODE_W = 140;
const NODE_H = 36;

export function renderGraph(container, graphData, { onNavigate, focusId, filterFn } = {}) {
  const nodes = (graphData.nodes || []).map((n) => ({ ...n }));
  const links = [];
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
  defs.append('marker')
    .attr('id', 'arrow')
    .attr('viewBox', '0 0 10 6')
    .attr('refX', 10).attr('refY', 3)
    .attr('markerWidth', 8).attr('markerHeight', 6)
    .attr('orient', 'auto')
    .append('path')
    .attr('d', 'M0,0 L10,3 L0,6')
    .attr('fill', 'var(--c-text-3)');

  const g = svg.append('g');

  const zoom = d3.zoom()
    .scaleExtent([0.2, 3])
    .on('zoom', (e) => g.attr('transform', e.transform));
  svg.call(zoom);

  const sim = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).id((d) => d.id).distance(180))
    .force('charge', d3.forceManyBody().strength(-400))
    .force('center', d3.forceCenter(width / 2, height / 2))
    .force('collision', d3.forceCollide().radius(NODE_W / 2 + 10));

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
    .attr('marker-end', 'url(#arrow)')
    .attr('opacity', 0.6);

  // Nodes
  const nodeG = g.append('g').attr('class', 'nodes');
  const nodeEls = nodeG.selectAll('g')
    .data(nodes)
    .join('g')
    .attr('cursor', 'pointer')
    .call(d3.drag()
      .on('start', (e, d) => { if (!e.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
      .on('drag', (e, d) => { d.fx = e.x; d.fy = e.y; })
      .on('end', (e, d) => { if (!e.active) sim.alphaTarget(0); d.fx = null; d.fy = null; })
    );

  nodeEls.on('click', (e, d) => {
    if (d.status !== 'external' && onNavigate) onNavigate(d.serviceName);
  });

  // Node rect
  nodeEls.append('rect')
    .attr('width', NODE_W).attr('height', NODE_H)
    .attr('x', -NODE_W / 2).attr('y', -NODE_H / 2)
    .attr('rx', 6)
    .attr('fill', 'var(--c-surface)')
    .attr('stroke', (d) => STATUS_COLORS[d.status] || STATUS_COLORS.Unknown)
    .attr('stroke-width', (d) => d.serviceName === focusId ? 2.5 : 1.5);

  // Status dot
  nodeEls.append('circle')
    .attr('cx', -NODE_W / 2 + 12).attr('cy', 0).attr('r', 4)
    .attr('fill', (d) => STATUS_COLORS[d.status] || STATUS_COLORS.Unknown);

  // Label
  nodeEls.append('text')
    .attr('x', -NODE_W / 2 + 22).attr('y', 1)
    .attr('dominant-baseline', 'middle')
    .attr('fill', 'var(--c-text)')
    .attr('font-size', '11px')
    .attr('font-weight', '500')
    .text((d) => {
      const name = d.serviceName || d.id;
      return name.length > 16 ? name.slice(0, 15) + '…' : name;
    });

  // Apply filter if provided
  function applyFilter(fn) {
    if (!fn) {
      nodeEls.attr('opacity', 1);
      linkEls.attr('opacity', 0.6);
      return;
    }
    const hidden = new Set();
    nodes.forEach((n) => { if (fn(n)) hidden.add(n.id); });
    nodeEls.attr('opacity', (d) => hidden.has(d.id) ? 0.1 : 1);
    linkEls.attr('opacity', (d) => {
      const sid = typeof d.source === 'object' ? d.source.id : d.source;
      const tid = typeof d.target === 'object' ? d.target.id : d.target;
      return hidden.has(sid) || hidden.has(tid) ? 0.05 : 0.6;
    });
  }

  if (filterFn) applyFilter(filterFn);

  sim.on('tick', () => {
    linkEls
      .attr('x1', (d) => d.source.x).attr('y1', (d) => d.source.y)
      .attr('x2', (d) => d.target.x).attr('y2', (d) => d.target.y);
    nodeEls.attr('transform', (d) => `translate(${d.x},${d.y})`);
  });

  return {
    nodes,
    destroy: () => { sim.stop(); container.innerHTML = ''; },
    zoomIn: () => svg.transition().duration(300).call(zoom.scaleBy, 1.4),
    zoomOut: () => svg.transition().duration(300).call(zoom.scaleBy, 0.7),
    resetView: () => svg.transition().duration(300).call(zoom.transform, d3.zoomIdentity),
    applyFilter,
  };
}

/**
 * Extract a subgraph centered on focusId via BFS.
 */
export function extractSubgraph(graphData, focusId) {
  if (!graphData?.nodes?.length || !focusId) return null;
  const nodeMap = new Map(graphData.nodes.map((n) => [n.id, n]));
  const focus = nodeMap.get(focusId);
  if (!focus) return null;

  const visited = new Set([focusId]);
  const queue = [focusId];
  // Gather direct edges from focus + edges pointing to focus
  while (queue.length) {
    const id = queue.shift();
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
