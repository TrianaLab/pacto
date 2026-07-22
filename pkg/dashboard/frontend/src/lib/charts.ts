/**
 * D3 chart renderers for dashboard aggregations.
 * Each render fn takes a container element, data and options, draws an SVG.
 */
import * as d3 from 'd3';
import { readinessBucketLabel } from './format';
import { resolvePalette, sharedTooltip, defineGradients, animateIn, emptyState } from './chartkit';

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
    .attr('rx', 'var(--chart-radius)')
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
    .style('font-family', 'var(--font-sans)');

  defineGradients(svg, pal);

  const g = svg.append('g').attr('transform', `translate(${donutRadius + 20}, ${height / 2})`);

  // Pie layout
  const pieData = [
    { label: readinessBucketLabel('ready'), value: data.ready, bucket: 'ready', gradient: 'url(#grad-ok)' },
    { label: readinessBucketLabel('partial'), value: data.partial, bucket: 'partial', gradient: 'url(#grad-warn)' },
    { label: readinessBucketLabel('not-ready'), value: data.notReady, bucket: 'not-ready', gradient: 'url(#grad-err)' },
    { label: readinessBucketLabel('unknown'), value: data.notConfigured, bucket: 'unknown', gradient: 'url(#grad-neutral)' },
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
    .attr('fill', (d) => d.gradient);

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
    .attr('rx', 'var(--chart-radius)')
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
