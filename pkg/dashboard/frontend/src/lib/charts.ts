/**
 * D3 chart renderers for dashboard aggregations.
 * Each render fn takes a container element, data and options, draws an SVG.
 */
import * as d3 from 'd3';

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
    const msg = d3.select(container).append('div')
      .attr('class', 'state-box');
    msg.text('No category data');
    return;
  }

  // Read theme colors from CSS
  const doneColor = getComputedStyle(container).getPropertyValue('--c-ok').trim();
  const partialColor = getComputedStyle(container).getPropertyValue('--c-warn').trim();
  const notDoneColor = getComputedStyle(container).getPropertyValue('--c-err').trim();
  const deferredColor = getComputedStyle(container).getPropertyValue('--c-text-3').trim();

  const margin = { top: 20, right: 140, bottom: 40, left: 120 };
  const width = Math.max(400, container.clientWidth || 400);
  const height = Math.max(220, data.length * 35 + margin.top + margin.bottom);

  const svg = d3.select(container)
    .append('svg')
    .attr('width', '100%')
    .attr('height', height)
    .style('font-family', 'var(--font-sans)');

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

  // Bars
  const colorMap: Record<string, string> = { done: doneColor, partial: partialColor, notDone: notDoneColor, deferred: deferredColor };

  g.selectAll('g.layer')
    .data(series)
    .join('g')
    .attr('class', 'layer')
    .attr('fill', (d) => colorMap[d.key])
    .selectAll('rect')
    .data((d) => d)
    .join('rect')
    .attr('x', (d) => x(d[0]))
    .attr('y', (d) => y((d.data as CategoryBarData).category) || 0)
    .attr('width', (d) => Math.max(0, x(d[1]) - x(d[0])))
    .attr('height', y.bandwidth())
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('click', (_, d) => {
      if (opts.onSelect) opts.onSelect((d.data as CategoryBarData).category);
    });

  // Y axis: category labels
  g.append('g')
    .call(d3.axisLeft(y).tickSize(0))
    .selectAll('text')
    .style('font-size', 'var(--text-sm)')
    .style('fill', 'var(--c-text)')
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('click', (_, cat) => {
      if (opts.onSelect) opts.onSelect(String(cat));
    });

  // X axis: count
  g.append('g')
    .attr('transform', `translate(0,${innerHeight})`)
    .call(d3.axisBottom(x).ticks(5).tickFormat(d3.format('d')))
    .selectAll('text')
    .style('font-size', 'var(--text-xs)')
    .style('fill', 'var(--c-text-3)');

  // Legend
  const legend = svg.append('g')
    .attr('transform', `translate(${width - margin.right + 20}, ${margin.top})`);

  const legendData = [
    { label: 'Done', color: doneColor },
    { label: 'Partial', color: partialColor },
    { label: 'Not done', color: notDoneColor },
    { label: 'Deferred', color: deferredColor },
  ];

  const legendItems = legend.selectAll('g')
    .data(legendData)
    .join('g')
    .attr('transform', (_, i) => `translate(0, ${i * 20})`);

  legendItems.append('rect')
    .attr('width', 12)
    .attr('height', 12)
    .attr('fill', (d) => d.color);

  legendItems.append('text')
    .attr('x', 18)
    .attr('y', 6)
    .attr('dominant-baseline', 'middle')
    .style('font-size', 'var(--text-xs)')
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
    const msg = d3.select(container).append('div')
      .attr('class', 'state-box');
    msg.text('No readiness data');
    return;
  }

  // Read theme colors
  const readyColor = getComputedStyle(container).getPropertyValue('--c-ok').trim();
  const partialColor = getComputedStyle(container).getPropertyValue('--c-warn').trim();
  const notReadyColor = getComputedStyle(container).getPropertyValue('--c-err').trim();
  const notConfiguredColor = getComputedStyle(container).getPropertyValue('--c-text-3').trim();

  const donutRadius = 70;
  const legendWidth = 120;
  const width = donutRadius * 2 + 40 + legendWidth;
  const height = 220;

  const svg = d3.select(container)
    .append('svg')
    .attr('width', '100%')
    .attr('viewBox', `0 0 ${width} ${height}`)
    .attr('preserveAspectRatio', 'xMidYMid meet')
    .style('font-family', 'var(--font-sans)');

  const g = svg.append('g').attr('transform', `translate(${donutRadius + 20}, ${height / 2})`);

  // Pie layout
  const pieData = [
    { label: 'Ready', value: data.ready, bucket: 'ready', color: readyColor },
    { label: 'Partial', value: data.partial, bucket: 'partial', color: partialColor },
    { label: 'Not Ready', value: data.notReady, bucket: 'not-ready', color: notReadyColor },
    { label: 'Not configured', value: data.notConfigured, bucket: 'unknown', color: notConfiguredColor },
  ].filter((d) => d.value > 0);

  const pie = d3.pie<{ label: string; value: number; bucket: string; color: string }>()
    .value((d) => d.value)
    .sort(null);

  const arc = d3.arc<d3.PieArcDatum<{ label: string; value: number; bucket: string; color: string }>>()
    .innerRadius(donutRadius * 0.6)
    .outerRadius(donutRadius);

  const arcs = g.selectAll('path')
    .data(pie(pieData))
    .join('path')
    .attr('d', arc)
    .attr('fill', (d) => d.data.color)
    .attr('stroke', 'var(--c-surface)')
    .attr('stroke-width', 2)
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('mouseenter', function () {
      d3.select(this).transition().duration(150).attr('opacity', 0.8);
    })
    .on('mouseleave', function () {
      d3.select(this).transition().duration(150).attr('opacity', 1);
    })
    .on('click', (_, d) => {
      if (opts.onSelect) opts.onSelect(d.data.bucket);
    });

  // Center label: total
  g.append('text')
    .attr('text-anchor', 'middle')
    .attr('dominant-baseline', 'middle')
    .attr('y', -8)
    .style('font-size', '2rem')
    .style('font-weight', '600')
    .style('fill', 'var(--c-text)')
    .text(total);

  g.append('text')
    .attr('text-anchor', 'middle')
    .attr('dominant-baseline', 'middle')
    .attr('y', 12)
    .style('font-size', 'var(--text-xs)')
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
    .style('fill', 'var(--c-text)')
    .text((d) => `${d.label} (${d.value})`);
}

/** Horizontal stacked bar chart for owner service-status composition. */
export interface OwnerBarData {
  key: string;
  services: number;
  compliant: number;
  warning: number;
  nonCompliant: number;
  reference: number;
  unknown: number;
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
    const msg = d3.select(container).append('div')
      .attr('class', 'state-box');
    msg.text('No owner data');
    return;
  }

  // Limit to top 15 owners for readability
  const topData = data.slice(0, 15);

  // Read theme colors from CSS
  const okColor = getComputedStyle(container).getPropertyValue('--c-ok').trim();
  const warnColor = getComputedStyle(container).getPropertyValue('--c-warn').trim();
  const errColor = getComputedStyle(container).getPropertyValue('--c-err').trim();
  const neutralColor = getComputedStyle(container).getPropertyValue('--c-text-3').trim();

  const margin = { top: 20, right: 80, bottom: 40, left: 120 };
  const width = Math.max(600, container.clientWidth || 600);
  const height = Math.max(200, topData.length * 40 + margin.top + margin.bottom);

  const svg = d3.select(container)
    .append('svg')
    .attr('width', '100%')
    .attr('height', height)
    .style('font-family', 'var(--font-sans)');

  const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`);

  const innerWidth = width - margin.left - margin.right;
  const innerHeight = height - margin.top - margin.bottom;

  // Stack keys: compliant, warning, nonCompliant, reference+unknown (neutral)
  const keys = ['compliant', 'warning', 'nonCompliant', 'neutral'];

  // Transform data: merge reference+unknown into neutral
  const stackData = topData.map((d) => ({
    key: d.key,
    services: d.services,
    compliant: d.compliant,
    warning: d.warning,
    nonCompliant: d.nonCompliant,
    neutral: d.reference + d.unknown,
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

  // Color map
  const colorMap: Record<string, string> = {
    compliant: okColor,
    warning: warnColor,
    nonCompliant: errColor,
    neutral: neutralColor,
  };

  // Bars
  g.selectAll('g.layer')
    .data(series)
    .join('g')
    .attr('class', 'layer')
    .attr('fill', (d) => colorMap[d.key])
    .selectAll('rect')
    .data((d) => d)
    .join('rect')
    .attr('x', (d) => x(d[0]))
    .attr('y', (d) => y((d.data as typeof stackData[0]).key) || 0)
    .attr('width', (d) => Math.max(0, x(d[1]) - x(d[0])))
    .attr('height', y.bandwidth())
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('click', (_, d) => {
      if (opts.onSelect) opts.onSelect((d.data as typeof stackData[0]).key);
    });

  // Y axis: owner labels
  g.append('g')
    .call(d3.axisLeft(y).tickSize(0))
    .selectAll('text')
    .style('font-size', 'var(--text-sm)')
    .style('fill', 'var(--c-text)')
    .attr('cursor', opts.onSelect ? 'pointer' : 'default')
    .on('click', (_, key) => {
      if (opts.onSelect) opts.onSelect(String(key));
    });

  // X axis: service count (integer ticks)
  g.append('g')
    .attr('transform', `translate(0,${innerHeight})`)
    .call(d3.axisBottom(x).ticks(5).tickFormat(d3.format('d')))
    .selectAll('text')
    .style('font-size', 'var(--text-xs)')
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
    .style('font-weight', '600')
    .style('fill', 'var(--c-text)')
    .text((d) => d.services);

  // Legend
  const legend = svg.append('g')
    .attr('transform', `translate(${width - margin.right + 20}, ${margin.top})`);

  const legendData = [
    { label: 'Compliant', color: okColor },
    { label: 'Warning', color: warnColor },
    { label: 'Non-Compliant', color: errColor },
    { label: 'Reference/Unknown', color: neutralColor },
  ];

  const legendItems = legend.selectAll('g')
    .data(legendData)
    .join('g')
    .attr('transform', (_, i) => `translate(0, ${i * 20})`);

  legendItems.append('rect')
    .attr('width', 12)
    .attr('height', 12)
    .attr('fill', (d) => d.color);

  legendItems.append('text')
    .attr('x', 18)
    .attr('y', 6)
    .attr('dominant-baseline', 'middle')
    .style('font-size', 'var(--text-xs)')
    .style('fill', 'var(--c-text)')
    .text((d) => d.label);
}
