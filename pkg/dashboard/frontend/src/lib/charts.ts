/**
 * D3 chart renderers for dashboard aggregations.
 * Each render fn takes a container element, data and options, draws an SVG.
 */
import * as d3 from 'd3';
import { readinessBucketLabel, ownerKeyLabel, ownerKeyKind } from './format';
import { resolvePalette, sharedTooltip, defineGradients, animateIn, emptyState } from './chartkit';
import { categoryIconInner } from './categoryIcons';

/** Horizontal stacked bar chart for readiness category breakdown. */
export interface CategoryBarData {
  category: string;
  done: number;
  partial: number;
  notDone: number;
  deferred: number;
}

export interface CategoryBarOptions {
  onSelect?: (category: string) => void;
}

export function renderCategoryStackedBars(
  container: HTMLElement,
  data: CategoryBarData[],
  opts: CategoryBarOptions = {},
): void {
  // Clear
  d3.select(container).selectAll('*').remove();

  if (!data.length) {
    emptyState(container, 'No category data');
    return;
  }

  const pal = resolvePalette(container);
  const radius = parseFloat(getComputedStyle(container).getPropertyValue('--chart-radius')) || 6;

  const margin = { top: 20, right: 140, bottom: 40, left: 120 };

  // Compute minimum width to include legend at the right.
  const legendLabels = ['Done', 'Partial', 'Not done', 'Deferred'];
  const charW = 6.5;
  const swatch = 12;
  const swatchGap = 6;
  const legendItemWidth = Math.max(...legendLabels.map(l => swatch + swatchGap + l.length * charW));
  const minWidth = margin.left + 200 + margin.right; // 200 for bars, rest for legend

  const width = Math.max(minWidth, container.clientWidth || 400);
  const height = Math.max(220, data.length * 35 + margin.top + margin.bottom);

  const svg = d3.select(container)
    .append('svg')
    .attr('width', '100%')
    .attr('viewBox', `0 0 ${width} ${height}`)
    .attr('preserveAspectRatio', 'xMidYMid meet')
    .attr('role', 'img')
    .attr('aria-label', 'Readiness category breakdown chart')
    .style('font-family', 'var(--font-sans)');

  defineGradients(svg, pal);

  const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`);

  const innerWidth = width - margin.left - margin.right;
  const innerHeight = height - margin.top - margin.bottom;

  // Stack keys
  const keys = ['done', 'partial', 'notDone', 'deferred'];
  const stack = d3.stack<CategoryBarData>().keys(keys);
  const series = stack(data);

  // X scale: total checks
  const xMax = d3.max(data, (d) => d.done + d.partial + d.notDone + d.deferred) || 100;
  const x = d3.scaleLinear().domain([0, xMax]).range([0, innerWidth]);

  // Y scale: categories
  const y = d3.scaleBand()
    .domain(data.map((d) => d.category))
    .range([0, innerHeight])
    .padding(0.3);

  // Bars — gradients by status, rounded ends, 2px surface gap between segments.
  const gradientMap: Record<string, string> = { done: 'url(#grad-ok)', partial: 'url(#grad-warn)', notDone: 'url(#grad-err)', deferred: 'url(#grad-neutral)' };

  const tooltip = sharedTooltip();
  tooltip.attach(container);

  const rects = g.selectAll('g.layer')
    .data(series)
    .join('g')
    .attr('class', 'layer')
    .attr('fill', (d) => gradientMap[d.key])
    .selectAll('rect')
    .data((d) => d)
    .join('rect')
    .attr('x', (d) => Math.max(0, x(d[0]) + 1)) // 1px inset for surface gap
    .attr('y', (d) => y((d.data as CategoryBarData).category) || 0)
    .attr('width', (d) => Math.max(0, x(d[1]) - x(d[0]) - 2)) // -2px for gap
    .attr('height', y.bandwidth())
    .attr('rx', radius)
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('mouseenter', function (event, d) {
      d3.select(this).transition().duration(150).attr('opacity', 0.8);
      const cat = d.data as CategoryBarData;
      const total = cat.done + cat.partial + cat.notDone + cat.deferred;
      const content = `${cat.category} — done ${cat.done} · partial ${cat.partial} · not-done ${cat.notDone} · deferred ${cat.deferred} · total: ${total} checks`;
      tooltip.show(content, event.offsetX + 10, event.offsetY - 10);
    })
    .on('mousemove', function (event) {
      tooltip.show(tooltip['el'].textContent || '', event.offsetX + 10, event.offsetY - 10);
    })
    .on('mouseleave', function () {
      d3.select(this).transition().duration(150).attr('opacity', 1);
      tooltip.hide();
    })
    .on('click', (_, d) => {
      if (opts.onSelect) opts.onSelect((d.data as CategoryBarData).category);
    });

  // Enter animation: width 0→value.
  animateIn(rects, { attr: 'width', from: 0, to: (d) => Math.max(0, x(d[1]) - x(d[0]) - 2) });

  // Y axis: category labels — muted chrome (no domain line, no tick marks).
  const yAxis = g.append('g').call(d3.axisLeft(y).tickSize(0));
  yAxis.select('.domain').remove();
  yAxis.selectAll('.tick line').remove();
  yAxis.selectAll('text')
    .style('font-size', 'var(--text-sm)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text)')
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('click', (_, cat) => {
      if (opts.onSelect) opts.onSelect(String(cat));
    });

  // X axis: count — muted chrome.
  const xAxis = g.append('g')
    .attr('transform', `translate(0,${innerHeight})`)
    .call(d3.axisBottom(x).ticks(5).tickFormat(d3.format('d')));
  xAxis.select('.domain').remove();
  xAxis.selectAll('.tick line').remove();
  xAxis.selectAll('text')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)');

  // Legend — positioned within the viewBox so it never clips.
  const legend = svg.append('g')
    .attr('transform', `translate(${innerWidth + margin.left + 20}, ${margin.top})`);

  const legendData = [
    { label: 'Done', color: pal.ok },
    { label: 'Partial', color: pal.warn },
    { label: 'Not done', color: pal.err },
    { label: 'Deferred', color: pal.neutral },
  ];

  const legendItems = legend.selectAll('g')
    .data(legendData)
    .join('g')
    .attr('transform', (_, i) => `translate(0, ${i * 20})`);

  legendItems.append('rect')
    .attr('width', 12)
    .attr('height', 12)
    .attr('rx', 2)
    .attr('fill', (d) => d.color);

  legendItems.append('text')
    .attr('x', 18)
    .attr('y', 6)
    .attr('dominant-baseline', 'middle')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text)')
    .text((d) => d.label);
}

/** Donut chart for readiness status distribution. */
export interface ReadinessDonutData {
  ready: number;
  partial: number;
  notReady: number;
  notConfigured: number;
}

export interface ReadinessDonutOptions {
  onSelect?: (bucket: string) => void;
}

export function renderReadinessDonut(
  container: HTMLElement,
  data: ReadinessDonutData,
  opts: ReadinessDonutOptions = {},
): void {
  // Clear
  d3.select(container).selectAll('*').remove();

  const total = data.ready + data.partial + data.notReady + data.notConfigured;
  if (total === 0) {
    emptyState(container, 'No readiness data');
    return;
  }

  const pal = resolvePalette(container);

  const donutRadius = 70;
  const legendWidth = 150;
  const width = donutRadius * 2 + 40 + legendWidth;
  const height = 220;

  const svg = d3.select(container)
    .append('svg')
    .attr('width', '100%')
    .attr('viewBox', `0 0 ${width} ${height}`)
    .attr('preserveAspectRatio', 'xMidYMid meet')
    .attr('role', 'img')
    .attr('aria-label', 'Readiness status distribution donut chart')
    .style('font-family', 'var(--font-sans)');

  defineGradients(svg, pal);

  const g = svg.append('g').attr('transform', `translate(${donutRadius + 20}, ${height / 2})`);

  // Pie layout
  const pieData = [
    { label: readinessBucketLabel('ready'), value: data.ready, bucket: 'ready', gradient: 'url(#grad-ok)', color: pal.ok },
    { label: readinessBucketLabel('partial'), value: data.partial, bucket: 'partial', gradient: 'url(#grad-warn)', color: pal.warn },
    { label: readinessBucketLabel('not-ready'), value: data.notReady, bucket: 'not-ready', gradient: 'url(#grad-err)', color: pal.err },
    { label: readinessBucketLabel('unknown'), value: data.notConfigured, bucket: 'unknown', gradient: 'url(#grad-neutral)', color: pal.neutral },
  ].filter((d) => d.value > 0);

  const pie = d3.pie<{ label: string; value: number; bucket: string; gradient: string }>()
    .value((d) => d.value)
    .sort(null);

  const arc = d3.arc<d3.PieArcDatum<{ label: string; value: number; bucket: string; gradient: string }>>()
    .innerRadius(donutRadius * 0.6)
    .outerRadius(donutRadius);

  const tooltip = sharedTooltip();
  tooltip.attach(container);

  const paths = g.selectAll('path')
    .data(pie(pieData))
    .join('path')
    .attr('d', arc)
    .attr('fill', (d) => d.data.gradient)
    .attr('stroke', 'var(--c-surface)')
    .attr('stroke-width', 2)
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('mouseenter', function (event, d) {
      d3.select(this).transition().duration(150).attr('opacity', 0.8);
      const pct = Math.round((d.data.value / total) * 100);
      const content = `${d.data.label} — ${d.data.value} (${pct}%)`;
      const rect = (this as SVGPathElement).getBoundingClientRect();
      const containerRect = container.getBoundingClientRect();
      tooltip.show(content, rect.left - containerRect.left + rect.width / 2, rect.top - containerRect.top - 10);
    })
    .on('mousemove', function (event, d) {
      const pct = Math.round((d.data.value / total) * 100);
      const content = `${d.data.label} — ${d.data.value} (${pct}%)`;
      const rect = (this as SVGPathElement).getBoundingClientRect();
      const containerRect = container.getBoundingClientRect();
      tooltip.show(content, rect.left - containerRect.left + rect.width / 2, rect.top - containerRect.top - 10);
    })
    .on('mouseleave', function () {
      d3.select(this).transition().duration(150).attr('opacity', 1);
      tooltip.hide();
    })
    .on('click', (_, d) => {
      if (opts.onSelect) opts.onSelect(d.data.bucket);
    });

  // Enter animation: fade in.
  animateIn(paths, { attr: 'opacity', from: 0, to: () => 1 });

  // Center label: total
  g.append('text')
    .attr('text-anchor', 'middle')
    .attr('dominant-baseline', 'middle')
    .attr('y', -8)
    .style('font-size', 'var(--text-xl)')
    .style('font-weight', '600')
    .style('fill', 'var(--c-text)')
    .text(total);

  g.append('text')
    .attr('text-anchor', 'middle')
    .attr('dominant-baseline', 'middle')
    .attr('y', 14)
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)')
    .text('services');

  // Legend
  const legend = svg.append('g')
    .attr('transform', `translate(${donutRadius * 2 + 40}, ${height / 2 - pieData.length * 10})`);

  const legendItems = legend.selectAll('g')
    .data(pieData)
    .join('g')
    .attr('transform', (_, i) => `translate(0, ${i * 20})`);

  legendItems.append('circle')
    .attr('cx', 6)
    .attr('cy', 6)
    .attr('r', 6)
    .attr('fill', (d) => d.color);

  legendItems.append('text')
    .attr('x', 18)
    .attr('y', 6)
    .attr('dominant-baseline', 'middle')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text)')
    .text((d) => `${d.label} (${d.value})`);
}

/** Horizontal stacked bar chart for per-owner readiness composition. */
export interface OwnerBarData {
  key: string;
  services: number;
  ready: number;
  partial: number;
  notReady: number;
  notConfigured: number;
}

export interface OwnerBarOptions {
  onSelect?: (key: string) => void;
}

export function renderOwnerBars(
  container: HTMLElement,
  data: OwnerBarData[],
  opts: OwnerBarOptions = {},
): void {
  // Clear
  d3.select(container).selectAll('*').remove();

  if (!data.length) {
    emptyState(container, 'No owner data');
    return;
  }

  // Limit to top 15 owners for readability
  const topData = data.slice(0, 15);

  const pal = resolvePalette(container);
  const radius = parseFloat(getComputedStyle(container).getPropertyValue('--chart-radius')) || 6;

  // Legend lives BELOW the bars so it is never clipped at the right edge.
  const legendHeight = 28;

  // The left margin must fit the WIDEST owner label, else long keys
  // (e.g. "platform-foundations") clip on the left and lose leading chars.
  // y-axis labels render at --text-sm (14px); estimate ~7px/char + padding.
  const labelCharW = 7;
  const labelPad = 16;
  const longestLabel = topData.reduce((max, d) => Math.max(max, d.key.length), 0);
  const marginLeft = Math.max(120, Math.ceil(longestLabel * labelCharW) + labelPad);
  const margin = { top: 16, right: 48, bottom: 40 + legendHeight, left: marginLeft };

  // Compute minimum width to fit the horizontal legend.
  const swatch = 12;
  const swatchGap = 6;
  const itemGap = 20;
  const charW = 6.5;
  const legendLabels = [
    readinessBucketLabel('ready'),
    readinessBucketLabel('partial'),
    readinessBucketLabel('not-ready'),
    readinessBucketLabel('unknown'),
  ];
  const legendWidth = legendLabels.reduce((sum, label) => sum + swatch + swatchGap + label.length * charW + itemGap, 0);
  const minWidth = Math.max(520, margin.left + legendWidth + margin.right);

  // The intrinsic coordinate width: fill the container, fall back to a sane default, floor at minWidth.
  const width = Math.max(minWidth, container.clientWidth || 600);
  const height = Math.max(200, topData.length * 40 + margin.top + margin.bottom);

  // viewBox + 100% width makes the chart responsive and keeps the legend in frame.
  const svg = d3.select(container)
    .append('svg')
    .attr('width', '100%')
    .attr('viewBox', `0 0 ${width} ${height}`)
    .attr('preserveAspectRatio', 'xMidYMid meet')
    .attr('role', 'img')
    .attr('aria-label', 'Owner readiness breakdown chart')
    .style('font-family', 'var(--font-sans)');

  defineGradients(svg, pal);

  const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`);

  const innerWidth = width - margin.left - margin.right;
  const innerHeight = height - margin.top - margin.bottom;

  // Stack keys: readiness composition.
  const keys = ['ready', 'partial', 'notReady', 'notConfigured'];

  const stackData = topData.map((d) => ({
    key: d.key,
    services: d.services,
    ready: d.ready,
    partial: d.partial,
    notReady: d.notReady,
    notConfigured: d.notConfigured,
  }));

  const stack = d3.stack<typeof stackData[0]>().keys(keys);
  const series = stack(stackData);

  // X scale: total services
  const xMax = d3.max(stackData, (d) => d.services) || 10;
  const x = d3.scaleLinear().domain([0, xMax]).range([0, innerWidth]);

  // Y scale: owner keys
  const y = d3.scaleBand()
    .domain(stackData.map((d) => d.key))
    .range([0, innerHeight])
    .padding(0.25);

  // Gradient map — readiness palette.
  const gradientMap: Record<string, string> = {
    ready: 'url(#grad-ok)',
    partial: 'url(#grad-warn)',
    notReady: 'url(#grad-err)',
    notConfigured: 'url(#grad-neutral)',
  };

  const tooltip = sharedTooltip();
  tooltip.attach(container);

  // Bars — gradients, rounded ends, 2px surface gap between segments.
  const rects = g.selectAll('g.layer')
    .data(series)
    .join('g')
    .attr('class', 'layer')
    .attr('fill', (d) => gradientMap[d.key])
    .selectAll('rect')
    .data((d) => d)
    .join('rect')
    .attr('x', (d) => Math.max(0, x(d[0]) + 1)) // 1px inset for surface gap
    .attr('y', (d) => y((d.data as typeof stackData[0]).key) || 0)
    .attr('width', (d) => Math.max(0, x(d[1]) - x(d[0]) - 2)) // -2px for gap
    .attr('height', y.bandwidth())
    .attr('rx', radius)
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('mouseenter', function (event, d) {
      d3.select(this).transition().duration(150).attr('opacity', 0.8);
      const owner = topData.find((o) => o.key === (d.data as typeof stackData[0]).key);
      if (!owner) return;
      const content = `${owner.key} — ${owner.services} services · ready ${owner.ready} · partial ${owner.partial} · not ready ${owner.notReady} · not configured ${owner.notConfigured}`;
      tooltip.show(content, event.offsetX + 10, event.offsetY - 10);
    })
    .on('mousemove', function (event) {
      tooltip.show(tooltip['el'].textContent || '', event.offsetX + 10, event.offsetY - 10);
    })
    .on('mouseleave', function () {
      d3.select(this).transition().duration(150).attr('opacity', 1);
      tooltip.hide();
    })
    .on('click', (_, d) => {
      if (opts.onSelect) opts.onSelect((d.data as typeof stackData[0]).key);
    });

  // Enter animation: width 0→value.
  animateIn(rects, { attr: 'width', from: 0, to: (d) => Math.max(0, x(d[1]) - x(d[0]) - 2) });

  // Y axis: owner labels — muted chrome (no domain line, no tick marks).
  const yAxis = g.append('g').call(d3.axisLeft(y).tickSize(0));
  yAxis.select('.domain').remove();
  yAxis.selectAll('.tick line').remove();
  yAxis.selectAll('text')
    .style('font-size', 'var(--text-sm)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text)')
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('click', (_, key) => {
      if (opts.onSelect) opts.onSelect(String(key));
    });

  // X axis: service count (integer ticks) — muted chrome.
  const xAxis = g.append('g')
    .attr('transform', `translate(0,${innerHeight})`)
    .call(d3.axisBottom(x).ticks(5).tickFormat(d3.format('d')));
  xAxis.select('.domain').remove();
  xAxis.selectAll('.tick line').remove();
  xAxis.selectAll('text')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)');

  // Total service count labels at the end of each bar
  g.selectAll('text.bar-label')
    .data(stackData)
    .join('text')
    .attr('class', 'bar-label')
    .attr('x', (d) => x(d.services) + 6)
    .attr('y', (d) => (y(d.key) || 0) + y.bandwidth() / 2)
    .attr('text-anchor', 'start')
    .attr('dominant-baseline', 'middle')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text)')
    .text((d) => d.services);

  // Legend — placed below the bars, horizontally, so it always fits the frame.
  const legendData = [
    { label: readinessBucketLabel('ready'), color: pal.ok },
    { label: readinessBucketLabel('partial'), color: pal.warn },
    { label: readinessBucketLabel('not-ready'), color: pal.err },
    { label: readinessBucketLabel('unknown'), color: pal.neutral },
  ];

  const legend = svg.append('g')
    .attr('transform', `translate(${margin.left}, ${height - legendHeight + 6})`);

  let legendX = 0;

  legendData.forEach((d) => {
    const item = legend.append('g').attr('transform', `translate(${legendX}, 0)`);
    item.append('rect')
      .attr('width', swatch)
      .attr('height', swatch)
      .attr('y', -swatch / 2)
      .attr('rx', 2)
      .attr('fill', d.color);
    item.append('text')
      .attr('x', swatch + swatchGap)
      .attr('dominant-baseline', 'middle')
      .style('font-size', 'var(--text-xs)')
      .style('font-weight', '500')
      .style('fill', 'var(--c-text-3)')
      .text(d.label);
    legendX += swatch + swatchGap + d.label.length * charW + itemGap;
  });
}

/** Status→gradient fill map for treemap tiles. */
const STATUS_TO_PAL: Record<string, string> = {
  Compliant: 'url(#grad-ok)',
  Warning: 'url(#grad-warn)',
  NonCompliant: 'url(#grad-err)',
  Reference: 'url(#grad-info)',
  Unknown: 'url(#grad-neutral)',
};

/** Treemap fleet risk map: tile size = blast radius, color = status. */
export interface TreemapDatum {
  name: string;
  value: number;
  status: string;
  blast: number;
}

export interface TreemapOptions {
  onSelect?: (name: string) => void;
}

export function renderTreemap(
  container: HTMLElement,
  data: TreemapDatum[],
  opts: TreemapOptions = {},
): void {
  // Clear
  d3.select(container).selectAll('*').remove();

  if (!data.length) {
    emptyState(container, 'No services to map');
    return;
  }

  const pal = resolvePalette(container);
  const radius = parseFloat(getComputedStyle(container).getPropertyValue('--chart-radius')) || 6;

  const width = container.clientWidth || 600;
  // Reserve a dedicated band below the tiles for the legend so it never
  // overlaps a coloured tile (which destroyed its contrast).
  const statusesPresent = [...new Set(data.map((d) => d.status))].filter(Boolean);
  const statusLegendData = [
    { label: 'Compliant', color: pal.ok, status: 'Compliant' },
    { label: 'Warning', color: pal.warn, status: 'Warning' },
    { label: 'Non-Compliant', color: pal.err, status: 'NonCompliant' },
    { label: 'Reference', color: pal.info, status: 'Reference' },
    { label: 'Unknown', color: pal.neutral, status: 'Unknown' },
  ].filter((d) => statusesPresent.includes(d.status));
  const legendH = statusLegendData.length > 0 ? 24 : 0;
  const height = 260;
  const treemapHeight = height - legendH;

  const svg = d3.select(container)
    .append('svg')
    .attr('width', '100%')
    .attr('viewBox', `0 0 ${width} ${height}`)
    .attr('preserveAspectRatio', 'xMidYMid meet')
    .attr('role', 'img')
    .attr('aria-label', 'Fleet risk treemap')
    .style('font-family', 'var(--font-sans)');

  defineGradients(svg, pal);

  // Treemap layout
  const root = d3.hierarchy<TreemapDatum>({ children: data } as any)
    .sum((d) => d.value || 0);

  const treemap = d3.treemap<TreemapDatum>()
    .size([width, treemapHeight])
    .paddingInner(2);

  treemap(root);

  const tooltip = sharedTooltip();
  tooltip.attach(container);

  // ponytail: cast to HierarchyRectangularNode for x0/y0/x1/y1 access
  const leaves = root.leaves() as Array<d3.HierarchyRectangularNode<TreemapDatum>>;

  const tiles = svg.selectAll('g')
    .data(leaves)
    .join('g');

  // Tile rectangles
  tiles.append('rect')
    .attr('x', (d) => d.x0)
    .attr('y', (d) => d.y0)
    .attr('width', (d) => Math.max(0, d.x1 - d.x0))
    .attr('height', (d) => Math.max(0, d.y1 - d.y0))
    .attr('fill', (d) => STATUS_TO_PAL[d.data.status] || 'url(#grad-neutral)')
    .attr('rx', radius)
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('mouseenter', function (event, d) {
      d3.select(this).transition().duration(150).attr('opacity', 0.8);
      const content = `${d.data.name} · ${d.data.status} · blast ${d.data.blast}`;
      tooltip.show(content, event.offsetX + 10, event.offsetY - 10);
    })
    .on('mousemove', function (event) {
      tooltip.show(tooltip['el'].textContent || '', event.offsetX + 10, event.offsetY - 10);
    })
    .on('mouseleave', function () {
      d3.select(this).transition().duration(150).attr('opacity', 1);
      tooltip.hide();
    })
    .on('click', (_, d) => {
      if (opts.onSelect) opts.onSelect(d.data.name);
    });

  // Labels: only show on tiles wide/tall enough
  tiles.append('text')
    .attr('x', (d) => (d.x0 + d.x1) / 2)
    .attr('y', (d) => (d.y0 + d.y1) / 2)
    .attr('text-anchor', 'middle')
    .attr('dominant-baseline', 'middle')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text)')
    .style('pointer-events', 'none')
    .text((d) => {
      const w = d.x1 - d.x0;
      const h = d.y1 - d.y0;
      // ponytail: heuristic truncation (6px/char, 8px margin); getComputedTextLength unavailable in jsdom
      const maxChars = Math.floor((w - 8) / 6);
      if (w < 60 || h < 30) return '';
      return d.data.name.length > maxChars && maxChars > 3 ? d.data.name.slice(0, maxChars - 1) + '…' : d.data.name;
    });

  // Enter animation: fade in
  animateIn(tiles.selectAll('rect'), { attr: 'opacity', from: 0, to: () => 1 });

  // Status legend — rendered in the reserved band BELOW the tiles (never over
  // a coloured tile) with full-contrast text so it stays readable in both themes.
  if (statusLegendData.length > 0) {
    const legend = svg.append('g').attr('transform', `translate(12, ${treemapHeight + legendH / 2})`);
    let legendX = 0;
    const swatch = 8;
    const swatchGap = 6;
    const itemGap = 16;
    const charW = 6;
    statusLegendData.forEach((d) => {
      const item = legend.append('g').attr('transform', `translate(${legendX}, 0)`);
      item.append('circle')
        .attr('cx', swatch / 2)
        .attr('cy', 0)
        .attr('r', swatch / 2)
        .attr('fill', d.color);
      item.append('text')
        .attr('x', swatch + swatchGap)
        .attr('dominant-baseline', 'middle')
        .style('font-size', 'var(--text-xs)')
        .style('font-weight', '600')
        .style('fill', 'var(--c-text)')
        .text(d.label);
      legendX += swatch + swatchGap + d.label.length * charW + itemGap;
    });
  }
}

/** Priority quadrant: readiness score (x) vs blast radius (y) scatter plot. */
export interface QuadrantDatum {
  name: string;
  x: number; // readiness score 0–100
  y: number; // blast/impact
  status: string;
  blast: number;
}

export interface QuadrantOptions {
  onSelect?: (name: string) => void;
}

export function renderPriorityQuadrant(
  container: HTMLElement,
  data: QuadrantDatum[],
  opts: QuadrantOptions = {},
): void {
  // Clear
  d3.select(container).selectAll('*').remove();

  if (!data.length) {
    emptyState(container, 'No readiness data to plot');
    return;
  }

  const pal = resolvePalette(container);

  const margin = { top: 30, right: 30, bottom: 50, left: 60 };
  const width = container.clientWidth || 600;
  const height = 300;

  const svg = d3.select(container)
    .append('svg')
    .attr('viewBox', `0 0 ${width} ${height}`)
    .attr('preserveAspectRatio', 'xMidYMid meet')
    .attr('role', 'img')
    .attr('aria-label', 'Priority quadrant scatter plot')
    .style('width', '100%')
    .style('height', 'auto')
    .style('font-family', 'var(--font-sans)');

  const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`);

  const innerWidth = width - margin.left - margin.right;
  const innerHeight = height - margin.top - margin.bottom;

  // X scale: readiness score 0–100
  const x = d3.scaleLinear().domain([0, 100]).range([0, innerWidth]);

  // Y scale: impact/blast 0–max
  const yMax = d3.max(data, (d) => d.y) || 1;
  const y = d3.scaleLinear().domain([0, yMax]).range([innerHeight, 0]);

  // Quadrant guide lines: vertical at x=50, horizontal at y=median
  const blastValues = data.map((d) => d.y).sort((a, b) => a - b);
  const medianBlast = blastValues[Math.floor(blastValues.length / 2)] || yMax / 2;

  g.append('line')
    .attr('x1', x(50))
    .attr('x2', x(50))
    .attr('y1', 0)
    .attr('y2', innerHeight)
    .attr('stroke', 'var(--c-border)')
    .attr('stroke-width', 1)
    .attr('stroke-dasharray', '4 4');

  g.append('line')
    .attr('x1', 0)
    .attr('x2', innerWidth)
    .attr('y1', y(medianBlast))
    .attr('y2', y(medianBlast))
    .attr('stroke', 'var(--c-border)')
    .attr('stroke-width', 1)
    .attr('stroke-dasharray', '4 4');

  // Quadrant labels
  g.append('text')
    .attr('x', 6)
    .attr('y', 6)
    .attr('dominant-baseline', 'hanging')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)')
    .text('Fix first');

  g.append('text')
    .attr('x', innerWidth - 6)
    .attr('y', innerHeight - 6)
    .attr('text-anchor', 'end')
    .attr('dominant-baseline', 'alphabetic')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)')
    .text('Healthy');

  // Status color map (flat, no gradients needed)
  const statusColor: Record<string, string> = {
    Compliant: pal.ok,
    Warning: pal.warn,
    NonCompliant: pal.err,
    Reference: pal.info,
  };

  const tooltip = sharedTooltip();
  tooltip.attach(container);

  // Radius scale: larger blast = larger dot (min 8px per dataviz markers)
  const maxBlast = d3.max(data, (d) => d.blast) || 1;
  const rScale = d3.scaleSqrt().domain([0, maxBlast]).range([8, 16]);

  // Dots
  const dots = g.selectAll('circle')
    .data(data)
    .join('circle')
    .attr('cx', (d) => x(d.x))
    .attr('cy', (d) => y(d.y))
    .attr('r', (d) => rScale(d.blast))
    .attr('fill', (d) => statusColor[d.status] || pal.neutral)
    .attr('stroke', 'var(--c-surface)')
    .attr('stroke-width', 2)
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('mouseenter', function (event, d) {
      d3.select(this).transition().duration(150).attr('opacity', 0.8);
      const content = `${d.name} · readiness ${d.x} · impact ${d.y} · ${d.status}`;
      tooltip.show(content, event.offsetX + 10, event.offsetY - 10);
    })
    .on('mousemove', function (event) {
      tooltip.show(tooltip['el'].textContent || '', event.offsetX + 10, event.offsetY - 10);
    })
    .on('mouseleave', function () {
      d3.select(this).transition().duration(150).attr('opacity', 1);
      tooltip.hide();
    })
    .on('click', (_, d) => {
      if (opts.onSelect) opts.onSelect(d.name);
    });

  // Enter animation: r 0→value
  animateIn(dots, { attr: 'r', from: 0, to: (d) => rScale(d.blast) });

  // X axis: readiness score
  const xAxis = g.append('g')
    .attr('transform', `translate(0,${innerHeight})`)
    .call(d3.axisBottom(x).ticks(5));
  xAxis.select('.domain').remove();
  xAxis.selectAll('.tick line').remove();
  xAxis.selectAll('text')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)');

  // Y axis: impact
  const yAxis = g.append('g').call(d3.axisLeft(y).ticks(5));
  yAxis.select('.domain').remove();
  yAxis.selectAll('.tick line').remove();
  yAxis.selectAll('text')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)');

  // Axis labels
  svg.append('text')
    .attr('x', width / 2)
    .attr('y', height - 12)
    .attr('text-anchor', 'middle')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)')
    .text('readiness →');

  svg.append('text')
    .attr('transform', `translate(12, ${height / 2}) rotate(-90)`)
    .attr('text-anchor', 'middle')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)')
    .text('impact →');

  // Status legend
  const statusesPresent = [...new Set(data.map((d) => d.status))].filter(Boolean);
  const statusLegendData = [
    { label: 'Compliant', color: pal.ok, status: 'Compliant' },
    { label: 'Warning', color: pal.warn, status: 'Warning' },
    { label: 'Non-Compliant', color: pal.err, status: 'NonCompliant' },
    { label: 'Reference', color: pal.info, status: 'Reference' },
  ].filter((d) => statusesPresent.includes(d.status));

  if (statusLegendData.length > 0) {
    const legend = svg.append('g').attr('transform', `translate(${margin.left}, ${margin.top - 12})`);
    let legendX = 0;
    const swatch = 8;
    const swatchGap = 6;
    const itemGap = 16;
    const charW = 6;
    statusLegendData.forEach((d) => {
      const item = legend.append('g').attr('transform', `translate(${legendX}, 0)`);
      item.append('circle')
        .attr('cx', swatch / 2)
        .attr('cy', -swatch / 2)
        .attr('r', swatch / 2)
        .attr('fill', d.color);
      item.append('text')
        .attr('x', swatch + swatchGap)
        .attr('dominant-baseline', 'middle')
        .style('font-size', 'var(--text-xs)')
        .style('font-weight', '500')
        .style('fill', 'var(--c-text-3)')
        .text(d.label);
      legendX += swatch + swatchGap + d.label.length * charW + itemGap;
    });
  }
}

/** Heatmap for team × category readiness. */
export interface HeatmapOptions {
  onSelectOwner?: (owner: string) => void;
  onSelectCategory?: (category: string) => void;
  onSelectCell?: (owner: string, category: string) => void;
}

export function renderHeatmap(
  container: HTMLElement,
  data: { owners: string[]; categories: string[]; cells: Array<{ owner: string; category: string; score: number; n: number }> },
  opts: HeatmapOptions = {},
): void {
  d3.select(container).selectAll('*').remove();

  if (!data.owners.length || !data.categories.length) {
    emptyState(container, 'No category data');
    return;
  }

  const pal = resolvePalette(container);
  const radius = parseFloat(getComputedStyle(container).getPropertyValue('--chart-radius')) || 6;

  // Sequential single-hue ramp: theme-aware surface → green (low scores recede in both themes)
  const surfaceInset = getComputedStyle(container).getPropertyValue('--c-surface-inset').trim() || '#e9f7ef';
  // Faint-green low end so a 0% cell still reads as ON the ramp — and stays distinct
  // from an empty "no data" cell (which keeps the plain surface fill). Single-hue.
  const rampLow = d3.interpolateRgb(surfaceInset, pal.ok)(0.14);
  const scoreFill = d3.scaleLinear<string>()
    .domain([0, 100])
    .range([rampLow, pal.ok])
    .interpolate(d3.interpolateRgb);

  const cellSize = 40;
  const gap = 2;
  // Generous top/left margins so the rotated category labels rise clear ABOVE the
  // grid (not into it) and long owner names fit.
  // Compact top margin: category headers are ICONS (not rotated text), so they sit
  // just above the grid with no collision.
  const margin = { top: 44, right: 140, bottom: 20, left: 156 };

  const width = margin.left + data.categories.length * (cellSize + gap) + margin.right;
  const height = margin.top + data.owners.length * (cellSize + gap) + margin.bottom;

  const svg = d3.select(container)
    .append('svg')
    .attr('width', '100%')
    .attr('viewBox', `0 0 ${width} ${height}`)
    .attr('preserveAspectRatio', 'xMidYMid meet')
    .attr('role', 'img')
    .attr('aria-label', 'Team by category readiness heatmap')
    .style('font-family', 'var(--font-sans)');

  const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`);

  const tooltip = sharedTooltip();
  tooltip.attach(container);

  // Build a map for quick cell lookup
  const cellMap = new Map<string, { score: number; n: number }>();
  for (const c of data.cells) {
    cellMap.set(`${c.owner}\0${c.category}`, { score: c.score, n: c.n });
  }

  // Grid cells
  const cells: Array<{ owner: string; category: string; x: number; y: number; score?: number; n?: number }> = [];
  for (let i = 0; i < data.owners.length; i++) {
    for (let j = 0; j < data.categories.length; j++) {
      const owner = data.owners[i];
      const category = data.categories[j];
      const key = `${owner}\0${category}`;
      const cell = cellMap.get(key);
      cells.push({
        owner,
        category,
        x: j * (cellSize + gap),
        y: i * (cellSize + gap),
        score: cell?.score,
        n: cell?.n,
      });
    }
  }

  const rects = g.selectAll('rect')
    .data(cells)
    .join('rect')
    .attr('x', (d) => d.x)
    .attr('y', (d) => d.y)
    .attr('width', cellSize)
    .attr('height', cellSize)
    .attr('rx', radius)
    .attr('fill', (d) => (d.score != null ? scoreFill(d.score) : 'var(--c-surface-inset)'))
    .attr('stroke', 'var(--c-border)')
    .attr('stroke-width', 1)
    .attr('cursor', opts.onSelectCell ? 'pointer' : 'default')
    .on('mouseenter', function (event, d) {
      if (d.score != null) {
        d3.select(this).transition().duration(150).attr('opacity', 0.8);
        const content = `${ownerKeyLabel(d.owner)} · ${d.category} · ${d.score}% · ${d.n} checks`;
        tooltip.show(content, event.offsetX + 10, event.offsetY - 10);
      }
    })
    .on('mousemove', function (event) {
      if (tooltip['el'].textContent) {
        tooltip.show(tooltip['el'].textContent, event.offsetX + 10, event.offsetY - 10);
      }
    })
    .on('mouseleave', function () {
      d3.select(this).transition().duration(150).attr('opacity', 1);
      tooltip.hide();
    })
    .on('click', (_, d) => {
      if (opts.onSelectCell) opts.onSelectCell(d.owner, d.category);
    });

  animateIn(rects, { attr: 'opacity', from: 0, to: () => 1 });

  // Column headers: category ICONS (no rotated text → no label/grid collision),
  // each with a hover <title> + aria-label carrying the full category name so it is
  // never meaning-by-shape-alone. Unknown categories fall back to the 'other' icon.
  const colHead = svg.append('g').attr('transform', `translate(${margin.left},${margin.top - 26})`);
  data.categories.forEach((cat, i) => {
    const cx = i * (cellSize + gap) + cellSize / 2;
    // Group carries the a11y name + a native <title>; a full-cell TRANSPARENT hit
    // rect makes the whole header hoverable (the icon is stroke-only, so its empty
    // interior isn't a hit target on its own), driving the same styled tooltip the
    // cells use. The icon itself is pointer-events:none so the rect owns hover.
    const cellG = colHead.append('g').attr('role', 'img').attr('aria-label', cat);
    cellG.append('title').text(cat);
    cellG.append('rect')
      .attr('x', i * (cellSize + gap))
      .attr('y', 0)
      .attr('width', cellSize)
      .attr('height', 24)
      .attr('fill', 'transparent')
      .style('pointer-events', 'all')
      .style('cursor', 'default')
      .on('mouseenter', (event) => tooltip.show(cat, event.offsetX + 10, event.offsetY - 10))
      .on('mousemove', (event) => tooltip.show(cat, event.offsetX + 10, event.offsetY - 10))
      .on('mouseleave', () => tooltip.hide());
    cellG.append('svg')
      .attr('x', cx - 10)
      .attr('y', 2)
      .attr('width', 20)
      .attr('height', 20)
      .attr('viewBox', '0 0 24 24')
      .attr('fill', 'none')
      .attr('stroke', 'var(--c-text-2)')
      .attr('stroke-width', 1.8)
      .attr('stroke-linecap', 'round')
      .attr('stroke-linejoin', 'round')
      .style('pointer-events', 'none')
      .html(categoryIconInner(cat));
  });

  // Row labels (owners)
  svg.append('g')
    .attr('transform', `translate(${margin.left - 10},${margin.top})`)
    .selectAll('text')
    .data(data.owners)
    .join('text')
    .attr('x', 0)
    .attr('y', (_, i) => i * (cellSize + gap) + cellSize / 2)
    .attr('text-anchor', 'end')
    .attr('dominant-baseline', 'middle')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-2)')
    // Rows are keyed by canonical owner identity so a team and a person of the same
    // name stay two rows, but a reader reads the name, not the encoding.
    .text((d) => {
      const kind = ownerKeyKind(d);
      return kind ? `${ownerKeyLabel(d)} (${kind})` : d;
    });

  // Sequential legend: gradient bar
  const legendX = margin.left + data.categories.length * (cellSize + gap) + 20;
  const legendY = margin.top;
  const legendWidth = 12;
  const legendHeight = 100;

  const defs = svg.append('defs');
  const lg = defs.append('linearGradient')
    .attr('id', 'heatmap-legend-grad')
    .attr('x1', '0')
    .attr('x2', '0')
    .attr('y1', '1')
    .attr('y2', '0');
  lg.append('stop').attr('offset', '0').attr('stop-color', rampLow);
  lg.append('stop').attr('offset', '1').attr('stop-color', pal.ok);

  svg.append('rect')
    .attr('x', legendX)
    .attr('y', legendY)
    .attr('width', legendWidth)
    .attr('height', legendHeight)
    .attr('fill', 'url(#heatmap-legend-grad)')
    .attr('rx', 2);

  svg.append('text')
    .attr('x', legendX + legendWidth + 6)
    .attr('y', legendY)
    .attr('dominant-baseline', 'hanging')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)')
    .text('100%');

  svg.append('text')
    .attr('x', legendX + legendWidth + 6)
    .attr('y', legendY + legendHeight)
    .attr('dominant-baseline', 'alphabetic')
    .style('font-size', 'var(--text-xs)')
    .style('font-weight', '500')
    .style('fill', 'var(--c-text-3)')
    .text('0%');
}

/** Version timeline: horizontal time series with classification legend. */
export interface VersionTimelineDatum {
  version: string;
  at: number; // epoch ms
  classification?: string;
  isCurrent?: boolean;
}

export interface VersionTimelineOptions {
  onSelect?: (version: string) => void;
}

export function renderVersionTimeline(
  container: HTMLElement,
  data: VersionTimelineDatum[],
  opts: VersionTimelineOptions = {},
): void {
  d3.select(container).selectAll('*').remove();

  if (!data.length) {
    emptyState(container, 'No version history');
    return;
  }

  const pal = resolvePalette(container);

  const margin = { top: 20, right: 20, bottom: 30, left: 20 };
  const width = container.clientWidth || 600;
  const height = 100;

  const svg = d3.select(container)
    .append('svg')
    .attr('viewBox', `0 0 ${width} ${height}`)
    .attr('preserveAspectRatio', 'xMidYMid meet')
    .attr('role', 'img')
    .attr('aria-label', 'Version timeline')
    .style('width', '100%')
    .style('height', 'auto')
    .style('font-family', 'var(--font-sans)');

  const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`);

  const innerWidth = width - margin.left - margin.right;
  const innerHeight = height - margin.top - margin.bottom;

  // Time scale
  let [min, max] = d3.extent(data, (d) => d.at) as [number, number];
  if (min === max) { const day = 86400000; min -= day; max += day; }
  const x = d3.scaleTime().domain([min, max]).range([0, innerWidth]);

  // Classification color map (status palette)
  const classColor: Record<string, string> = {
    BREAKING: pal.err,
    POTENTIAL_BREAKING: pal.warn,
    NON_BREAKING: pal.ok,
  };

  const tooltip = sharedTooltip();
  tooltip.attach(container);

  // Baseline
  const baselineY = innerHeight / 2;
  g.append('line')
    .attr('x1', 0)
    .attr('x2', innerWidth)
    .attr('y1', baselineY)
    .attr('y2', baselineY)
    .attr('stroke', 'var(--c-border)')
    .attr('stroke-width', 1);

  // Markers
  const markers = g.selectAll('circle')
    .data(data)
    .join('circle')
    .attr('cx', (d) => x(d.at))
    .attr('cy', baselineY)
    .attr('r', (d) => d.isCurrent ? 8 : 6)
    .attr('fill', (d) => classColor[d.classification || ''] || pal.neutral)
    .attr('stroke', (d) => d.isCurrent ? 'var(--c-surface)' : 'none')
    .attr('stroke-width', 2)
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('mouseenter', function (event, d) {
      d3.select(this).transition().duration(150).attr('opacity', 0.8);
      const dateStr = (!d.at || isNaN(d.at)) ? '—' : new Date(d.at).toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' });
      const classStr = d.classification ? d.classification.replace(/_/g, ' ').toLowerCase() : '—';
      const content = `${d.version} · ${dateStr} · ${classStr}`;
      tooltip.show(content, event.offsetX + 10, event.offsetY - 10);
    })
    .on('mousemove', function (event) {
      tooltip.show(tooltip['el'].textContent || '', event.offsetX + 10, event.offsetY - 10);
    })
    .on('mouseleave', function () {
      d3.select(this).transition().duration(150).attr('opacity', 1);
      tooltip.hide();
    })
    .on('click', (_, d) => {
      if (opts.onSelect) opts.onSelect(d.version);
    });

  animateIn(markers, { attr: 'opacity', from: 0, to: () => 1 });

  // Legend
  const legendData = [
    { label: 'Breaking', color: pal.err },
    { label: 'Potential breaking', color: pal.warn },
    { label: 'Non-breaking', color: pal.ok },
  ];

  const legend = svg.append('g')
    .attr('transform', `translate(${margin.left}, ${height - 10})`);

  let legendX = 0;
  const swatch = 8;
  const swatchGap = 6;
  const itemGap = 16;
  const charW = 6;

  legendData.forEach((d) => {
    const item = legend.append('g').attr('transform', `translate(${legendX}, 0)`);
    item.append('circle')
      .attr('cx', swatch / 2)
      .attr('cy', -swatch / 2)
      .attr('r', swatch / 2)
      .attr('fill', d.color);
    item.append('text')
      .attr('x', swatch + swatchGap)
      .attr('dominant-baseline', 'middle')
      .style('font-size', 'var(--text-xs)')
      .style('font-weight', '500')
      .style('fill', 'var(--c-text-3)')
      .text(d.label);
    legendX += swatch + swatchGap + d.label.length * charW + itemGap;
  });
}
