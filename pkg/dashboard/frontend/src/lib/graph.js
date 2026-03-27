import * as d3 from 'd3';
import { statusColor } from './helpers.js';

const NODE_W = 160;
const NODE_H = 44;
const COL_SPACING = NODE_W + 60;
const ROW_SPACING = NODE_H + 30;

function computeDepths(rawNodes) {
  const adjList = {};
  const incoming = {};
  rawNodes.forEach((n) => { incoming[n.id] = 0; adjList[n.id] = []; });
  rawNodes.forEach((rn) => {
    (rn.edges || []).forEach((e) => {
      if (incoming[e.targetId] !== undefined) {
        adjList[rn.id].push(e.targetId);
        incoming[e.targetId]++;
      }
    });
  });

  const depths = {};
  const queue = [];
  rawNodes.forEach((n) => { if (incoming[n.id] === 0) { queue.push(n.id); depths[n.id] = 0; } });
  while (queue.length) {
    const cur = queue.shift();
    (adjList[cur] || []).forEach((tid) => {
      if (depths[tid] === undefined || depths[tid] < depths[cur] + 1) {
        depths[tid] = depths[cur] + 1;
        queue.push(tid);
      }
    });
  }
  return depths;
}

function normalizeStatus(status) {
  if (status === 'external') return 'External';
  if (status === 'Unknown') return 'Unmonitored';
  return status || 'Unknown';
}

function displayStatus(status) {
  return status === 'Unmonitored' ? 'Unmonitored' : status === 'External' ? 'External' : status;
}

function closestBoxPoint(x, y, w, h, px, py) {
  const cx = x + w / 2, cy = y + h / 2;
  const dx = px - cx, dy = py - cy;
  if (dx === 0 && dy === 0) return [cx, y];
  const scaleX = (w / 2) / (Math.abs(dx) || 1);
  const scaleY = (h / 2) / (Math.abs(dy) || 1);
  const scale = Math.min(scaleX, scaleY);
  return [cx + dx * scale, cy + dy * scale];
}

function isBroken(status) {
  return status === 'Invalid' || status === 'Degraded';
}

function buildImpactChain(nodeId, reverseDeps) {
  const chain = new Set();
  const q = [nodeId];
  while (q.length) {
    const cur = q.shift();
    (reverseDeps[cur] || []).forEach((dep) => {
      if (!chain.has(dep)) { chain.add(dep); q.push(dep); }
    });
  }
  return chain;
}

/**
 * Renders a D3 force-directed graph into a container element.
 * Returns a cleanup function and control methods.
 */
export function renderGraph(container, graphData, options = {}) {
  const { focusId = null, cachedPositions = {}, onNavigate = null } = options;
  const rawNodes = graphData?.nodes;
  if (!rawNodes?.length) {
    container.innerHTML = '<div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:40px">No dependency relationships to display</div>';
    return { destroy() {}, nodes: [], zoomIn() {}, zoomOut() {}, resetView() {} };
  }

  const rect = container.getBoundingClientRect();
  const width = rect.width || 800;
  const height = rect.height || 500;
  const markerPrefix = focusId ? 'svc-arrow-' : 'arrow-';

  const depths = computeDepths(rawNodes);

  const byDepth = {};
  let maxDepth = 0;
  rawNodes.forEach((n) => {
    const d = depths[n.id] || 0;
    if (!byDepth[d]) byDepth[d] = [];
    byDepth[d].push(n.id);
    if (d > maxDepth) maxDepth = d;
  });

  const nodeMap = {};
  const nodes = [];
  const links = [];

  rawNodes.forEach((rn) => {
    const status = normalizeStatus(rn.status);
    const d = depths[rn.id] || 0;
    const col = byDepth[d] || [rn.id];
    const row = col.indexOf(rn.id);
    const totalH = col.length * ROW_SPACING;
    const cached = cachedPositions[rn.id];
    const node = {
      id: rn.id, serviceName: rn.serviceName, status, source: rn.source || '',
      edges: rn.edges || [], depth: d,
      isFocus: rn.id === focusId,
      x: cached ? cached.x : width / 2 - (maxDepth * COL_SPACING) / 2 + d * COL_SPACING,
      y: cached ? cached.y : height / 2 - totalH / 2 + row * ROW_SPACING,
    };
    nodes.push(node);
    nodeMap[node.id] = node;
  });

  nodes.forEach((n) => {
    n.edges.forEach((e) => {
      if (nodeMap[e.targetId]) {
        links.push({ source: n.id, target: e.targetId, required: e.required, type: e.type || 'dependency' });
      }
    });
  });

  // Save existing zoom transform before replacing SVG
  let savedTransform = null;
  const existing = container.querySelector('svg');
  if (existing) {
    try { savedTransform = d3.zoomTransform(existing); } catch (_) {}
    existing.remove();
  }

  const svg = d3.select(container).append('svg')
    .attr('width', '100%')
    .attr('height', '100%')
    .style('display', 'block');

  const zoom = d3.zoom()
    .scaleExtent([0.2, 4])
    .on('zoom', (event) => g.attr('transform', event.transform));
  svg.call(zoom);
  const g = svg.append('g');

  // Arrow markers
  const defs = svg.append('defs');
  ['required', 'optional', 'reference'].forEach((type) => {
    defs.append('marker')
      .attr('id', markerPrefix + type)
      .attr('viewBox', '0 0 10 6')
      .attr('refX', 10).attr('refY', 3)
      .attr('markerWidth', 5).attr('markerHeight', 4)
      .attr('orient', 'auto')
      .append('path')
      .attr('d', 'M0,0 L10,3 L0,6 Z')
      .attr('fill', type === 'reference' ? 'var(--accent)' : type === 'required' ? 'var(--text-secondary)' : 'var(--text-dim)');
  });

  // Simulation
  const sim = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).id((d) => d.id).distance(180).strength(0.7))
    .force('charge', d3.forceManyBody().strength(-500))
    .force('x', d3.forceX((d) => width / 2 - (maxDepth * COL_SPACING) / 2 + d.depth * COL_SPACING).strength(0.3))
    .force('y', d3.forceY(height / 2).strength(0.05))
    .force('collision', d3.forceCollide().radius(80))
    .alphaDecay(0.06)
    .velocityDecay(0.5);

  // Links
  const link = g.selectAll('.edge-line')
    .data(links)
    .join('line')
    .attr('class', (d) => 'edge-line' + (d.type === 'reference' ? ' edge-reference' : ''))
    .attr('stroke', (d) => d.type === 'reference' ? 'var(--accent)' : d.required ? 'var(--text-secondary)' : 'var(--text-dim)')
    .attr('stroke-width', (d) => d.type === 'reference' ? 1.5 : d.required ? 2 : 1)
    .attr('stroke-dasharray', (d) => d.type === 'reference' ? '6,4' : d.required ? null : '4,3')
    .attr('marker-end', (d) => `url(#${markerPrefix}${d.type === 'reference' ? 'reference' : d.required ? 'required' : 'optional'})`)
    .attr('opacity', 0.7);

  // Nodes
  const nodeGroup = g.selectAll('.node-group')
    .data(nodes)
    .join('g')
    .attr('class', (d) => 'node-group' + (d.status === 'External' ? ' node-external' : ''))
    .attr('transform', (d) => `translate(${d.x},${d.y})`)
    .on('click', (event, d) => {
      if (event.defaultPrevented || d.status === 'External') return;
      onNavigate?.(d.serviceName);
    })
    .on('dblclick', (event, d) => {
      if (focusId) return; // no dblclick unpin for service graphs
      event.preventDefault();
      d.fx = null; d.fy = null;
      sim.alphaTarget(0.1).restart();
      setTimeout(() => sim.alphaTarget(0), 500);
    })
    .call(d3.drag()
      .on('start', (event, d) => {
        if (!event.active) sim.alphaTarget(0.3).restart();
        d.fx = d.x; d.fy = d.y;
      })
      .on('drag', (event, d) => { d.fx = event.x; d.fy = event.y; })
      .on('end', (event, d) => {
        if (!event.active) sim.alphaTarget(0);
        cachedPositions[d.id] = { x: d.x, y: d.y };
      })
    );

  nodeGroup.append('rect')
    .attr('width', NODE_W).attr('height', NODE_H)
    .attr('rx', 6)
    .attr('fill', 'var(--bg-surface)')
    .attr('stroke', (d) => d.isFocus ? 'var(--accent)' : statusColor(d.status))
    .attr('stroke-width', (d) => d.isFocus ? 2.5 : 1.5);

  nodeGroup.append('text')
    .attr('class', 'node-label')
    .attr('x', 10).attr('y', 18)
    .attr('font-weight', (d) => d.isFocus ? '700' : null)
    .text((d) => d.serviceName.length > 20 ? d.serviceName.substring(0, 18) + '...' : d.serviceName);

  nodeGroup.append('text')
    .attr('class', 'node-status')
    .attr('x', 10).attr('y', 34)
    .attr('fill', (d) => statusColor(d.status))
    .text((d) => displayStatus(d.status));

  // Impact chain
  const reverseDeps = {};
  links.forEach((l) => {
    if (l.required) {
      const tid = typeof l.target === 'object' ? l.target.id : l.target;
      const sid = typeof l.source === 'object' ? l.source.id : l.source;
      if (!reverseDeps[tid]) reverseDeps[tid] = [];
      reverseDeps[tid].push(sid);
    }
  });

  const allImpacted = new Set();
  nodes.filter((n) => isBroken(n.status)).forEach((bn) => {
    buildImpactChain(bn.id, reverseDeps).forEach((id) => allImpacted.add(id));
  });

  nodeGroup.filter((d) => allImpacted.has(d.id) && !isBroken(d.status))
    .append('text')
    .attr('class', 'node-impact-icon')
    .attr('x', NODE_W - 20).attr('y', 16)
    .attr('fill', 'var(--warning)')
    .text('\u26A0');

  // Hover impact highlighting
  nodeGroup.on('mouseenter', (event, d) => {
    if (!isBroken(d.status)) return;
    const chain = buildImpactChain(d.id, reverseDeps);
    chain.add(d.id);
    nodeGroup.classed('graph-highlight', (n) => chain.has(n.id));
    link.classed('graph-impact', (l) => {
      const sid = typeof l.source === 'object' ? l.source.id : l.source;
      const tid = typeof l.target === 'object' ? l.target.id : l.target;
      return chain.has(sid) && chain.has(tid);
    });
  }).on('mouseleave', () => {
    nodeGroup.classed('graph-highlight', false);
    link.classed('graph-impact', false);
  });

  function updatePositions() {
    link.each(function (d) {
      const s = d.source, t = d.target;
      const sp = closestBoxPoint(s.x, s.y, NODE_W, NODE_H, t.x + NODE_W / 2, t.y + NODE_H / 2);
      const tp = closestBoxPoint(t.x, t.y, NODE_W, NODE_H, s.x + NODE_W / 2, s.y + NODE_H / 2);
      d3.select(this).attr('x1', sp[0]).attr('y1', sp[1]).attr('x2', tp[0]).attr('y2', tp[1]);
    });
    nodeGroup.attr('transform', (d) => `translate(${d.x},${d.y})`);
  }

  sim.on('tick', updatePositions);

  // Run simulation synchronously for initial layout
  sim.stop();
  for (let i = 0; i < 150; i++) sim.tick();
  updatePositions();
  nodes.forEach((n) => { cachedPositions[n.id] = { x: n.x, y: n.y }; });

  function fitToView() {
    const bounds = g.node().getBBox();
    if (bounds.width === 0 || bounds.height === 0) return;
    const pad = 60;
    const scale = Math.min(width / (bounds.width + pad * 2), height / (bounds.height + pad * 2), 1.5);
    const tx = (width - bounds.width * scale) / 2 - bounds.x * scale;
    const ty = (height - bounds.height * scale) / 2 - bounds.y * scale;
    svg.transition().duration(400).call(zoom.transform, d3.zoomIdentity.translate(tx, ty).scale(scale));
  }

  if (savedTransform && !focusId) {
    svg.call(zoom.transform, savedTransform);
  } else {
    setTimeout(fitToView, 50);
  }

  return {
    nodes,
    svg,
    nodeGroup,
    link,
    destroy() {
      sim.stop();
      svg.remove();
    },
    zoomIn() { svg.transition().duration(300).call(zoom.scaleBy, 1.3); },
    zoomOut() { svg.transition().duration(300).call(zoom.scaleBy, 0.7); },
    resetView() {
      nodes.forEach((n) => { n.fx = null; n.fy = null; });
      Object.keys(cachedPositions).forEach((k) => delete cachedPositions[k]);
      sim.stop();
      sim.alpha(1);
      for (let i = 0; i < 150; i++) sim.tick();
      nodes.forEach((n) => { cachedPositions[n.id] = { x: n.x, y: n.y }; });
      updatePositions();
      fitToView();
    },
    applyFilter(filterFn) {
      nodeGroup.classed('graph-dimmed', (d) => filterFn(d));
      link.classed('graph-dimmed', (d) => {
        const src = typeof d.source === 'object' ? d.source : null;
        const tgt = typeof d.target === 'object' ? d.target : null;
        return (src && filterFn(src)) || (tgt && filterFn(tgt));
      });
    },
    updateNodeStyles(data) {
      if (!data?.nodes) return;
      const statusMap = {};
      data.nodes.forEach((n) => { statusMap[n.id] = n.status || 'Unknown'; });
      nodeGroup.each(function (d) {
        const newStatus = normalizeStatus(statusMap[d.id]);
        if (!newStatus || newStatus === d.status) return;
        d.status = newStatus;
        d3.select(this).select('rect').attr('stroke', statusColor(newStatus));
        d3.select(this).select('.node-status')
          .attr('fill', statusColor(newStatus))
          .text(displayStatus(newStatus));
      });
    },
  };
}

export function extractSubgraph(graphData, focusId) {
  if (!graphData?.nodes) return null;
  const nodeMap = {};
  graphData.nodes.forEach((n) => { nodeMap[n.id] = n; });

  const forward = {};
  const reverse = {};
  graphData.nodes.forEach((n) => {
    forward[n.id] = [];
    if (!reverse[n.id]) reverse[n.id] = [];
    (n.edges || []).forEach((e) => {
      forward[n.id].push(e.targetId);
      if (!reverse[e.targetId]) reverse[e.targetId] = [];
      reverse[e.targetId].push(n.id);
    });
  });

  const visited = {};
  const queue = [focusId];
  visited[focusId] = true;
  while (queue.length) {
    const cur = queue.shift();
    (forward[cur] || []).forEach((id) => { if (!visited[id]) { visited[id] = true; queue.push(id); } });
    (reverse[cur] || []).forEach((id) => { if (!visited[id]) { visited[id] = true; queue.push(id); } });
  }

  const subNodes = graphData.nodes.filter((n) => visited[n.id]);
  return subNodes.length ? { nodes: subNodes } : null;
}
