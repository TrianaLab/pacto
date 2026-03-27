/* ── State ─── */
var state = { view: 'list', service: null, tab: 'overview', services: [], details: {}, versions: {}, aggregated: {}, sourcesInfo: [], discovering: false, filter: 'all', graphData: null, overviewView: 'table', pendingRef: null, pendingCompat: null };

// getSources extracts the sources array from a service entry.
function getSources(svc) { return (svc.sources || [svc.source]).filter(Boolean); }

/* ── Version badge ─── */
fetch('/health').then(function(r) { return r.json(); }).then(function(d) {
  if (d.version) {
    var el = document.querySelector('.topbar-logo');
    if (el) { var badge = document.createElement('span'); badge.className = 'version-badge'; badge.textContent = d.version; el.appendChild(badge); }
  }
}).catch(function() {});

/* ── API ─── */
var api = {
  get: function(p) { return fetch('/api' + p).then(function(r) { if (!r.ok) { var err = new Error('API ' + r.status); err.status = r.status; throw err; } return r.json(); }); },
  listServices: function() { return this.get('/services'); },
  getService: function(n) { return this.get('/services/' + encodeURIComponent(n)); },
  getVersions: function(n) { return this.get('/services/' + encodeURIComponent(n) + '/versions'); },
  getServiceSources: function(n) { return this.get('/services/' + encodeURIComponent(n) + '/sources'); },
  getDependents: function(n) { return this.get('/services/' + encodeURIComponent(n) + '/dependents'); },
  getGraph: function() { return this.get('/graph'); },
  getServiceGraph: function(n) { return this.get('/services/' + encodeURIComponent(n) + '/graph'); },
  getSources: function() { return this.get('/sources'); },
  getCrossRefs: function(n) { return this.get('/services/' + encodeURIComponent(n) + '/refs'); },
  getDiff: function(a, b) { return this.get('/diff?from_name=' + encodeURIComponent(a.name) + '&from_version=' + encodeURIComponent(a.version) + '&to_name=' + encodeURIComponent(b.name) + '&to_version=' + encodeURIComponent(b.version)); },
  getDebugSources: function() { return this.get('/debug/sources'); },
  resolveRef: function(ref, compatibility) {
    var payload = { ref: ref };
    if (compatibility) payload.compatibility = compatibility;
    return fetch('/api/resolve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    }).then(function(r) {
      if (!r.ok) return r.json().then(function(body) {
        var msg = (body && body.detail) || (body && body.title) || ('API ' + r.status);
        var err = new Error(msg);
        err.status = r.status;
        throw err;
      });
      return r.json();
    });
  },
  listRemoteVersions: function(ref, fetchAll) {
    return fetch('/api/versions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ref: ref, fetch: !!fetchAll })
    }).then(function(r) {
      if (!r.ok) return r.json().then(function(body) {
        var msg = (body && body.detail) || (body && body.title) || ('API ' + r.status);
        var err = new Error(msg);
        err.status = r.status;
        throw err;
      });
      return r.json();
    });
  }
};

/* ── DOM Morph ─── */
// Lightweight DOM patching: updates target to match newHTML without destroying
// scroll position, focus, input values, or other interactive state.
// Skips D3-managed containers (graph-container) and preserves form elements.
var MORPH_SKIP_IDS = { 'graph-container': true, 'graph-connections': true, 'diff-result': true, 'debug-panel-slot': true };

function patchDOM(target, newHTML) {
  var tmp = document.createElement(target.tagName || 'div');
  tmp.innerHTML = newHTML;
  morphChildren(target, tmp);
}

function morphChildren(live, next) {
  var lc = Array.from(live.childNodes);
  var nc = Array.from(next.childNodes);

  // Patch shared indices
  var min = Math.min(lc.length, nc.length);
  for (var i = 0; i < min; i++) {
    morphNode(live, lc[i], nc[i]);
  }
  // Remove extra live nodes (iterate backwards to keep indices stable)
  for (var i = lc.length - 1; i >= min; i--) {
    live.removeChild(lc[i]);
  }
  // Append new nodes
  for (var i = min; i < nc.length; i++) {
    live.appendChild(nc[i].cloneNode(true));
  }
}

function morphNode(parent, ln, nn) {
  // Different node types or tag names — replace wholesale
  if (ln.nodeType !== nn.nodeType || ln.nodeName !== nn.nodeName) {
    parent.replaceChild(nn.cloneNode(true), ln);
    return;
  }
  // Text / comment nodes
  if (ln.nodeType === 3 || ln.nodeType === 8) {
    if (ln.textContent !== nn.textContent) ln.textContent = nn.textContent;
    return;
  }
  if (ln.nodeType !== 1) return;

  // Skip D3-managed or other interactive containers
  var id = ln.id;
  if (id && MORPH_SKIP_IDS[id]) {
    morphAttrs(ln, nn);
    return;
  }

  // SVG — replace if different (SVG DOM is complex)
  if (ln.namespaceURI === 'http://www.w3.org/2000/svg') {
    if (ln.outerHTML !== nn.outerHTML) parent.replaceChild(nn.cloneNode(true), ln);
    return;
  }

  // Preserve form element values
  var tag = ln.tagName;
  if (tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA') {
    morphAttrs(ln, nn);
    return;
  }

  morphAttrs(ln, nn);
  morphChildren(ln, nn);
}

function morphAttrs(live, next) {
  var la = live.attributes, na = next.attributes;
  // Remove attrs not in next
  for (var i = la.length - 1; i >= 0; i--) {
    if (!next.hasAttribute(la[i].name)) live.removeAttribute(la[i].name);
  }
  // Set/update attrs from next
  for (var i = 0; i < na.length; i++) {
    if (live.getAttribute(na[i].name) !== na[i].value) live.setAttribute(na[i].name, na[i].value);
  }
}

/* ── Helpers ─── */
function h(s) { if (s == null) return ''; var d = document.createElement('div'); d.textContent = String(s); return d.innerHTML; }
function ha(s) { return h(s).replace(/'/g, '&#39;').replace(/"/g, '&quot;'); }
function pct(n, t) { return t > 0 ? Math.round(n / t * 100) : 0; }
function classificationBadge(c) {
  if (!c) return '<span class="text-dim">\u2014</span>';
  if (c === 'NON_BREAKING') return '<span class="badge badge-ok">non-breaking</span>';
  if (c === 'POTENTIAL_BREAKING') return '<span class="badge badge-warning">potential breaking</span>';
  if (c === 'BREAKING') return '<span class="badge badge-critical">breaking</span>';
  return '<span class="badge badge-neutral">' + h(c) + '</span>';
}

function probeInlineBadge(endpoints, probeType, iface) {
  if (!endpoints || !endpoints.length) return '';
  var ep = endpoints.find(function(e) {
    if (e.type === probeType) return true;
    if (!e.type && e.interface === iface) return true;
    return false;
  });
  if (!ep) return '';
  var badge = ep.healthy === true ? '<span class="badge badge-ok" style="margin-left:8px">reachable</span>'
    : ep.healthy === false ? '<span class="badge badge-critical" style="margin-left:8px">failing</span>'
    : '<span class="badge badge-neutral" style="margin-left:8px">unknown</span>';
  if (ep.latencyMs != null) badge += ' <span class="text-dim" style="font-size:var(--text-xs)">' + ep.latencyMs + 'ms</span>';
  return badge;
}

function refLink(ref) {
  if (!ref) return '\u2014';
  var name = extractServiceName(ref);
  // Use resolved name from cross-refs if available (handles repo name != service name).
  if (state.crossRefs && state.crossRefs.references) {
    var match = state.crossRefs.references.find(function(r) { return r.ref === ref; });
    if (match && match.name) name = match.name;
  }
  return '<a class="dep-link" onclick="navigateTo(\'detail\',\'' + ha(name) + '\')">' + h(name) + '</a> <code class="text-dim" style="font-size:var(--text-xs)">' + h(ref) + '</code>';
}

function hasValidationPath(d, prefix) {
  if (!d || !d.validation) return false;
  var issues = (d.validation.errors || []).concat(d.validation.warnings || []);
  return issues.some(function(e) { return e.path && e.path.indexOf(prefix) !== -1; });
}

function phaseBadge(phase) {
  var p = phase || 'Unknown';
  var cls = { Healthy: 'badge-ok', Degraded: 'badge-warning', Invalid: 'badge-critical', Unknown: 'badge-neutral', Reference: 'badge-neutral' }[p] || 'badge-neutral';
  return '<span class="badge ' + cls + '"><span class="badge-dot"></span>' + h(p) + '</span>';
}

function complianceBadge(status) {
  if (!status) return '';
  var cls = { OK: 'badge-ok', WARNING: 'badge-warning', ERROR: 'badge-critical', REFERENCE: 'badge-neutral' }[status] || 'badge-neutral';
  return '<span class="badge ' + cls + '">' + h(status) + '</span>';
}

function complianceScoreBadge(score, status) {
  if (score == null) return '';
  var cls = 'compliance-score';
  if (status === 'ERROR' || score < 50) cls += ' compliance-score-error';
  else if (status === 'WARNING' || score < 80) cls += ' compliance-score-warning';
  else cls += ' compliance-score-ok';
  return '<span class="' + cls + '">' + score + '%</span>';
}

function sourceLabel(type) {
  return type.toUpperCase();
}

function sourcePill(type) {
  var tip = sourceTooltips[type] || type;
  return '<span class="source-pill source-pill-' + h(type) + '" title="' + ha(tip) + '"><span class="pill-dot"></span>' + sourceLabel(type) + '</span>';
}

function insightClass(severity) {
  return { critical: 'insight-critical', warning: 'insight-warning', info: 'insight-info' }[severity] || 'insight-info';
}

function insightIcon(severity) {
  if (severity === 'critical') return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>';
  if (severity === 'warning') return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>';
  return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>';
}

function condBadge(status) {
  if (status === 'True') return '<span class="badge badge-ok">True</span>';
  if (status === 'False') return '<span class="badge badge-critical">False</span>';
  return '<span class="badge badge-neutral">' + h(status) + '</span>';
}

/* Column help tooltip helper */
function colHelp(text) {
  return ' <span class="col-help"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg><span class="col-help-text">' + h(text) + '</span></span>';
}

/* Extract short service name from OCI ref like oci://ghcr.io/org/svc:1.0 → svc */
function extractServiceName(ref) {
  if (!ref) return '';
  ref = ref.replace(/^oci:\/\//, '');
  var parts = ref.split('/');
  var name = parts[parts.length - 1];
  var idx = name.indexOf(':');
  if (idx > 0) name = name.substring(0, idx);
  return name;
}

/* Check if a service exists in our loaded data */
function serviceExists(name) {
  return state.services.some(function(s) { return s.name === name; });
}

/* ── Navigation ─── */
function resolveServiceName(name) {
  if (!name) return name;
  if (serviceExists(name)) return name;
  // Convention: OCI repos use a -pacto suffix that the contract service.name omits.
  var stripped = name.replace(/-pacto$/, '');
  if (stripped !== name && serviceExists(stripped)) return stripped;
  return name;
}
// Render generation: incremented on every navigation. Async callbacks compare
// their captured generation against the current one to detect stale renders.
var _renderGen = 0;

function navigateTo(view, svc, ref, compat, _fromPopState) {
  state.view = view;
  state.service = resolveServiceName(svc) || null;
  state.pendingRef = ref || null;
  state.pendingCompat = compat || null;
  if (view === 'list') { state.tab = 'overview'; state.filter = 'all'; }
  else { state.tab = 'overview'; }
  // Clear search when navigating to detail
  var si = document.getElementById('search-input');
  if (si && view !== 'list') si.value = '';
  closeSearchDropdown();
  // Update URL for bookmarkability and browser history.
  var wantHash = view === 'list' ? '#' : '#service/' + encodeURIComponent(svc);
  var currentHash = location.hash || '#';
  if (currentHash !== wantHash) {
    if (_fromPopState) {
      // popstate already changed the URL; don't push a new entry
      history.replaceState(null, '', wantHash);
    } else {
      history.pushState(null, '', wantHash);
    }
  }
  _renderGen++;
  render();
}

/* ── Auto-reload ─── */
var autoReloadEnabled = localStorage.getItem('pacto-auto-reload') === 'true';
var autoReloadTimer = null;
function updateAutoReloadUI() {
  var btn = document.getElementById('auto-reload-btn');
  if (btn) btn.classList.toggle('active', autoReloadEnabled);
  var tip = document.getElementById('auto-reload-tooltip');
  if (tip) tip.textContent = autoReloadEnabled ? 'Auto-reload every 10s (on)' : 'Auto-reload every 10s (off)';
}
function toggleAutoReload() {
  autoReloadEnabled = !autoReloadEnabled;
  localStorage.setItem('pacto-auto-reload', String(autoReloadEnabled));
  updateAutoReloadUI();
  if (autoReloadEnabled) startAutoReload(); else stopAutoReload();
}
function startAutoReload() { stopAutoReload(); autoReloadTimer = setInterval(doRefresh, 10000); }
function stopAutoReload() { if (autoReloadTimer) { clearInterval(autoReloadTimer); autoReloadTimer = null; } }
function doRefresh() {
  // Skip refresh when tab is hidden — prevents stale renders competing
  // with navigation when the user switches back to the tab.
  if (document.hidden) return;
  var btn = document.getElementById('reload-btn');
  if (btn) { btn.classList.add('spinning'); setTimeout(function() { btn.classList.remove('spinning'); }, 600); }
  // Re-render without clearing cached state — the render functions
  // detect existing data and refresh in the background seamlessly.
  // Increment generation so any in-flight stale renders are cancelled.
  _renderGen++;
  render();
}
updateAutoReloadUI();
if (autoReloadEnabled) startAutoReload();

/* ── Render Router ─── */
function render() {
  if (state.view === 'list') renderOverview();
  else renderDetail();
}

/* ════════════════════════════════════════════════════════════════
   OVERVIEW PAGE — matches operator overview.html with D3 graph
   ════════════════════════════════════════════════════════════════ */
var graphInitialized = false;
var graphDataFingerprint = null;

// Structural fingerprint: only node IDs and edges — determines if graph needs full re-init.
function graphStructureFingerprint(data) {
  if (!data || !data.nodes) return '';
  return data.nodes.map(function(n) {
    return n.id + ':' + (n.edges || []).map(function(e) { return e.targetId; }).join(',');
  }).join('|');
}

// Update node visual styles (status color, text) in-place without re-initializing the graph.
function updateGraphNodeStyles(data) {
  if (!graphSvg || !data || !data.nodes) return;
  var statusMap = {};
  data.nodes.forEach(function(n) { statusMap[n.id] = n.status || 'Unknown'; });
  graphSvg.selectAll('.node-group').each(function(d) {
    var newStatus = statusMap[d.id];
    if (!newStatus || newStatus === d.status) return;
    d.status = newStatus;
    d3.select(this).select('rect').attr('stroke', statusColor(newStatus));
    d3.select(this).select('.node-status')
      .attr('fill', statusColor(newStatus))
      .text(newStatus === 'Unmonitored' ? 'Unmonitored' : newStatus === 'External' ? 'External' : newStatus);
  });
}

var overviewLoaded = false;
async function renderOverview() {
  var gen = _renderGen;
  var app = document.getElementById('app');
  // If we already have data, render immediately from cache, then refresh in background
  if (overviewLoaded && state.services.length) {
    // Render from cache immediately (full replace if page type changed, e.g. detail→overview)
    if (state.overviewView !== 'graph' || !graphInitialized) renderOverviewPage();
    Promise.all([
      api.listServices(),
      api.getSources().catch(function() { return { sources: [], discovering: false }; }),
      api.getGraph().catch(function() { return null; })
    ]).then(function(r) {
      if (_renderGen !== gen) return; // stale render
      state.services = r[0] || [];
      applySourcesResponse(r[1]);
      var newFp = graphStructureFingerprint(r[2]);
      if (newFp !== graphDataFingerprint) {
        // Structure changed (nodes added/removed, edges changed) — full re-init needed
        state.graphData = r[2];
        graphDataFingerprint = newFp;
        graphInitialized = false;
      } else if (r[2]) {
        // Structure unchanged but status may have changed — update in-place
        state.graphData = r[2];
        updateGraphNodeStyles(r[2]);
      }
      renderSourcePills();
      if (state.overviewView !== 'graph' || !graphInitialized) {
        renderOverviewPage();
      }
      scheduleDiscoveryRefresh();
    }).catch(function() { /* keep stale data */ });
    return;
  }
  // First load — show spinner
  _currentPage = null;
  app.innerHTML = '<div class="loading"><div class="spinner"></div>Loading...</div>';
  try {
    var r = await Promise.all([
      api.listServices(),
      api.getSources().catch(function() { return { sources: [], discovering: false }; }),
      api.getGraph().catch(function() { return null; })
    ]);
    if (_renderGen !== gen) return; // stale render
    state.services = r[0] || [];
    applySourcesResponse(r[1]);
    state.graphData = r[2];
    graphDataFingerprint = graphStructureFingerprint(r[2]);
    graphInitialized = false;
    overviewLoaded = true;
    renderSourcePills();
    renderOverviewPage();
    scheduleDiscoveryRefresh();
  } catch (e) {
    if (_renderGen !== gen) return;
    app.innerHTML = '<div class="empty-state"><div class="empty-state-title">Failed to load</div><p>' + h(e.message) + '</p></div>';
  }
}

// Parse the /api/sources response into state.
function applySourcesResponse(r) {
  if (Array.isArray(r)) {
    // Legacy format (plain array) — shouldn't happen but safe fallback.
    state.sourcesInfo = r;
    state.discovering = false;
  } else if (r && r.sources) {
    state.sourcesInfo = r.sources;
    state.discovering = !!r.discovering;
  } else {
    state.sourcesInfo = [];
    state.discovering = false;
  }
}

// During OCI discovery, schedule frequent auto-refreshes (every 2s) so new
// services appear in real time. Stops once discovery completes.
var discoveryTimer = null;
function scheduleDiscoveryRefresh() {
  if (state.discovering && !discoveryTimer) {
    discoveryTimer = setInterval(doRefresh, 2000);
  } else if (!state.discovering && discoveryTimer) {
    clearInterval(discoveryTimer);
    discoveryTimer = null;
  }
}

var sourceTooltips = {
  k8s: 'Kubernetes: live CRD status from the cluster operator',
  oci: 'OCI Registry: contract versions pushed to container registries',
  local: 'Local: contract from the working directory (pacto.yaml)'
};

function renderSourcePills() {
  document.getElementById('source-pills').innerHTML = state.sourcesInfo.filter(function(s) { return s.enabled; }).map(function(s) {
    var active = isSourceEnabled(s.type);
    var tip = sourceTooltips[s.type] || s.type;
    return '<span class="topbar-btn-wrap">' +
      '<span class="source-pill source-pill-' + h(s.type) + '" style="cursor:pointer;opacity:' + (active ? '1' : '0.35') + '" onclick="toggleSourceFilterGlobal(\'' + ha(s.type) + '\')">' +
      '<span class="pill-dot"></span>' + sourceLabel(s.type) + '</span>' +
      '<span class="topbar-tooltip">' + h(tip) + '</span></span>';
  }).join('');
}

function renderDebugPanel(debug) {
  if (!debug) return '';
  var o = '<details class="debug-panel"><summary class="debug-summary">Source Diagnostics</summary>';
  o += '<div class="debug-content">';

  if (debug.diagnostics) {
    var d = debug.diagnostics;
    o += '<div class="debug-section"><h4>Kubernetes</h4><table class="debug-table">';
    o += '<tr><td>Client configured</td><td>' + (d.k8s && d.k8s.clientConfigured ? 'Yes' : 'No') + '</td></tr>';
    if (d.k8s && d.k8s.kubeconfigPath) o += '<tr><td>kubeconfig</td><td>' + h(d.k8s.kubeconfigPath) + '</td></tr>';
    o += '<tr><td>Cluster reachable</td><td>' + (d.k8s && d.k8s.clusterReachable ? 'Yes' : 'No') + '</td></tr>';
    o += '<tr><td>CRD exists</td><td>' + (d.k8s && d.k8s.crdExists ? 'Yes' : 'No') + '</td></tr>';
    if (d.k8s) o += '<tr><td>Namespace</td><td>' + (d.k8s.allNamespaces ? 'all namespaces' : h(d.k8s.namespace || 'default')) + '</td></tr>';
    if (d.k8s) o += '<tr><td>Resources found</td><td>' + (d.k8s.resourceCount || 0) + '</td></tr>';
    if (d.k8s && d.k8s.error) o += '<tr><td>Error</td><td class="text-critical">' + h(d.k8s.error) + '</td></tr>';
    o += '</table></div>';

    if (d.cache) {
      o += '<div class="debug-section"><h4>OCI Cache</h4><table class="debug-table">';
      o += '<tr><td>Cache dir</td><td>' + h(d.cache.cacheDir) + '</td></tr>';
      o += '<tr><td>Exists</td><td>' + (d.cache.exists ? 'Yes' : 'No') + '</td></tr>';
      o += '<tr><td>Services</td><td>' + (d.cache.serviceCount || 0) + '</td></tr>';
      o += '<tr><td>Versions</td><td>' + (d.cache.versionCount || 0) + '</td></tr>';
      if (d.cache.error) o += '<tr><td>Error</td><td class="text-critical">' + h(d.cache.error) + '</td></tr>';
      o += '</table></div>';
    }

    if (d.oci) {
      o += '<div class="debug-section"><h4>OCI Registry</h4><table class="debug-table">';
      o += '<tr><td>Store configured</td><td>' + (d.oci.storeConfigured ? 'Yes' : 'No') + '</td></tr>';
      if (d.oci.repos && d.oci.repos.length) o += '<tr><td>Repos</td><td>' + d.oci.repos.map(h).join(', ') + '</td></tr>';
      if (d.oci.error) o += '<tr><td>Error</td><td class="text-critical">' + h(d.oci.error) + '</td></tr>';
      o += '</table></div>';
    }

    if (d.local) {
      o += '<div class="debug-section"><h4>Local</h4><table class="debug-table">';
      o += '<tr><td>Directory</td><td>' + h(d.local.dir) + '</td></tr>';
      o += '<tr><td>pacto.yaml found</td><td>' + (d.local.pactoYamlFound ? 'Yes' : 'No') + '</td></tr>';
      if (d.local.foundIn) o += '<tr><td>Found in</td><td>' + h(d.local.foundIn) + '</td></tr>';
      if (d.local.error) o += '<tr><td>Error</td><td class="text-critical">' + h(d.local.error) + '</td></tr>';
      o += '</table></div>';
    }
  }

  if (debug.live) {
    o += '<div class="debug-section"><h4>Live API</h4><table class="debug-table">';
    o += '<tr><td>Service count</td><td>' + debug.live.serviceCount + '</td></tr>';
    if (debug.live.error) o += '<tr><td>Error</td><td class="text-critical">' + h(debug.live.error) + '</td></tr>';
    o += '</table></div>';
  }

  o += '</div></details>';
  return o;
}

var _currentPage = null; // tracks which page type is currently rendered

function updateApp(html, page) {
  var app = document.getElementById('app');
  if (_currentPage === page && app.children.length > 0) {
    patchDOM(app, html);
  } else {
    app.innerHTML = html;
  }
  _currentPage = page;
}

function renderOverviewPage() {
  var svcs = state.services;
  if (!svcs.length) {
    var emptyHTML = '<h1 class="page-title">Overview</h1><p class="page-subtitle">0 contracts</p>';
    emptyHTML += '<div class="empty-state"><div class="empty-state-title">No Pacto resources found</div><p>No service contracts detected from any source.</p></div>';
    api.getDebugSources().then(function(debug) {
      document.getElementById('app').innerHTML = emptyHTML + renderDebugPanel(debug);
    }).catch(function() {
      document.getElementById('app').innerHTML = emptyHTML;
    });
    return;
  }

  var healthy = 0, degraded = 0, invalid = 0, unknown = 0;
  var atRisk = [];
  for (var i = 0; i < svcs.length; i++) {
    var p = svcs[i].phase;
    if (p === 'Healthy') healthy++;
    else if (p === 'Degraded') { degraded++; atRisk.push(svcs[i]); }
    else if (p === 'Invalid') { invalid++; atRisk.push(svcs[i]); }
    else unknown++;
  }
  var total = svcs.length;
  var monitored = healthy + degraded + invalid;

  var o = '<h1 class="page-title">Overview</h1>';
  o += '<p class="page-subtitle">' + total + ' contract' + (total !== 1 ? 's' : '') + ' \u2014 ' + monitored + ' active' + (unknown > 0 ? ', ' + unknown + ' unmonitored' : '') + '</p>';

  // Status bar
  if (monitored > 0 || unknown > 0) {
    o += '<div class="status-bar">';
    o += '<div class="status-bar-ok" style="width:' + pct(healthy, total) + '%"></div>';
    o += '<div class="status-bar-warning" style="width:' + pct(degraded, total) + '%"></div>';
    o += '<div class="status-bar-critical" style="width:' + pct(invalid, total) + '%"></div>';
    o += '<div class="status-bar-neutral" style="width:' + pct(unknown, total) + '%"></div>';
    o += '</div>';
  }

  // Stats bar (with help tooltips matching v8)
  o += '<div class="stats-bar">';
  o += statCard('all', total, 'All', 'stat-neutral', 'Total contracts registered');
  o += statCard('Healthy', healthy, 'Healthy', 'stat-ok', 'All validation checks pass \u2014 contract, service, workload, and ports match');
  o += statCard('Degraded', degraded, 'Degraded', 'stat-warning', 'Some checks fail (e.g. port mismatch) but service and workload exist');
  o += statCard('Invalid', invalid, 'Invalid', 'stat-critical', 'Critical failures \u2014 contract invalid, service missing, or workload not found');
  if (unknown > 0) o += statCard('Unknown', unknown, 'Unmonitored', 'stat-neutral', 'Contracts without a target workload \u2014 published as shared definitions or dependency references');
  o += '</div>';

  // View tabs: Table | Graph
  o += '<div class="view-tabs">';
  o += '<button class="view-tab' + (state.overviewView === 'table' ? ' active' : '') + '" data-view="table" onclick="switchOverviewView(\'table\')">Table</button>';
  o += '<button class="view-tab' + (state.overviewView === 'graph' ? ' active' : '') + '" data-view="graph" onclick="switchOverviewView(\'graph\')">Graph</button>';
  o += '</div>';

  // ─── TABLE VIEW ───
  o += '<div id="view-table"' + (state.overviewView !== 'table' ? ' style="display:none"' : '') + '>';

  // Filter indicator
  o += '<div id="filter-indicator" style="' + (state.filter === 'all' ? 'display:none;' : '') + 'margin-bottom:16px">';
  o += '<span class="pill pill-accent" style="font-size:12px">Showing: <span id="filter-label">' + h(state.filter) + '</span></span> ';
  o += '<button class="filter-clear" onclick="clearAllFilters()">clear</button>';
  o += '</div>';

  // Needs attention — sorted by blast radius descending, then phase severity
  if (atRisk.length > 0) {
    atRisk.sort(function(a, b) {
      var br = (b.blastRadius || 0) - (a.blastRadius || 0);
      if (br !== 0) return br;
      var sev = { Invalid: 0, Degraded: 1 };
      return (sev[a.phase] || 2) - (sev[b.phase] || 2);
    });
    o += '<div id="at-risk-section" style="margin-bottom:32px"><div class="section-heading">Needs Attention <span class="text-dim" style="font-weight:400;font-size:var(--text-sm)">' + atRisk.length + ' service' + (atRisk.length > 1 ? 's' : '') + '</span></div>';
    o += '<div class="at-risk-grid">';
    for (var i = 0; i < atRisk.length; i++) {
      var s = atRisk[i];
      var cls = s.phase === 'Invalid' ? 'alert-card-critical' : 'alert-card-warning';
      var sources = getSources(s);
      o += '<div class="alert-card ' + cls + '" data-sources="' + ha(sources.join(',')) + '" onclick="navigateTo(\'detail\',\'' + ha(s.name) + '\')">';
      o += '<div class="alert-card-body"><div class="alert-card-title">' + h(s.name) + '</div>';
      o += '<div class="alert-card-desc">' + h(s.topInsight || s.phase) + '</div></div>';
      o += '<div class="alert-card-meta">' + phaseBadge(s.phase);
      if (s.checksFailed > 0) o += ' <span class="pill pill-critical">' + s.checksFailed + ' failed</span>';
      if (s.blastRadius > 0) o += ' <span class="pill pill-accent">\u26A1 ' + s.blastRadius + ' affected</span>';
      o += '</div></div>';
    }
    o += '</div></div>';
  }

  // Source filter bar
  var activeSrcTypes = {};
  for (var si = 0; si < svcs.length; si++) {
    var srcs = getSources(svcs[si]);
    for (var sj = 0; sj < srcs.length; sj++) activeSrcTypes[srcs[sj]] = true;
  }
  var srcTypeList = Object.keys(activeSrcTypes).sort();
  if (srcTypeList.length > 1) {
    o += '<div class="source-filter-bar"><span class="source-filter-label">Source</span>';
    var srcColors = { k8s: 'var(--info)', oci: 'var(--accent)', local: 'var(--neutral)' };
    for (var si = 0; si < srcTypeList.length; si++) {
      var st = srcTypeList[si];
      var stTip = sourceTooltips[st] || st;
      o += '<button class="source-filter-btn active" data-source-filter="' + ha(st) + '" title="' + ha(stTip) + '" onclick="toggleSourceFilter(\'' + ha(st) + '\')">';
      o += '<span class="pill-dot" style="background:' + (srcColors[st] || 'var(--neutral)') + '"></span>' + sourceLabel(st) + '</button>';
    }
    o += '</div>';
  }

  // Service table — enriched with Checks, Impact, Insight columns (v8 parity)
  o += '<div class="section-heading">All Contracts</div>';
  o += '<div class="table-wrapper"><table><thead><tr>';
  o += '<th>Service</th>';
  o += '<th>Compliance' + colHelp('Contract compliance: OK, WARNING, ERROR, or REFERENCE') + '</th>';
  o += '<th>Checks' + colHelp('Passed / total reconciliation checks from the operator') + '</th>';
  o += '<th>Impact' + colHelp('Blast radius: how many services are affected if this one breaks') + '</th>';
  o += '<th class="hide-narrow">Version</th>';
  o += '<th class="hide-narrow">Insight' + colHelp('Top diagnostic finding from the operator') + '</th>';
  o += '<th>Sources</th>';
  o += '</tr></thead><tbody>';
  for (var i = 0; i < svcs.length; i++) {
    var s = svcs[i];
    var sources = getSources(s);
    var searchText = [s.name, s.owner || '', s.version || '', sources.join(' ')].join(' ').toLowerCase();
    o += '<tr data-click data-phase="' + ha(s.phase) + '" data-sources="' + ha(sources.join(',')) + '" data-search="' + ha(searchText) + '" onclick="navigateTo(\'detail\',\'' + ha(s.name) + '\')">';
    o += '<td><a>' + h(s.name) + '</a></td>';
    // Compliance column — shows compliance badge + score + error/warning counts
    o += '<td>';
    o += complianceBadge(s.complianceStatus || (s.phase === 'Reference' ? 'REFERENCE' : ''));
    if (s.complianceScore != null) o += ' ' + complianceScoreBadge(s.complianceScore, s.complianceStatus);
    if (s.complianceErrors > 0) o += ' <span class="pill pill-critical" style="font-size:10px">' + s.complianceErrors + 'E</span>';
    if (s.complianceWarnings > 0) o += ' <span class="pill pill-warning" style="font-size:10px">' + s.complianceWarnings + 'W</span>';
    if (!s.complianceStatus && s.phase !== 'Reference') o += phaseBadge(s.phase);
    o += '</td>';
    // Checks column — use checksTotal explicitly; 0 is valid, undefined/null means no data
    var cTotal = s.checksTotal != null ? s.checksTotal : -1;
    var cPassed = s.checksPassed != null ? s.checksPassed : 0;
    var cFailed = s.checksFailed != null ? s.checksFailed : 0;
    if (cTotal >= 0) {
      o += '<td><span class="count ' + (cFailed > 0 ? 'count-error' : 'count-zero') + '">' + cPassed + '/' + cTotal + '</span></td>';
    } else {
      o += '<td><span class="count count-zero">\u2014</span></td>';
    }
    // Impact column — v8 parity: "X affected" when broken + blast radius, "X dependents" when healthy + blast radius, "X deps" when only deps
    if (s.blastRadius > 0 && s.phase !== 'Healthy') {
      o += '<td><span class="blast-radius" style="color:var(--warning);background:var(--warning-bg)">' + s.blastRadius + ' affected</span></td>';
    } else if (s.blastRadius > 0) {
      o += '<td><span class="blast-radius">' + s.blastRadius + ' dependent' + (s.blastRadius > 1 ? 's' : '') + '</span></td>';
    } else if (s.dependencyCount > 0) {
      o += '<td><span class="text-dim">' + s.dependencyCount + ' dep' + (s.dependencyCount > 1 ? 's' : '') + '</span></td>';
    } else {
      o += '<td><span class="count count-zero">\u2014</span></td>';
    }
    o += '<td class="hide-narrow"><span class="text-dim">' + h(s.version || '\u2014') + '</span></td>';
    // Insight column
    o += '<td class="hide-narrow"><span class="text-dim">' + h(s.topInsight || '\u2014') + '</span></td>';
    o += '<td>' + sources.map(sourcePill).join(' ') + '</td>';
    o += '</tr>';
  }
  o += '</tbody></table></div>';
  o += '</div>'; // end view-table

  // ─── GRAPH VIEW ───
  o += '<div id="view-graph"' + (state.overviewView !== 'graph' ? ' style="display:none"' : '') + '>';
  o += '<div class="graph-fullscreen" id="graph-container">';
  o += '<div class="graph-controls">';
  o += '<button class="graph-btn" onclick="graphZoomIn()" title="Zoom in">+</button>';
  o += '<button class="graph-btn" onclick="graphZoomOut()" title="Zoom out">\u2212</button>';
  o += '<button class="graph-btn" onclick="graphResetView()" title="Reset view">\u21BA</button>';
  o += '</div>';
  o += '<div class="graph-legend" id="graph-legend"></div>';
  o += '</div>';

  // Service Connections table below graph
  o += '<div class="graph-connections" id="graph-connections"></div>';
  o += '</div>'; // end view-graph

  o += '<div id="debug-panel-slot"></div>';

  updateApp(o, 'overview');

  // Apply current filter
  applyFilter();

  // Initialize graph if graph view is active (only on first render or view switch)
  if (state.overviewView === 'graph' && !graphInitialized) initGraph();

  // Render connections table
  renderConnectionsTable();

  // Load debug panel in background
  api.getDebugSources().then(function(debug) {
    var panel = document.getElementById('debug-panel-slot');
    if (panel) panel.innerHTML = renderDebugPanel(debug);
  }).catch(function() {});
}

function switchOverviewView(view) {
  state.overviewView = view;
  document.getElementById('view-table').style.display = view === 'table' ? '' : 'none';
  document.getElementById('view-graph').style.display = view === 'graph' ? '' : 'none';
  document.querySelectorAll('.view-tab').forEach(function(t) {
    t.classList.toggle('active', t.getAttribute('data-view') === view);
  });
  if (view === 'graph' && !graphInitialized) initGraph();
  history.replaceState(null, '', view === 'graph' ? '#graph' : '#');
}

function statCard(filter, value, label, cls, helpText) {
  var isActive = state.filter === filter;
  return '<div class="stat ' + cls + (isActive ? ' stat-active' : '') + '" onclick="toggleFilter(\'' + filter + '\')" role="button" aria-pressed="' + isActive + '">' +
    '<div class="stat-value">' + value + '</div>' +
    '<div class="stat-label">' + label + (helpText ? colHelp(helpText) : '') + '</div></div>';
}

function clearAllFilters() {
  state.filter = 'all';
  enabledSources = {};
  var searchEl = document.getElementById('search-input');
  if (searchEl) searchEl.value = '';
  // Update stats bar active state
  document.querySelectorAll('.stats-bar .stat').forEach(function(el) {
    var onclick = el.getAttribute('onclick') || '';
    var match = onclick.match(/toggleFilter\('(\w+)'\)/);
    if (match) {
      el.classList.toggle('stat-active', match[1] === 'all');
      el.setAttribute('aria-pressed', match[1] === 'all');
    }
  });
  syncSourceUI();
  applyFilter();
  syncGraphFilters();
}

function toggleFilter(f) {
  state.filter = (state.filter === f) ? 'all' : f;
  // Update stats bar active state
  document.querySelectorAll('.stats-bar .stat').forEach(function(el) {
    var onclick = el.getAttribute('onclick') || '';
    var match = onclick.match(/toggleFilter\('(\w+)'\)/);
    if (match) {
      el.classList.toggle('stat-active', match[1] === state.filter);
      el.setAttribute('aria-pressed', match[1] === state.filter);
    }
  });
  applyFilter();
  syncGraphFilters();
}

// Source filter: enabledSources is empty when all sources are shown.
// When non-empty, only sources in the set are shown.
// This matches the status filter click semantics.
var enabledSources = {};

function isSourceEnabled(src) {
  var keys = Object.keys(enabledSources);
  return keys.length === 0 || enabledSources[src];
}

function toggleSourceClick(src) {
  var keys = Object.keys(enabledSources);
  if (keys.length === 0) {
    // All enabled → narrow to only this source
    enabledSources = {};
    enabledSources[src] = true;
  } else if (enabledSources[src]) {
    // This source is already enabled
    if (keys.length === 1) {
      // Only one enabled and clicking it → reset to all
      enabledSources = {};
    } else {
      // Multiple enabled → remove this one
      delete enabledSources[src];
    }
  } else {
    // This source is not enabled → add it
    enabledSources[src] = true;
  }
}

function toggleSourceFilter(src) {
  toggleSourceClick(src);
  syncSourceUI();
  applyFilter();
  syncGraphFilters();
}
var toggleSourceFilterGlobal = toggleSourceFilter;

function syncSourceUI() {
  renderSourcePills();
  document.querySelectorAll('.source-filter-btn[data-source-filter]').forEach(function(btn) {
    var s = btn.getAttribute('data-source-filter');
    btn.classList.toggle('active', isSourceEnabled(s));
  });
}

function getSearchTerm() {
  var el = document.getElementById('search-input');
  return el ? el.value.toLowerCase().trim() : '';
}

var searchDropdownIndex = -1;
function onSearchInput() {
  if (state.view === 'list') applyFilter();
  updateSearchDropdown();
}
function onSearchFocus() {
  updateSearchDropdown();
}
function onSearchKeydown(e) {
  var dd = document.getElementById('search-dropdown');
  var items = dd ? dd.querySelectorAll('.search-dropdown-item') : [];
  if (e.key === 'ArrowDown') { e.preventDefault(); searchDropdownIndex = Math.min(searchDropdownIndex + 1, items.length - 1); highlightDropdownItem(items); }
  else if (e.key === 'ArrowUp') { e.preventDefault(); searchDropdownIndex = Math.max(searchDropdownIndex - 1, -1); highlightDropdownItem(items); }
  else if (e.key === 'Enter' && searchDropdownIndex >= 0 && items[searchDropdownIndex]) {
    e.preventDefault();
    var name = items[searchDropdownIndex].dataset.name;
    closeSearchDropdown();
    if (name) navigateTo('detail', name);
  } else if (e.key === 'Escape') { closeSearchDropdown(); }
}
function updateSearchDropdown() {
  var dd = document.getElementById('search-dropdown');
  if (!dd) return;
  var term = getSearchTerm();
  searchDropdownIndex = -1;
  if (!term) { dd.classList.remove('open'); dd.innerHTML = ''; return; }
  var matches = (state.services || []).filter(function(s) {
    var sources = getSources(s);
    var text = [s.name, s.owner || '', s.version || '', sources.join(' ')].join(' ').toLowerCase();
    return text.indexOf(term) !== -1;
  }).slice(0, 8);
  if (matches.length === 0) {
    dd.innerHTML = '<div class="search-dropdown-empty">No services match \u201c' + h(term) + '\u201d</div>';
  } else {
    dd.innerHTML = matches.map(function(s) {
      var sources = getSources(s);
      return '<div class="search-dropdown-item" data-name="' + ha(s.name) + '" onclick="navigateTo(\'detail\',\'' + ha(s.name) + '\');closeSearchDropdown()" onmouseenter="searchDropdownIndex=-1;highlightDropdownItem([])">' +
        '<span class="sdi-name">' + h(s.name) + '</span>' +
        '<span class="sdi-meta">' + h(s.version || '') + '</span>' +
        phaseBadge(s.phase) +
        '<span class="sdi-meta">' + sources.map(sourcePill).join(' ') + '</span>' +
        '</div>';
    }).join('');
  }
  dd.classList.add('open');
}
function highlightDropdownItem(items) {
  for (var i = 0; i < items.length; i++) items[i].classList.toggle('active', i === searchDropdownIndex);
}
function closeSearchDropdown() {
  var dd = document.getElementById('search-dropdown');
  if (dd) { dd.classList.remove('open'); dd.innerHTML = ''; }
  searchDropdownIndex = -1;
}
// Close dropdown when clicking outside
document.addEventListener('click', function(e) {
  if (!e.target.closest('.topbar-search')) closeSearchDropdown();
});

function isMonitoredPhase(phase) {
  return phase === 'Healthy' || phase === 'Degraded' || phase === 'Invalid';
}

function isServiceVisible(svc) {
  var phase = svc.phase;
  var sources = getSources(svc);
  // "Unknown" filter matches any non-monitored phase (Unknown, Reference, empty, etc.)
  var phaseMatch = (state.filter === 'all' || (state.filter === 'Unknown' ? !isMonitoredPhase(phase) : state.filter === phase));
  var sourceMatch = sources.some(function(s) { return isSourceEnabled(s); });
  var searchTerm = getSearchTerm();
  var searchMatch = true;
  if (searchTerm) {
    var text = [svc.name, svc.owner || '', svc.version || '', sources.join(' ')].join(' ').toLowerCase();
    searchMatch = text.indexOf(searchTerm) !== -1;
  }
  return phaseMatch && sourceMatch && searchMatch;
}

function applyFilter() {
  var searchTerm = getSearchTerm();
  var rows = document.querySelectorAll('tr[data-phase]');
  for (var i = 0; i < rows.length; i++) {
    var row = rows[i];
    var phase = row.dataset.phase;
    var rowSources = (row.dataset.sources || '').split(',');
    var phaseMatch = (state.filter === 'all' || (state.filter === 'Unknown' ? !isMonitoredPhase(phase) : state.filter === phase));
    var sourceMatch = rowSources.some(function(s) { return isSourceEnabled(s); });
    var searchMatch = true;
    if (searchTerm) {
      var rowSearch = row.dataset.search || '';
      searchMatch = rowSearch.indexOf(searchTerm) !== -1;
    }
    row.style.display = (phaseMatch && sourceMatch && searchMatch) ? '' : 'none';
  }

  // Recompute stat counts from the filtered service set
  var svcs = state.services || [];
  var healthy = 0, degraded = 0, invalid = 0, unknown = 0;
  for (var i = 0; i < svcs.length; i++) {
    if (!isServiceVisible(svcs[i])) continue;
    var p = svcs[i].phase;
    if (p === 'Healthy') healthy++;
    else if (p === 'Degraded') degraded++;
    else if (p === 'Invalid') invalid++;
    else unknown++;
  }
  var total = healthy + degraded + invalid + unknown;
  var monitored = healthy + degraded + invalid;

  // Update stat card values
  var statEls = document.querySelectorAll('.stats-bar .stat');
  statEls.forEach(function(el) {
    var onclick = el.getAttribute('onclick') || '';
    var match = onclick.match(/toggleFilter\('(\w+)'\)/);
    if (!match) return;
    var key = match[1];
    var val = key === 'all' ? total : key === 'Healthy' ? healthy : key === 'Degraded' ? degraded : key === 'Invalid' ? invalid : key === 'Unknown' ? unknown : null;
    if (val !== null) {
      var valEl = el.querySelector('.stat-value');
      if (valEl) valEl.textContent = val;
    }
  });

  // Update status bar widths
  var bars = document.querySelectorAll('.status-bar > div');
  if (bars.length === 4 && total > 0) {
    bars[0].style.width = pct(healthy, total) + '%';
    bars[1].style.width = pct(degraded, total) + '%';
    bars[2].style.width = pct(invalid, total) + '%';
    bars[3].style.width = pct(unknown, total) + '%';
  }

  // Update subtitle
  var subtitle = document.querySelector('.page-subtitle');
  if (subtitle) subtitle.textContent = total + ' contract' + (total !== 1 ? 's' : '') + ' \u2014 ' + monitored + ' active' + (unknown > 0 ? ', ' + unknown + ' unmonitored' : '');

  // Filter "Needs Attention" cards using full filter pipeline
  var atRisk = document.getElementById('at-risk-section');
  if (atRisk) {
    var atRiskCards = atRisk.querySelectorAll('.alert-card');
    var anyVisible = false;
    atRiskCards.forEach(function(card) {
      var cardName = (card.getAttribute('onclick') || '').match(/navigateTo\('detail','(.+?)'\)/);
      if (cardName) {
        var svc = svcs.find(function(s) { return s.name === cardName[1]; });
        var vis = svc ? isServiceVisible(svc) : false;
        card.style.display = vis ? '' : 'none';
        if (vis) anyVisible = true;
      }
    });
    atRisk.style.display = anyVisible ? '' : 'none';
  }

  var indicator = document.getElementById('filter-indicator');
  if (indicator) {
    var hasSourceFilter = Object.keys(enabledSources).length > 0;
    var hasPhaseFilter = state.filter !== 'all';
    var hasSearch = getSearchTerm() !== '';
    indicator.style.display = (hasPhaseFilter || hasSourceFilter || hasSearch) ? '' : 'none';
    var label = document.getElementById('filter-label');
    if (label) {
      var parts = [];
      if (hasPhaseFilter) parts.push(state.filter);
      if (hasSourceFilter) {
        parts.push('sources: ' + Object.keys(enabledSources).join(', '));
      }
      if (hasSearch) parts.push('search: "' + getSearchTerm() + '"');
      label.textContent = parts.join(' \u2014 ');
    }
  }
}

/* ── Service Connections table (below graph) ─── */
function renderConnectionsTable() {
  var el = document.getElementById('graph-connections');
  if (!el) return;
  var gd = state.graphData;
  if (!gd || !gd.nodes || !gd.nodes.length) { el.innerHTML = ''; return; }

  var o = '<div class="section-heading">Service Connections</div>';
  o += '<div class="table-wrapper"><table><thead><tr><th>Service</th><th>Status</th><th>Dependencies</th></tr></thead><tbody>';
  for (var i = 0; i < gd.nodes.length; i++) {
    var node = gd.nodes[i];
    var edges = node.edges || [];
    if (node.status === 'external') {
      o += '<tr>';
      o += '<td>' + h(node.serviceName) + ' <span class="badge badge-neutral">external</span></td>';
      o += '<td><span class="badge badge-neutral"><span class="badge-dot"></span>External</span></td>';
    } else {
      o += '<tr data-click onclick="navigateTo(\'detail\',\'' + ha(node.serviceName) + '\')">';
      o += '<td><a>' + h(node.serviceName) + '</a></td>';
      o += '<td>' + phaseBadge(node.status) + '</td>';
    }
    o += '<td>';
    if (edges.length) {
      for (var j = 0; j < edges.length; j++) {
        var e = edges[j];
        o += '<span class="dep-link" onclick="event.stopPropagation();navigateTo(\'detail\',\'' + ha(e.targetName) + '\')">' + h(e.targetName) + '</span>';
        if (e.type === 'reference') o += ' <span class="badge badge-accent" style="font-size:10px">ref</span>';
        else if (e.required) o += ' <span class="badge badge-info" style="font-size:10px">req</span>';
        if (j < edges.length - 1) o += ', ';
      }
    } else {
      o += '<span class="text-dim">\u2014</span>';
    }
    o += '</td></tr>';
  }
  o += '</tbody></table></div>';
  el.innerHTML = o;
}

/* ════════════════════════════════════════════════════════════════
   D3 FORCE-DIRECTED GRAPH — adapted from operator v8 overview.html
   ════════════════════════════════════════════════════════════════ */
var graphSim = null, graphZoom = null, graphSvg = null, graphG = null;
var graphNodePositions = {}; // cached {id: {x, y}} across re-inits

function initGraph() {
  var container = document.getElementById('graph-container');
  if (!container || !state.graphData || !state.graphData.nodes) return;
  graphInitialized = true;

  var rawNodes = state.graphData.nodes;
  if (!rawNodes.length) {
    container.innerHTML += '<div class="empty-state" style="position:absolute;top:50%;left:50%;transform:translate(-50%,-50%)"><div class="empty-state-title">No graph data</div></div>';
    return;
  }

  var rect = container.getBoundingClientRect();
  var width = rect.width || 800;
  var height = rect.height || 500;

  // Build nodes and links
  var nodeMap = {};
  var nodes = [];
  var links = [];

  // BFS adjacency list + incoming count for depth computation
  var adjList = {};
  var incoming = {};
  rawNodes.forEach(function(rn) { incoming[rn.id] = 0; adjList[rn.id] = []; });
  rawNodes.forEach(function(rn) {
    (rn.edges || []).forEach(function(e) {
      if (incoming[e.targetId] !== undefined) {
        adjList[rn.id].push(e.targetId);
        incoming[e.targetId]++;
      }
    });
  });

  // BFS to compute depths (topological ordering, matching v8 exactly)
  var depths = {};
  var queue = [];
  rawNodes.forEach(function(n) { if (incoming[n.id] === 0) { queue.push(n.id); depths[n.id] = 0; } });
  while (queue.length > 0) {
    var cur = queue.shift();
    (adjList[cur] || []).forEach(function(tid) {
      if (depths[tid] === undefined || depths[tid] < depths[cur] + 1) {
        depths[tid] = depths[cur] + 1;
        queue.push(tid);
      }
    });
  }

  var byDepth = {};
  var maxDepth = 0;
  rawNodes.forEach(function(n) {
    var d = depths[n.id] || 0;
    if (!byDepth[d]) byDepth[d] = [];
    byDepth[d].push(n.id);
    if (d > maxDepth) maxDepth = d;
  });

  var nodeW = 160, nodeH = 44;
  var colSpacing = nodeW + 60; // 220px — v8 exact
  var rowSpacing = nodeH + 30; // 74px — v8 exact

  rawNodes.forEach(function(rn) {
    var status = rn.status || 'Unknown';
    if (status === 'external') status = 'External';
    else if (status === 'Unknown') status = 'Unmonitored';
    var d = depths[rn.id] || 0;
    var col = byDepth[d] || [rn.id];
    var row = col.indexOf(rn.id);
    var totalH = col.length * rowSpacing;
    var cached = graphNodePositions[rn.id];
    var node = {
      id: rn.id, serviceName: rn.serviceName, status: status, source: rn.source || '',
      edges: rn.edges || [], depth: d,
      x: cached ? cached.x : width / 2 - (maxDepth * colSpacing) / 2 + d * colSpacing,
      y: cached ? cached.y : height / 2 - totalH / 2 + row * rowSpacing
    };
    nodes.push(node);
    nodeMap[node.id] = node;
  });

  nodes.forEach(function(n) {
    n.edges.forEach(function(e) {
      if (nodeMap[e.targetId]) {
        links.push({ source: n.id, target: e.targetId, required: e.required, type: e.type || 'dependency' });
      }
    });
  });

  // Save current zoom transform before re-creating SVG
  var savedTransform = null;
  var existing = container.querySelector('svg');
  if (existing && graphSvg) {
    try { savedTransform = d3.zoomTransform(graphSvg.node()); } catch (_e) {}
  }
  if (existing) existing.remove();

  graphSvg = d3.select(container).append('svg')
    .attr('width', '100%')
    .attr('height', '100%')
    .style('display', 'block');

  graphZoom = d3.zoom()
    .scaleExtent([0.2, 4])
    .on('zoom', function(event) { graphG.attr('transform', event.transform); });

  graphSvg.call(graphZoom);
  graphG = graphSvg.append('g');

  // Arrow markers — v8 exact viewBox '0 0 10 6'
  var defs = graphSvg.append('defs');
  ['required', 'optional', 'reference'].forEach(function(type) {
    defs.append('marker')
      .attr('id', 'arrow-' + type)
      .attr('viewBox', '0 0 10 6')
      .attr('refX', 10).attr('refY', 3)
      .attr('markerWidth', 5).attr('markerHeight', 4)
      .attr('orient', 'auto')
      .append('path')
      .attr('d', 'M0,0 L10,3 L0,6 Z')
      .attr('fill', type === 'reference' ? 'var(--accent)' : type === 'required' ? 'var(--text-secondary)' : 'var(--text-dim)');
  });

  // Force simulation — v8 exact parameters
  graphSim = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).id(function(d) { return d.id; }).distance(180).strength(0.7))
    .force('charge', d3.forceManyBody().strength(-500))
    .force('x', d3.forceX(function(d) { return width / 2 - (maxDepth * colSpacing) / 2 + d.depth * colSpacing; }).strength(0.3))
    .force('y', d3.forceY(height / 2).strength(0.05))
    .force('collision', d3.forceCollide().radius(80))
    .alphaDecay(0.06)
    .velocityDecay(0.5);

  // Draw links — v8: edges go from right edge of source to left edge of target
  var link = graphG.selectAll('.edge-line')
    .data(links)
    .join('line')
    .attr('class', function(d) { return 'edge-line' + (d.type === 'reference' ? ' edge-reference' : ''); })
    .attr('stroke', function(d) { return d.type === 'reference' ? 'var(--accent)' : d.required ? 'var(--text-secondary)' : 'var(--text-dim)'; })
    .attr('stroke-width', function(d) { return d.type === 'reference' ? 1.5 : d.required ? 2 : 1; })
    .attr('stroke-dasharray', function(d) { return d.type === 'reference' ? '6,4' : d.required ? null : '4,3'; })
    .attr('marker-end', function(d) { return 'url(#arrow-' + (d.type === 'reference' ? 'reference' : d.required ? 'required' : 'optional') + ')'; })
    .attr('opacity', 0.7);

  // Status colors — v8 uses bg-surface fill for all nodes, stroke color differentiates
  function statusColor(status) {
    return { Healthy: 'var(--ok)', Degraded: 'var(--warning)', Invalid: 'var(--critical)', Unmonitored: 'var(--neutral)', External: 'var(--neutral)' }[status] || 'var(--neutral)';
  }

  function displayStatus(status) {
    return status === 'Unmonitored' ? 'Unmonitored' : status === 'External' ? 'External' : status;
  }

  // Draw nodes — v8: top-left origin, left-aligned text, bg-surface fill
  var nodeGroup = graphG.selectAll('.node-group')
    .data(nodes)
    .join('g')
    .attr('class', function(d) { return 'node-group' + (d.status === 'External' ? ' node-external' : ''); })
    .attr('transform', function(d) { return 'translate(' + d.x + ',' + d.y + ')'; })
    .on('click', function(event, d) {
      if (event.defaultPrevented) return;
      if (d.status === 'External') return; // external nodes have no detail page
      navigateTo('detail', d.serviceName);
    })
    .on('dblclick', function(event, d) {
      event.preventDefault();
      d.fx = null; d.fy = null;
      graphSim.alphaTarget(0.1).restart();
      setTimeout(function() { graphSim.alphaTarget(0); }, 500);
    })
    .call(d3.drag()
      .on('start', function(event, d) {
        if (!event.active) graphSim.alphaTarget(0.3).restart();
        d.fx = d.x; d.fy = d.y;
      })
      .on('drag', function(event, d) { d.fx = event.x; d.fy = event.y; })
      .on('end', function(event, d) {
        if (!event.active) graphSim.alphaTarget(0);
        graphNodePositions[d.id] = { x: d.x, y: d.y };
      })
    );

  nodeGroup.append('rect')
    .attr('width', nodeW).attr('height', nodeH)
    .attr('rx', 6)
    .attr('fill', 'var(--bg-surface)')
    .attr('stroke', function(d) { return statusColor(d.status); })
    .attr('stroke-width', 1.5);

  // v8: left-aligned labels at x=10, y=18
  nodeGroup.append('text')
    .attr('class', 'node-label')
    .attr('x', 10).attr('y', 18)
    .text(function(d) { var n = d.serviceName; return n.length > 20 ? n.substring(0, 18) + '...' : n; });

  // v8: left-aligned status at x=10, y=34
  nodeGroup.append('text')
    .attr('class', 'node-status')
    .attr('x', 10).attr('y', 34)
    .attr('fill', function(d) { return statusColor(d.status); })
    .text(function(d) { return displayStatus(d.status); });

  // Impact chain: build reverse required dependency map
  function isBroken(status) { return status === 'Invalid' || status === 'Degraded'; }

  var reverseDeps = {};
  links.forEach(function(l) {
    if (l.required) {
      var tid = typeof l.target === 'object' ? l.target.id : l.target;
      var sid = typeof l.source === 'object' ? l.source.id : l.source;
      if (!reverseDeps[tid]) reverseDeps[tid] = [];
      reverseDeps[tid].push(sid);
    }
  });

  function getImpactChain(nodeId) {
    var chain = new Set();
    var q = [nodeId];
    while (q.length) {
      var cur = q.shift();
      (reverseDeps[cur] || []).forEach(function(dep) {
        if (!chain.has(dep)) { chain.add(dep); q.push(dep); }
      });
    }
    return chain;
  }

  // Add warning icon to impacted (non-broken) nodes — v8: x=nodeW-20, y=16
  var allImpacted = new Set();
  nodes.filter(function(n) { return isBroken(n.status); }).forEach(function(bn) {
    getImpactChain(bn.id).forEach(function(id) { allImpacted.add(id); });
  });

  nodeGroup.filter(function(d) { return allImpacted.has(d.id) && !isBroken(d.status); })
    .append('text')
    .attr('class', 'node-impact-icon')
    .attr('x', nodeW - 20).attr('y', 16)
    .attr('fill', 'var(--warning)')
    .text('\u26A0');

  // Hover impact chain — v8: highlights chain, dims nothing except via CSS
  nodeGroup.on('mouseenter', function(event, d) {
    if (!isBroken(d.status)) return;
    var chain = getImpactChain(d.id);
    chain.add(d.id);
    nodeGroup.classed('graph-highlight', function(n) { return chain.has(n.id); });
    link.classed('graph-impact', function(l) {
      var sid = typeof l.source === 'object' ? l.source.id : l.source;
      var tid = typeof l.target === 'object' ? l.target.id : l.target;
      return chain.has(sid) && chain.has(tid);
    });
  }).on('mouseleave', function() {
    nodeGroup.classed('graph-highlight', false);
    link.classed('graph-impact', false);
  });

  // Compute closest edge point on a box (x,y is top-left, w/h is size) to an external point (px,py).
  function closestBoxPoint(x, y, w, h, px, py) {
    var cx = x + w / 2, cy = y + h / 2;
    var dx = px - cx, dy = py - cy;
    if (dx === 0 && dy === 0) return [cx, y]; // degenerate: same center
    var absDx = Math.abs(dx), absDy = Math.abs(dy);
    // Check which box edge the line from center to (px,py) intersects first
    var scaleX = (w / 2) / (absDx || 1);
    var scaleY = (h / 2) / (absDy || 1);
    var scale = Math.min(scaleX, scaleY);
    return [cx + dx * scale, cy + dy * scale];
  }

  function updatePositions() {
    link.each(function(d) {
      var s = d.source, t = d.target;
      var sCx = s.x + nodeW / 2, sCy = s.y + nodeH / 2;
      var tCx = t.x + nodeW / 2, tCy = t.y + nodeH / 2;
      var sp = closestBoxPoint(s.x, s.y, nodeW, nodeH, tCx, tCy);
      var tp = closestBoxPoint(t.x, t.y, nodeW, nodeH, sCx, sCy);
      d3.select(this).attr('x1', sp[0]).attr('y1', sp[1]).attr('x2', tp[0]).attr('y2', tp[1]);
    });
    nodeGroup.attr('transform', function(d) { return 'translate(' + d.x + ',' + d.y + ')'; });
  }

  // Register tick handler for drag interactions
  graphSim.on('tick', updatePositions);

  // Run simulation synchronously for initial layout, then stop — v8: 150 ticks
  graphSim.stop();
  for (var i = 0; i < 150; i++) graphSim.tick();
  updatePositions();
  // Cache final positions so re-inits don't cause nodes to jump
  nodes.forEach(function(n) { graphNodePositions[n.id] = { x: n.x, y: n.y }; });

  // v8: fitToView using getBBox with 400ms transition after 50ms delay
  function fitToView() {
    var bounds = graphG.node().getBBox();
    if (bounds.width === 0 || bounds.height === 0) return;
    var pad = 60;
    var scale = Math.min(width / (bounds.width + pad * 2), height / (bounds.height + pad * 2), 1.5);
    var tx = (width - bounds.width * scale) / 2 - bounds.x * scale;
    var ty = (height - bounds.height * scale) / 2 - bounds.y * scale;
    graphSvg.transition().duration(400).call(graphZoom.transform, d3.zoomIdentity.translate(tx, ty).scale(scale));
  }
  // Restore previous zoom/pan if re-initializing; otherwise fit to view
  if (savedTransform) {
    graphSvg.call(graphZoom.transform, savedTransform);
  } else {
    setTimeout(fitToView, 50);
  }

  // Store references for zoom controls and filtering
  graphNodes = nodes; graphLinks = link; graphNodeGroup = nodeGroup; graphUpdatePositions = updatePositions; graphFitToView = fitToView;

  // Render legend
  renderGraphLegend(nodes);
}

// Store graph references for external access
var graphNodes = null, graphLinks = null, graphNodeGroup = null, graphUpdatePositions = null, graphFitToView = null;

function graphZoomIn() { if (graphSvg) graphSvg.transition().duration(300).call(graphZoom.scaleBy, 1.3); }
function graphZoomOut() { if (graphSvg) graphSvg.transition().duration(300).call(graphZoom.scaleBy, 0.7); }
function graphResetView() {
  if (!graphSim || !graphNodes) return;
  // v8: unpin all nodes, re-run simulation, refit
  graphNodes.forEach(function(n) { n.fx = null; n.fy = null; });
  graphNodePositions = {};
  graphSim.stop();
  graphSim.alpha(1);
  for (var i = 0; i < 150; i++) graphSim.tick();
  graphNodes.forEach(function(n) { graphNodePositions[n.id] = { x: n.x, y: n.y }; });
  if (graphUpdatePositions) graphUpdatePositions();
  if (graphFitToView) graphFitToView();
}

function renderGraphLegend(nodes) {
  var el = document.getElementById('graph-legend');
  if (!el) return;
  var statuses = ['Healthy', 'Degraded', 'Invalid', 'Unmonitored', 'External'];
  var colors = { Healthy: 'var(--ok)', Degraded: 'var(--warning)', Invalid: 'var(--critical)', Unmonitored: 'var(--neutral)', External: 'var(--neutral)' };
  var counts = {};
  for (var i = 0; i < nodes.length; i++) {
    if (isNodeFiltered(nodes[i])) continue;
    var s = nodes[i].status;
    counts[s] = (counts[s] || 0) + 1;
  }

  var o = '';
  for (var i = 0; i < statuses.length; i++) {
    var s = statuses[i];
    if (!counts[s]) continue;
    o += '<span class="legend-item">';
    o += '<span class="legend-dot" style="background:' + colors[s] + '"></span>';
    o += s + ' (' + counts[s] + ')';
    o += '</span>';
  }
  o += '<span class="legend-sep">|</span>';
  o += '<span class="legend-item" style="font-size:10px"><span style="display:inline-block;width:16px;border-top:2px solid var(--text-secondary)"></span> required</span>';
  o += '<span class="legend-item" style="font-size:10px"><span style="display:inline-block;width:16px;border-top:1px dashed var(--text-dim)"></span> optional</span>';
  o += '<span class="legend-item" style="font-size:10px"><span style="display:inline-block;width:16px;border-top:1.5px dashed var(--accent)"></span> reference</span>';
  el.innerHTML = o;
}

function isNodeFiltered(d) {
  if (state.filter !== 'all') {
    var phase = (d.status === 'Unmonitored' || d.status === 'External' || d.status === 'Reference') ? 'Unknown' : d.status;
    if (phase !== state.filter) return true;
  }
  if (Object.keys(enabledSources).length > 0) {
    // Match the table filter: check all sources the service belongs to.
    var svc = state.services.find(function(s) { return s.name === d.serviceName; });
    var nodeSources = svc ? getSources(svc) : (d.source ? [d.source] : []);
    if (!nodeSources.some(function(s) { return enabledSources[s]; })) return true;
  }
  return false;
}

function syncGraphFilters() {
  if (!graphSvg) return;
  // v8: dim both nodes AND edges when filtered
  graphSvg.selectAll('.node-group')
    .classed('graph-dimmed', function(d) { return isNodeFiltered(d); });
  graphSvg.selectAll('.edge-line')
    .classed('graph-dimmed', function(d) {
      var src = typeof d.source === 'object' ? d.source : null;
      var tgt = typeof d.target === 'object' ? d.target : null;
      if (!src || !tgt) return false;
      return isNodeFiltered(src) || isNodeFiltered(tgt);
    });
  // Update legend counts to reflect current filter
  if (graphNodes) renderGraphLegend(graphNodes);
}

/* ── Service-scoped dependency graph for detail page ── */
function extractSubgraph(graphData, focusId) {
  if (!graphData || !graphData.nodes) return null;
  var nodeMap = {};
  graphData.nodes.forEach(function(n) { nodeMap[n.id] = n; });

  // Build forward and reverse adjacency
  var forward = {};  // id -> [targetId, ...]
  var reverse = {};  // id -> [sourceId, ...]
  graphData.nodes.forEach(function(n) {
    forward[n.id] = [];
    if (!reverse[n.id]) reverse[n.id] = [];
    (n.edges || []).forEach(function(e) {
      forward[n.id].push(e.targetId);
      if (!reverse[e.targetId]) reverse[e.targetId] = [];
      reverse[e.targetId].push(n.id);
    });
  });

  // BFS in both directions to collect the connected subgraph
  var visited = {};
  var queue = [focusId];
  visited[focusId] = true;
  while (queue.length) {
    var cur = queue.shift();
    (forward[cur] || []).forEach(function(id) { if (!visited[id]) { visited[id] = true; queue.push(id); } });
    (reverse[cur] || []).forEach(function(id) { if (!visited[id]) { visited[id] = true; queue.push(id); } });
  }

  var subNodes = graphData.nodes.filter(function(n) { return visited[n.id]; });
  if (subNodes.length === 0) return null;
  return { nodes: subNodes };
}

function initServiceGraph(containerId, graphData, focusId) {
  var container = document.getElementById(containerId);
  if (!container || !graphData || !graphData.nodes || graphData.nodes.length === 0) {
    if (container) container.innerHTML = '<div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:40px">No dependency relationships to display</div>';
    return;
  }

  var rawNodes = graphData.nodes;
  var rect = container.getBoundingClientRect();
  var width = rect.width || 700;
  var height = rect.height || 400;

  var nodeMap = {};
  var nodes = [];
  var links = [];

  // BFS depth computation
  var adjList = {};
  var incoming = {};
  rawNodes.forEach(function(rn) { incoming[rn.id] = 0; adjList[rn.id] = []; });
  rawNodes.forEach(function(rn) {
    (rn.edges || []).forEach(function(e) {
      if (incoming[e.targetId] !== undefined) {
        adjList[rn.id].push(e.targetId);
        incoming[e.targetId]++;
      }
    });
  });

  var depths = {};
  var queue = [];
  rawNodes.forEach(function(n) { if (incoming[n.id] === 0) { queue.push(n.id); depths[n.id] = 0; } });
  while (queue.length) {
    var cur = queue.shift();
    (adjList[cur] || []).forEach(function(tid) {
      if (depths[tid] === undefined || depths[tid] < depths[cur] + 1) {
        depths[tid] = depths[cur] + 1;
        queue.push(tid);
      }
    });
  }

  var byDepth = {};
  var maxDepth = 0;
  rawNodes.forEach(function(n) {
    var d = depths[n.id] || 0;
    if (!byDepth[d]) byDepth[d] = [];
    byDepth[d].push(n.id);
    if (d > maxDepth) maxDepth = d;
  });

  var nodeW = 160, nodeH = 44;
  var colSpacing = nodeW + 60;
  var rowSpacing = nodeH + 30;

  rawNodes.forEach(function(rn) {
    var status = rn.status || 'Unknown';
    if (status === 'external') status = 'External';
    else if (status === 'Unknown') status = 'Unmonitored';
    var d = depths[rn.id] || 0;
    var col = byDepth[d] || [rn.id];
    var row = col.indexOf(rn.id);
    var totalH = col.length * rowSpacing;
    var node = {
      id: rn.id, serviceName: rn.serviceName, status: status, source: rn.source || '',
      edges: rn.edges || [], depth: d, isFocus: rn.id === focusId,
      x: width / 2 - (maxDepth * colSpacing) / 2 + d * colSpacing,
      y: height / 2 - totalH / 2 + row * rowSpacing
    };
    nodes.push(node);
    nodeMap[node.id] = node;
  });

  nodes.forEach(function(n) {
    n.edges.forEach(function(e) {
      if (nodeMap[e.targetId]) {
        links.push({ source: n.id, target: e.targetId, required: e.required, type: e.type || 'dependency' });
      }
    });
  });

  var svg = d3.select(container).append('svg')
    .attr('width', '100%')
    .attr('height', '100%')
    .style('display', 'block');

  var zoom = d3.zoom()
    .scaleExtent([0.2, 4])
    .on('zoom', function(event) { g.attr('transform', event.transform); });
  svg.call(zoom);
  var g = svg.append('g');

  var defs = svg.append('defs');
  ['required', 'optional', 'reference'].forEach(function(type) {
    defs.append('marker')
      .attr('id', 'svc-arrow-' + type)
      .attr('viewBox', '0 0 10 6')
      .attr('refX', 10).attr('refY', 3)
      .attr('markerWidth', 5).attr('markerHeight', 4)
      .attr('orient', 'auto')
      .append('path')
      .attr('d', 'M0,0 L10,3 L0,6 Z')
      .attr('fill', type === 'reference' ? 'var(--accent)' : type === 'required' ? 'var(--text-secondary)' : 'var(--text-dim)');
  });

  var sim = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).id(function(d) { return d.id; }).distance(180).strength(0.7))
    .force('charge', d3.forceManyBody().strength(-500))
    .force('x', d3.forceX(function(d) { return width / 2 - (maxDepth * colSpacing) / 2 + d.depth * colSpacing; }).strength(0.3))
    .force('y', d3.forceY(height / 2).strength(0.05))
    .force('collision', d3.forceCollide().radius(80))
    .alphaDecay(0.06)
    .velocityDecay(0.5);

  var link = g.selectAll('.edge-line')
    .data(links)
    .join('line')
    .attr('class', function(d) { return 'edge-line' + (d.type === 'reference' ? ' edge-reference' : ''); })
    .attr('stroke', function(d) { return d.type === 'reference' ? 'var(--accent)' : d.required ? 'var(--text-secondary)' : 'var(--text-dim)'; })
    .attr('stroke-width', function(d) { return d.type === 'reference' ? 1.5 : d.required ? 2 : 1; })
    .attr('stroke-dasharray', function(d) { return d.type === 'reference' ? '6,4' : d.required ? null : '4,3'; })
    .attr('marker-end', function(d) { return 'url(#svc-arrow-' + (d.type === 'reference' ? 'reference' : d.required ? 'required' : 'optional') + ')'; })
    .attr('opacity', 0.7);

  function statusColor(status) {
    return { Healthy: 'var(--ok)', Degraded: 'var(--warning)', Invalid: 'var(--critical)', Unmonitored: 'var(--neutral)', External: 'var(--neutral)' }[status] || 'var(--neutral)';
  }
  function displayStatus(status) {
    return status === 'Unmonitored' ? 'Unmonitored' : status === 'External' ? 'External' : status;
  }

  var nodeGroup = g.selectAll('.node-group')
    .data(nodes)
    .join('g')
    .attr('class', function(d) { return 'node-group' + (d.status === 'External' ? ' node-external' : ''); })
    .attr('transform', function(d) { return 'translate(' + d.x + ',' + d.y + ')'; })
    .on('click', function(event, d) {
      if (event.defaultPrevented) return;
      if (d.status === 'External') return;
      navigateTo('detail', d.serviceName);
    })
    .call(d3.drag()
      .on('start', function(event, d) { if (!event.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
      .on('drag', function(event, d) { d.fx = event.x; d.fy = event.y; })
      .on('end', function(event, d) { if (!event.active) sim.alphaTarget(0); })
    );

  nodeGroup.append('rect')
    .attr('width', nodeW).attr('height', nodeH)
    .attr('rx', 6)
    .attr('fill', 'var(--bg-surface)')
    .attr('stroke', function(d) { return d.isFocus ? 'var(--accent)' : statusColor(d.status); })
    .attr('stroke-width', function(d) { return d.isFocus ? 2.5 : 1.5; });

  nodeGroup.append('text')
    .attr('class', 'node-label')
    .attr('x', 10).attr('y', 18)
    .attr('font-weight', function(d) { return d.isFocus ? '700' : null; })
    .text(function(d) { var n = d.serviceName; return n.length > 20 ? n.substring(0, 18) + '...' : n; });

  nodeGroup.append('text')
    .attr('class', 'node-status')
    .attr('x', 10).attr('y', 34)
    .attr('fill', function(d) { return statusColor(d.status); })
    .text(function(d) { return displayStatus(d.status); });

  // Impact chain: build reverse required dependency map
  function isBroken(status) { return status === 'Invalid' || status === 'Degraded'; }

  var reverseDeps = {};
  links.forEach(function(l) {
    if (l.required) {
      var tid = typeof l.target === 'object' ? l.target.id : l.target;
      var sid = typeof l.source === 'object' ? l.source.id : l.source;
      if (!reverseDeps[tid]) reverseDeps[tid] = [];
      reverseDeps[tid].push(sid);
    }
  });

  function getImpactChain(nodeId) {
    var chain = new Set();
    var q = [nodeId];
    while (q.length) {
      var cur = q.shift();
      (reverseDeps[cur] || []).forEach(function(dep) {
        if (!chain.has(dep)) { chain.add(dep); q.push(dep); }
      });
    }
    return chain;
  }

  // Add warning icon to impacted (non-broken) nodes
  var allImpacted = new Set();
  nodes.filter(function(n) { return isBroken(n.status); }).forEach(function(bn) {
    getImpactChain(bn.id).forEach(function(id) { allImpacted.add(id); });
  });

  nodeGroup.filter(function(d) { return allImpacted.has(d.id) && !isBroken(d.status); })
    .append('text')
    .attr('class', 'node-impact-icon')
    .attr('x', nodeW - 20).attr('y', 16)
    .attr('fill', 'var(--warning)')
    .text('\u26A0');

  // Hover impact chain — highlights chain + dims others
  nodeGroup.on('mouseenter', function(event, d) {
    if (!isBroken(d.status)) return;
    var chain = getImpactChain(d.id);
    chain.add(d.id);
    nodeGroup.classed('graph-highlight', function(n) { return chain.has(n.id); });
    link.classed('graph-impact', function(l) {
      var sid = typeof l.source === 'object' ? l.source.id : l.source;
      var tid = typeof l.target === 'object' ? l.target.id : l.target;
      return chain.has(sid) && chain.has(tid);
    });
  }).on('mouseleave', function() {
    nodeGroup.classed('graph-highlight', false);
    link.classed('graph-impact', false);
  });

  function closestBoxPoint(x, y, w, hh, px, py) {
    var cx = x + w / 2, cy = y + hh / 2;
    var dx = px - cx, dy = py - cy;
    if (dx === 0 && dy === 0) return [cx, y];
    var scaleX = (w / 2) / (Math.abs(dx) || 1);
    var scaleY = (hh / 2) / (Math.abs(dy) || 1);
    var scale = Math.min(scaleX, scaleY);
    return [cx + dx * scale, cy + dy * scale];
  }

  function updatePositions() {
    link.each(function(d) {
      var s = d.source, t = d.target;
      var sp = closestBoxPoint(s.x, s.y, nodeW, nodeH, t.x + nodeW / 2, t.y + nodeH / 2);
      var tp = closestBoxPoint(t.x, t.y, nodeW, nodeH, s.x + nodeW / 2, s.y + nodeH / 2);
      d3.select(this).attr('x1', sp[0]).attr('y1', sp[1]).attr('x2', tp[0]).attr('y2', tp[1]);
    });
    nodeGroup.attr('transform', function(d) { return 'translate(' + d.x + ',' + d.y + ')'; });
  }

  sim.on('tick', updatePositions);
  sim.stop();
  for (var i = 0; i < 150; i++) sim.tick();
  updatePositions();

  setTimeout(function() {
    var bounds = g.node().getBBox();
    if (bounds.width === 0 || bounds.height === 0) return;
    var pad = 40;
    var scale = Math.min(width / (bounds.width + pad * 2), height / (bounds.height + pad * 2), 1.5);
    var tx = (width - bounds.width * scale) / 2 - bounds.x * scale;
    var ty = (height - bounds.height * scale) / 2 - bounds.y * scale;
    svg.transition().duration(400).call(zoom.transform, d3.zoomIdentity.translate(tx, ty).scale(scale));
  }, 50);
}

function maybeInitServiceGraph() {
  if (state.tab !== 'dependencies' || !state.service) return;
  var subgraph = extractSubgraph(state.graphData, state.service);
  initServiceGraph('service-graph-container', subgraph, state.service);
}

/* ════════════════════════════════════════════════════════════════
   DETAIL PAGE — matches operator detail.html + all tab partials
   ════════════════════════════════════════════════════════════════ */
async function renderDetail() {
  var gen = _renderGen;
  var app = document.getElementById('app');
  var svcName = state.service;
  var hasExisting = state.details[svcName] != null;

  // Fetch global graph lazily for the dependency graph visualization
  if (!state.graphData) {
    api.getGraph().catch(function() { return null; }).then(function(g) {
      if (g) { state.graphData = g; if (_renderGen === gen) maybeInitServiceGraph(); }
    });
  }

  // If we have cached data, render immediately with morphing, then refresh in background
  if (hasExisting) {
    renderDetailPage();
    Promise.all([
      api.getService(svcName),
      api.getVersions(svcName).catch(function() { return []; }),
      api.getServiceSources(svcName).catch(function() { return null; }),
      api.getDependents(svcName).catch(function() { return []; }),
      api.getCrossRefs(svcName).catch(function() { return { references: [], referencedBy: [] }; })
    ]).then(function(r) {
      if (_renderGen !== gen) return; // stale render
      state.details[svcName] = r[0];
      state.versions[svcName] = r[1] || [];
      if (r[2]) state.aggregated[svcName] = r[2];
      state.dependents = r[3] || [];
      state.crossRefs = r[4] || { references: [], referencedBy: [] };
      renderDetailPage();
    }).catch(function() { /* keep stale data */ });
    return;
  }

  _currentPage = null;
  app.innerHTML = '<div class="loading"><div class="spinner"></div>Loading...</div>';
  try {
    // Fetch service details first for fast render
    var details = await api.getService(svcName);
    if (_renderGen !== gen) return; // stale render
    state.details[svcName] = details;
    state.dependents = [];
    state.crossRefs = { references: [], referencedBy: [] };
    renderDetailPage();
    // Load supplementary data in background
    Promise.all([
      api.getVersions(svcName).catch(function() { return []; }),
      api.getServiceSources(svcName).catch(function() { return null; }),
      api.getDependents(svcName).catch(function() { return []; }),
      api.getCrossRefs(svcName).catch(function() { return { references: [], referencedBy: [] }; })
    ]).then(function(r) {
      if (_renderGen !== gen) return; // stale render
      state.versions[svcName] = r[0] || [];
      if (r[1]) state.aggregated[svcName] = r[1];
      state.dependents = r[2] || [];
      state.crossRefs = r[3] || { references: [], referencedBy: [] };
      renderDetailPage();
    }).catch(function() {});
  } catch (e) {
    if (_renderGen !== gen) return; // stale render
    // If 404 and we have an OCI ref, try lazy resolution
    var depInfo = state.pendingRef ? { ref: state.pendingRef, compatibility: state.pendingCompat || '' } : findDepInfo(svcName);
    if (e.status === 404 && depInfo) {
      await resolveRemoteDep(svcName, gen, depInfo.ref, depInfo.compatibility);
    } else {
      app.innerHTML = '<div class="empty-state"><div class="empty-state-title">Service not found</div><p>' + h(e.message) + '</p>' +
        '<div style="margin-top:16px"><a class="dep-link" onclick="navigateTo(\'list\')">Back to overview</a></div></div>';
    }
  }
}

/* Find a dependency ref and compatibility for a service name from any loaded service's deps or cross-refs */
function findDepInfo(name) {
  for (var key in state.details) {
    var d = state.details[key];
    if (!d) continue;
    // Check regular dependencies
    if (d.dependencies) {
      for (var i = 0; i < d.dependencies.length; i++) {
        var dep = d.dependencies[i];
        var depName = dep.name || extractServiceName(dep.ref);
        if (depName === name && dep.ref && dep.ref !== name) return { ref: dep.ref, compatibility: dep.compatibility || '' };
      }
    }
    // Check configuration/policy refs (from contract fields)
    if (d.configuration && d.configuration.ref) {
      var cfgName = extractServiceName(d.configuration.ref);
      if (cfgName === name) return { ref: d.configuration.ref, compatibility: '' };
    }
    if (d.policy && d.policy.ref) {
      var polName = extractServiceName(d.policy.ref);
      if (polName === name) return { ref: d.policy.ref, compatibility: '' };
    }
  }
  // Also check cross-refs from the current service view
  if (state.crossRefs && state.crossRefs.references) {
    for (var i = 0; i < state.crossRefs.references.length; i++) {
      var cr = state.crossRefs.references[i];
      if (cr.name === name && cr.ref) return { ref: cr.ref, compatibility: '' };
    }
  }
  return null;
}

/* Attempt to lazily resolve a remote OCI dependency */
async function resolveRemoteDep(svcName, gen, ref, compatibility) {
  var app = document.getElementById('app');
  app.innerHTML = '<div class="loading"><div class="spinner"></div>Resolving remote dependency&hellip;<br><code class="text-dim" style="font-size:var(--text-xs);margin-top:8px;display:inline-block">' + h(ref) + (compatibility ? ' (' + h(compatibility) + ')' : '') + '</code></div>';
  try {
    var details = await api.resolveRef(ref, compatibility);
    if (_renderGen !== gen) return; // stale render
    // The resolved service might have a different name than what we navigated to
    var resolvedName = details.name || svcName;
    state.details[resolvedName] = details;
    if (resolvedName !== svcName) {
      state.service = resolvedName;
      history.replaceState(null, '', '#service/' + encodeURIComponent(resolvedName));
    }
    // Add to known services list so serviceExists() works
    if (!state.services.some(function(s) { return s.name === resolvedName; })) {
      state.services.push({ name: resolvedName, version: details.version, owner: details.owner, phase: details.phase, source: 'oci', sources: ['oci'] });
    }
    state.dependents = [];
    state.crossRefs = { references: [], referencedBy: [] };
    state.pendingRef = null;
    state.pendingCompat = null;
    renderDetailPage();
  } catch (re) {
    if (_renderGen !== gen) return; // stale render
    var errorTitle = 'Failed to resolve dependency';
    var errorMsg = re.message || 'Unknown error';
    if (re.status === 403) errorTitle = 'Authentication failed';
    else if (re.status === 404) errorTitle = 'Artifact not found in registry';
    else if (re.status === 422) errorTitle = 'Invalid reference or bundle';
    else if (re.status === 502) errorTitle = 'Registry unreachable';
    app.innerHTML = '<div class="empty-state"><div class="empty-state-title">' + h(errorTitle) + '</div>' +
      '<p>' + h(errorMsg) + '</p>' +
      '<code class="text-dim" style="font-size:var(--text-xs);display:block;margin-top:8px">' + h(ref) + '</code>' +
      '<div style="margin-top:16px"><a class="dep-link" onclick="navigateTo(\'list\')">Back to overview</a></div></div>';
  }
}

function renderDetailPage() {
  var d = state.details[state.service];
  if (!d) return;
  var versions = state.versions[state.service] || [];
  var agg = state.aggregated[state.service];
  var sources = getSources(d);

  var o = '';

  // Breadcrumb
  o += '<div class="breadcrumb"><a onclick="navigateTo(\'list\')">Overview</a><span class="separator">/</span><span>' + h(d.name) + '</span></div>';

  // Service header
  o += '<div class="service-header"><div style="display:flex;align-items:center;gap:8px;flex:1">';
  o += '<h1 class="service-title">' + h(d.name) + '</h1>';
  o += phaseBadge(d.phase);
  if (d.compliance) {
    o += complianceBadge(d.compliance.status);
    if (d.compliance.score != null) o += ' ' + complianceScoreBadge(d.compliance.score, d.compliance.status);
    if (d.compliance.summary) {
      var cs = d.compliance.summary;
      if (cs.errors > 0) o += ' <span class="pill pill-critical">' + cs.errors + ' error' + (cs.errors > 1 ? 's' : '') + '</span>';
      if (cs.warnings > 0) o += ' <span class="pill pill-warning">' + cs.warnings + ' warning' + (cs.warnings > 1 ? 's' : '') + '</span>';
    }
  }
  if (d.checksSummary) o += '<span class="text-dim" style="margin-left:4px">' + d.checksSummary.passed + '/' + d.checksSummary.total + ' checks</span>';
  if (d.owner) o += '<span class="text-dim" style="margin-left:4px">owner: ' + h(d.owner) + '</span>';
  o += '</div></div>';

  // Contract info line
  o += '<div class="contract-info-line">';
  if (d.version) o += '<span class="pill pill-dim">' + h(d.version) + '</span>';
  o += sources.map(sourcePill).join(' ');
  if (d.imageRef) o += '<code class="contract-ref-code">' + h(d.imageRef) + '</code>';
  o += '</div>';

  // Reference-only banner (v8 parity)
  if (d.phase === 'Unknown' || d.phase === 'Reference') {
    o += '<div style="display:flex;align-items:center;gap:10px;padding:12px 16px;margin-bottom:16px;border-radius:var(--radius-sm);background:var(--neutral-bg);border:1px solid var(--border);color:var(--text-secondary);font-size:var(--text-sm)">';
    o += '<span style="font-size:18px">\uD83D\uDCC4</span>';
    o += '<span><strong>Reference-only contract</strong> \u2014 no runtime target. Used as a shared definition or dependency reference.</span>';
    o += '</div>';
  }

  // Tab bar
  var deps = d.dependencies || [];
  o += '<div class="tab-bar" role="tablist">';
  o += tabBtn('overview', 'Overview');
  o += tabBtn('dependencies', 'Dependencies', deps.length || null);
  o += tabBtn('history', 'History', versions.length || null);
  if (versions.length > 1) o += tabBtn('diff', 'Diff');
  if (d.interfaces && d.interfaces.length) o += tabBtn('interfaces', 'Interfaces', d.interfaces.length);
  // Validations tab: show if conditions exist or validation issues exist
  var hasValidations = (d.conditions && d.conditions.length) || (d.validation && ((d.validation.errors || []).length || (d.validation.warnings || []).length));
  if (hasValidations) o += tabBtn('validations', 'Validations');
  // Contract vs Runtime tab: show if runtimeDiff exists
  if (d.runtimeDiff && d.runtimeDiff.length) o += tabBtn('runtime-diff', 'Contract vs Runtime');
  // Observed Runtime tab: show if observedRuntime exists
  if (d.observedRuntime) o += tabBtn('observed', 'Observed Runtime');
  var hasConfigIssues = hasValidationPath(d, 'configuration');
  var hasPolicyIssues = hasValidationPath(d, 'policy');
  if (d.configuration || hasConfigIssues) o += tabBtn('config', 'Config');
  if (d.policy || hasPolicyIssues) o += tabBtn('policy', 'Policy');
  if (agg && agg.sources && agg.sources.length > 1) o += tabBtn('sources', 'Sources', agg.sources.length);
  o += '</div>';

  // Tab content
  o += '<div id="tab-content">';
  o += renderCurrentTab(d, versions, agg);
  o += '</div>';

  updateApp(o, 'detail:' + state.service);
  maybeInitServiceGraph();
}

function tabBtn(id, label, count) {
  var active = state.tab === id;
  var o = '<button class="tab-btn' + (active ? ' tab-active' : '') + '" data-tab="' + id + '" onclick="switchTab(\'' + id + '\')">' + label;
  if (count) o += ' <span class="tab-count">' + count + '</span>';
  return o + '</button>';
}

function switchTab(tab) {
  state.tab = tab;
  var d = state.details[state.service];
  if (!d) return;
  var versions = state.versions[state.service] || [];
  var agg = state.aggregated[state.service];
  document.getElementById('tab-content').innerHTML = renderCurrentTab(d, versions, agg);
  document.querySelectorAll('.tab-btn').forEach(function(btn) {
    btn.classList.toggle('tab-active', btn.getAttribute('data-tab') === tab);
  });
  maybeInitServiceGraph();
}

function renderCurrentTab(d, versions, agg) {
  switch (state.tab) {
    case 'overview': return renderTabOverview(d);
    case 'dependencies': return renderTabDependencies(d);
    case 'history': return renderTabHistory(versions);
    case 'diff': return renderTabDiff(versions);
    case 'interfaces': return renderTabInterfaces(d);
    case 'validations': return renderTabValidations(d);
    case 'runtime-diff': return renderTabRuntimeDiff(d);
    case 'observed': return renderTabObservedRuntime(d);
    case 'config': return renderTabConfig(d);
    case 'policy': return renderTabPolicy(d);
    case 'sources': return renderTabSources(agg);
    default: return '';
  }
}

/* ─── Tab: Overview (matches operator partial-tab-overview.html) ─── */
function renderTabOverview(d) {
  var o = '';

  // 1. INSIGHTS (critical, warning, info)
  var insights = d.insights || [];
  if (insights.length) {
    o += '<div style="margin-bottom:24px"><div class="section-heading">Issues</div>';
    for (var i = 0; i < insights.length; i++) {
      var ins = insights[i];
      o += '<div class="insight-card ' + insightClass(ins.severity) + '">';
      o += '<div class="insight-icon">' + insightIcon(ins.severity) + '</div>';
      o += '<div class="insight-body"><div class="insight-title">' + h(ins.title) + '</div>';
      if (ins.description) o += '<div class="insight-desc">' + h(ins.description) + '</div>';
      o += '</div></div>';
    }
    o += '</div>';
  }

  // 2. Runtime Endpoint Probes (health + metrics)
  if (d.endpoints && d.endpoints.length) {
    var failing = [], healthy = [], unknown = [];
    for (var i = 0; i < d.endpoints.length; i++) {
      var ep = d.endpoints[i];
      if (ep.healthy === false) failing.push(ep);
      else if (ep.healthy === true) healthy.push(ep);
      else unknown.push(ep);
    }

    o += '<div class="card"><div class="card-header"><div class="section-label">Runtime Probes</div><div>';
    if (failing.length) o += '<span class="pill pill-critical">' + failing.length + ' failing</span> ';
    if (healthy.length) o += '<span class="pill pill-ok">' + healthy.length + ' reachable</span> ';
    if (unknown.length) o += '<span class="pill pill-neutral">' + unknown.length + ' unknown</span>';
    o += '</div></div>';
    o += '<div class="table-wrapper"><table><thead><tr><th>Status</th><th>Probe</th><th>Interface</th><th>URL</th><th class="hide-narrow">Code</th><th class="hide-narrow">Latency</th><th class="hide-narrow">Error</th></tr></thead><tbody>';
    var allEp = [].concat(failing, unknown, healthy);
    for (var i = 0; i < allEp.length; i++) {
      var ep = allEp[i];
      var st = ep.healthy === true ? '<span class="badge badge-ok">reachable</span>' : ep.healthy === false ? '<span class="badge badge-critical">failing</span>' : '<span class="badge badge-neutral">unknown</span>';
      var probeType = ep.type ? '<span class="pill pill-dim">' + h(ep.type) + '</span>' : '\u2014';
      var code = ep.statusCode != null ? '<code>' + ep.statusCode + '</code>' : '\u2014';
      var latency = ep.latencyMs != null ? ep.latencyMs + 'ms' : '\u2014';
      var errMsg = ep.error || ep.message || '';
      o += '<tr><td>' + st + '</td><td>' + probeType + '</td><td>' + h(ep.interface) + '</td><td><code>' + h(ep.url || '\u2014') + '</code></td><td class="hide-narrow">' + code + '</td><td class="hide-narrow">' + latency + '</td><td class="hide-narrow"><span class="text-dim">' + h(errMsg) + '</span></td></tr>';
    }
    o += '</tbody></table></div></div>';
  }

  // 3. STATUS + RESOURCES (left) and CONDITIONS (right)
  o += '<div class="detail-grid">';

  // Left: Status card
  o += '<div class="card"><div class="section-label">Status</div><table>';
  if (d.version) o += '<tr><td class="text-dim">Version</td><td>' + h(d.version) + '</td></tr>';
  if (d.imageRef) o += '<tr><td class="text-dim">Image</td><td><code>' + h(d.imageRef) + '</code></td></tr>';
  if (d.checksSummary) {
    var cs = d.checksSummary;
    o += '<tr><td class="text-dim">Checks</td><td><span class="count ' + (cs.failed > 0 ? 'count-error' : 'count-zero') + '">' + cs.passed + '/' + cs.total + ' passed</span></td></tr>';
  }
  if (d.lastReconciledAt) {
    o += '<tr><td class="text-dim">Reconciled</td><td><span class="text-dim">' + h(d.lastReconciledAt) + '</span></td></tr>';
  }
  o += '</table>';

  if (d.resources) {
    o += '<div class="section-label" style="margin-top:16px">Resources</div><table>';
    if (d.resources.serviceExists != null) {
      o += '<tr><td class="text-dim">Service</td><td>' + (d.resources.serviceExists ? '<span class="badge badge-ok">found</span>' : '<span class="badge badge-critical">not found</span>') + '</td></tr>';
    }
    if (d.resources.workloadExists != null) {
      o += '<tr><td class="text-dim">Workload</td><td>' + (d.resources.workloadExists ? '<span class="badge badge-ok">found</span>' : '<span class="badge badge-critical">not found</span>') + '</td></tr>';
    }
    o += '</table>';
  }

  if (d.ports) {
    o += '<div class="section-label" style="margin-top:16px">Ports</div><table>';
    if (d.ports.expected && d.ports.expected.length) o += '<tr><td class="text-dim">Expected</td><td>' + d.ports.expected.map(function(p) { return '<code>' + p + '</code>'; }).join(', ') + '</td></tr>';
    if (d.ports.observed && d.ports.observed.length) o += '<tr><td class="text-dim">Observed</td><td>' + d.ports.observed.map(function(p) { return '<code>' + p + '</code>'; }).join(', ') + '</td></tr>';
    if (d.ports.missing && d.ports.missing.length) o += '<tr><td class="text-dim">Missing</td><td>' + d.ports.missing.map(function(p) { return '<span class="count count-error"><code>' + p + '</code></span>'; }).join(', ') + '</td></tr>';
    if (d.ports.unexpected && d.ports.unexpected.length) o += '<tr><td class="text-dim">Unexpected</td><td>' + d.ports.unexpected.map(function(p) { return '<span class="count count-warning"><code>' + p + '</code></span>'; }).join(', ') + '</td></tr>';
    o += '</table>';
  }
  o += '</div>';

  // Right: Conditions card
  o += '<div class="card"><div class="section-label">Conditions</div>';
  if (d.conditions && d.conditions.length) {
    o += '<div class="conditions-grid">';
    for (var i = 0; i < d.conditions.length; i++) {
      var c = d.conditions[i];
      o += '<div class="condition-card"><div class="condition-type">' + condBadge(c.status) + ' ' + h(c.type) + '</div>';
      if (c.reason || c.lastTransitionAgo) {
        o += '<div class="condition-message" style="font-weight:500">';
        if (c.reason) o += h(c.reason);
        if (c.lastTransitionAgo) o += (c.reason ? ' \u00B7 ' : '') + h(c.lastTransitionAgo);
        o += '</div>';
      }
      if (c.message) o += '<div class="condition-message">' + h(c.message) + '</div>';
      o += '</div>';
    }
    o += '</div>';
  } else {
    o += '<div style="color:var(--text-dim);font-size:var(--text-sm)">No conditions reported</div>';
  }
  o += '</div>';
  o += '</div>';

  // 4. RUNTIME + SCALING
  if (d.runtime || d.scaling) {
    o += '<div class="detail-grid">';
    if (d.runtime) {
      o += '<div class="card"><div class="section-label">Runtime</div><table>';
      if (d.runtime.workload) o += '<tr><td class="text-dim" style="width:160px">Workload</td><td><span class="badge badge-info">' + h(d.runtime.workload) + '</span></td></tr>';
      if (d.runtime.stateType) o += '<tr><td class="text-dim">State</td><td>' + h(d.runtime.stateType) + '</td></tr>';
      if (d.runtime.dataCriticality) o += '<tr><td class="text-dim">Data Criticality</td><td><span class="pill ' + (d.runtime.dataCriticality === 'critical' ? 'pill-critical' : d.runtime.dataCriticality === 'high' ? 'pill-warning' : 'pill-dim') + '">' + h(d.runtime.dataCriticality) + '</span></td></tr>';
      if (d.runtime.upgradeStrategy) o += '<tr><td class="text-dim">Upgrade Strategy</td><td>' + h(d.runtime.upgradeStrategy) + '</td></tr>';
      if (d.runtime.healthInterface) {
        o += '<tr><td class="text-dim">Health Check</td><td><code>' + h(d.runtime.healthInterface) + '</code>';
        if (d.runtime.healthPath) o += ' <span class="text-dim">' + h(d.runtime.healthPath) + '</span>';
        o += probeInlineBadge(d.endpoints, 'health', d.runtime.healthInterface);
        o += '</td></tr>';
      }
      if (d.runtime.metricsInterface) {
        o += '<tr><td class="text-dim">Metrics</td><td><code>' + h(d.runtime.metricsInterface) + '</code>';
        if (d.runtime.metricsPath) o += ' <span class="text-dim">' + h(d.runtime.metricsPath) + '</span>';
        o += probeInlineBadge(d.endpoints, 'metrics', d.runtime.metricsInterface);
        o += '</td></tr>';
      }
      o += '</table></div>';
    }
    if (d.scaling) {
      o += '<div class="card"><div class="section-label">Scaling</div><table>';
      if (d.scaling.replicas != null) o += '<tr><td class="text-dim" style="width:160px">Replicas</td><td><code>' + d.scaling.replicas + '</code></td></tr>';
      if (d.scaling.min != null) o += '<tr><td class="text-dim">Min</td><td><code>' + d.scaling.min + '</code></td></tr>';
      if (d.scaling.max != null) o += '<tr><td class="text-dim">Max</td><td><code>' + d.scaling.max + '</code></td></tr>';
      o += '</table></div>';
    }
    o += '</div>';
  }

  // Validation issues
  if (d.validation) {
    var allIssues = (d.validation.errors || []).concat(d.validation.warnings || []);
    if (allIssues.length) {
      o += '<div class="card"><div class="section-label">Validation Issues</div><div class="table-wrapper"><table><thead><tr><th>Severity</th><th>Code</th><th>Path</th><th>Message</th></tr></thead><tbody>';
      for (var i = 0; i < (d.validation.errors || []).length; i++) {
        var e = d.validation.errors[i];
        o += '<tr><td><span class="badge badge-critical">error</span></td><td><code>' + h(e.code) + '</code></td><td><code>' + h(e.path) + '</code></td><td>' + h(e.message) + '</td></tr>';
      }
      for (var i = 0; i < (d.validation.warnings || []).length; i++) {
        var w = d.validation.warnings[i];
        o += '<tr><td><span class="badge badge-warning">warning</span></td><td><code>' + h(w.code) + '</code></td><td><code>' + h(w.path) + '</code></td><td>' + h(w.message) + '</td></tr>';
      }
      o += '</tbody></table></div></div>';
    }
  }

  return o;
}

/* ─── Tab: Validations (conditions grouped by category + validation issues) ─── */
var validationCatalog = {
  ContractValid:        { category: 'contract', label: 'Contract Structure', severity: 'error' },
  ServiceExists:        { category: 'infrastructure', label: 'Service Exists', severity: 'error' },
  WorkloadExists:       { category: 'infrastructure', label: 'Workload Exists', severity: 'error' },
  PortsValid:           { category: 'networking', label: 'Port Alignment', severity: 'error' },
  HealthEndpointValid:  { category: 'networking', label: 'Health Endpoint', severity: 'error' },
  MetricsEndpointValid: { category: 'networking', label: 'Metrics Endpoint', severity: 'error' },
  WorkloadTypeMatch:    { category: 'workload', label: 'Workload Type', severity: 'error' },
  StateModelMatch:      { category: 'state', label: 'State Model', severity: 'error' },
  UpgradeStrategyMatch: { category: 'lifecycle', label: 'Upgrade Strategy', severity: 'warning' },
  GracefulShutdownMatch:{ category: 'lifecycle', label: 'Graceful Shutdown', severity: 'warning' },
  ImageMatch:           { category: 'image', label: 'Container Image', severity: 'error' },
  HealthTimingMatch:    { category: 'health', label: 'Health Probe Timing', severity: 'warning' }
};

function lookupValidation(type) {
  return validationCatalog[type] || { category: 'other', label: type, severity: 'error' };
}

function renderTabValidations(d) {
  var conditions = d.conditions || [];
  var validation = d.validation;
  var o = '';

  // Compliance summary at top
  if (d.compliance) {
    o += '<div class="compliance-summary-card">';
    o += '<div class="compliance-summary-header">';
    o += '<span class="compliance-summary-status">' + complianceBadge(d.compliance.status) + '</span>';
    if (d.compliance.score != null) o += complianceScoreBadge(d.compliance.score, d.compliance.status);
    o += '</div>';
    if (d.compliance.summary) {
      var s = d.compliance.summary;
      o += '<div class="compliance-summary-counts">';
      o += '<span>' + s.total + ' checks</span>';
      o += '<span class="text-dim">\u2022</span>';
      o += '<span style="color:var(--ok)">' + s.passed + ' passed</span>';
      if (s.errors > 0) o += '<span style="color:var(--critical)">' + s.errors + ' errors</span>';
      if (s.warnings > 0) o += '<span style="color:var(--warning)">' + s.warnings + ' warnings</span>';
      o += '</div>';
    }
    o += '</div>';
  }

  // Group conditions by category
  if (conditions.length) {
    var groups = {};
    for (var i = 0; i < conditions.length; i++) {
      var c = conditions[i];
      var entry = lookupValidation(c.type);
      var cat = entry.category;
      if (!groups[cat]) groups[cat] = [];
      groups[cat].push({ condition: c, entry: entry });
    }
    var catOrder = ['contract', 'infrastructure', 'networking', 'workload', 'state', 'lifecycle', 'image', 'health', 'other'];
    for (var ci = 0; ci < catOrder.length; ci++) {
      var cat = catOrder[ci];
      var items = groups[cat];
      if (!items) continue;
      o += '<div class="card"><div class="section-label" style="text-transform:capitalize">' + h(cat) + '</div>';
      o += '<div class="conditions-grid">';
      for (var j = 0; j < items.length; j++) {
        var item = items[j];
        var c = item.condition;
        var sev = c.severity || item.entry.severity;
        o += '<div class="condition-card">';
        o += '<div class="condition-type">' + condBadge(c.status) + ' ' + h(item.entry.label);
        if (sev === 'warning') o += ' <span class="pill pill-warning" style="font-size:9px;padding:1px 5px">warn</span>';
        o += '</div>';
        if (c.reason || c.lastTransitionAgo) {
          o += '<div class="condition-message" style="font-weight:500">';
          if (c.reason) o += h(c.reason);
          if (c.lastTransitionAgo) o += (c.reason ? ' \u00B7 ' : '') + h(c.lastTransitionAgo);
          o += '</div>';
        }
        if (c.message) o += '<div class="condition-message">' + h(c.message) + '</div>';
        o += '</div>';
      }
      o += '</div></div>';
    }
  }

  // Validation issues (from contract validation, not k8s conditions)
  if (validation) {
    var errs = validation.errors || [];
    var warns = validation.warnings || [];
    if (errs.length || warns.length) {
      o += '<div class="card"><div class="section-label">Contract Validation Issues</div>';
      o += '<div class="table-wrapper"><table><thead><tr><th>Severity</th><th>Code</th><th>Path</th><th>Message</th></tr></thead><tbody>';
      for (var i = 0; i < errs.length; i++) {
        var e = errs[i];
        o += '<tr><td><span class="badge badge-critical">error</span></td><td><code>' + h(e.code) + '</code></td><td><code>' + h(e.path) + '</code></td><td>' + h(e.message) + '</td></tr>';
      }
      for (var i = 0; i < warns.length; i++) {
        var w = warns[i];
        o += '<tr><td><span class="badge badge-warning">warning</span></td><td><code>' + h(w.code) + '</code></td><td><code>' + h(w.path) + '</code></td><td>' + h(w.message) + '</td></tr>';
      }
      o += '</tbody></table></div></div>';
    }
  }

  if (!o) o = '<div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No validation data available</div></div>';
  return o;
}

/* ─── Tab: Contract vs Runtime ─── */
function renderTabRuntimeDiff(d) {
  var rows = d.runtimeDiff || [];
  if (!rows.length) return '<div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No contract vs runtime comparison available</div></div>';

  var o = '<div class="card"><div class="card-header"><div class="section-label">Contract vs Runtime</div>';
  var matches = 0, mismatches = 0, skipped = 0;
  for (var i = 0; i < rows.length; i++) {
    if (rows[i].status === 'match') matches++;
    else if (rows[i].status === 'mismatch') mismatches++;
    else skipped++;
  }
  o += '<div>';
  if (matches) o += '<span class="pill pill-ok">' + matches + ' match</span> ';
  if (mismatches) o += '<span class="pill pill-critical">' + mismatches + ' mismatch</span> ';
  if (skipped) o += '<span class="pill pill-dim">' + skipped + ' skipped</span>';
  o += '</div></div>';

  o += '<div class="table-wrapper"><table><thead><tr>';
  o += '<th>Field</th><th>Contract Path</th><th>Declared</th><th>Observed</th><th>Status</th>';
  o += '</tr></thead><tbody>';
  for (var i = 0; i < rows.length; i++) {
    var r = rows[i];
    var statusBadge = r.status === 'match' ? '<span class="badge badge-ok">match</span>'
      : r.status === 'mismatch' ? '<span class="badge badge-critical">mismatch</span>'
      : '<span class="badge badge-neutral">' + h(r.status) + '</span>';
    o += '<tr>';
    o += '<td><strong>' + h(r.field) + '</strong></td>';
    o += '<td><code class="text-dim">' + h(r.contractPath || '') + '</code></td>';
    o += '<td>' + (r.declaredValue ? '<code>' + h(r.declaredValue) + '</code>' : '<span class="text-dim">\u2014</span>') + '</td>';
    o += '<td>' + (r.observedValue ? '<code>' + h(r.observedValue) + '</code>' : '<span class="text-dim">\u2014</span>') + '</td>';
    o += '<td>' + statusBadge + '</td>';
    o += '</tr>';
  }
  o += '</tbody></table></div></div>';
  return o;
}

/* ─── Tab: Observed Runtime ─── */
function renderTabObservedRuntime(d) {
  var obs = d.observedRuntime;
  if (!obs) return '<div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No observed runtime data available. This data is populated by the Kubernetes operator.</div></div>';

  var o = '<div class="card"><div class="section-label">Observed Runtime State</div>';
  o += '<table>';
  if (obs.workloadKind) o += '<tr><td class="text-dim" style="width:200px">Workload Kind</td><td><span class="badge badge-info">' + h(obs.workloadKind) + '</span></td></tr>';
  if (obs.deploymentStrategy) o += '<tr><td class="text-dim">Deployment Strategy</td><td>' + h(obs.deploymentStrategy) + '</td></tr>';
  if (obs.podManagementPolicy) o += '<tr><td class="text-dim">Pod Management Policy</td><td>' + h(obs.podManagementPolicy) + '</td></tr>';
  if (obs.terminationGracePeriodSeconds != null) o += '<tr><td class="text-dim">Termination Grace Period</td><td><code>' + obs.terminationGracePeriodSeconds + 's</code></td></tr>';
  if (obs.containerImages && obs.containerImages.length) {
    o += '<tr><td class="text-dim">Container Images</td><td>';
    for (var i = 0; i < obs.containerImages.length; i++) {
      o += '<code style="display:block;margin-bottom:2px">' + h(obs.containerImages[i]) + '</code>';
    }
    o += '</td></tr>';
  }
  if (obs.hasPVC != null) o += '<tr><td class="text-dim">Has PVC</td><td>' + (obs.hasPVC ? '<span class="badge badge-info">yes</span>' : '<span class="badge badge-neutral">no</span>') + '</td></tr>';
  if (obs.hasEmptyDir != null) o += '<tr><td class="text-dim">Has EmptyDir</td><td>' + (obs.hasEmptyDir ? '<span class="badge badge-info">yes</span>' : '<span class="badge badge-neutral">no</span>') + '</td></tr>';
  if (obs.healthProbeInitialDelaySeconds != null) o += '<tr><td class="text-dim">Health Probe Initial Delay</td><td><code>' + obs.healthProbeInitialDelaySeconds + 's</code></td></tr>';
  o += '</table></div>';
  return o;
}

/* ─── Tab: Dependencies (matches operator partial-tab-dependencies.html) ─── */
function renderTabDependencies(d) {
  var deps = d.dependencies || [];
  var dependents = state.dependents || [];

  var o = '';

  // Service dependency graph — shows full chain of deps + dependents
  o += '<div class="card" style="padding:0;overflow:hidden"><div class="card-header" style="padding:20px 20px 0"><div class="section-label">Dependency Graph</div></div>';
  o += '<div id="service-graph-container" style="width:100%;height:400px;position:relative"></div></div>';


  // Depends On section — with clickable refs
  o += '<div class="card"><div class="card-header"><div class="section-label">Depends On</div>';
  if (deps.length) o += '<span class="text-dim">' + deps.length + ' dependenc' + (deps.length > 1 ? 'ies' : 'y') + '</span>';
  o += '</div>';

  if (deps.length) {
    o += '<div class="table-wrapper"><table><thead><tr><th>Service</th><th>Required</th><th class="hide-narrow">Compatibility</th><th>Status</th></tr></thead><tbody>';
    for (var i = 0; i < deps.length; i++) {
      var dep = deps[i];
      var depName = dep.name || extractServiceName(dep.ref);
      var exists = serviceExists(depName);
      o += '<tr>';
      // Always clickable — navigates to detail; passes OCI ref for lazy resolution of external deps
      if (exists) {
        o += '<td><a class="dep-link" onclick="navigateTo(\'detail\',\'' + ha(depName) + '\')">' + h(depName) + '</a>';
      } else {
        o += '<td><a class="dep-link" onclick="navigateTo(\'detail\',\'' + ha(depName) + '\',\'' + ha(dep.ref) + '\',\'' + ha(dep.compatibility || '') + '\')">' + h(depName) + '</a>';
        o += ' <span class="badge badge-neutral">external</span>';
      }
      if (dep.ref !== depName) o += '<br><code class="text-dim" style="font-size:var(--text-xs)">' + h(dep.ref) + '</code>';
      o += '</td>';
      o += '<td>' + (dep.required ? '<span class="badge badge-info">required</span>' : '<span class="badge badge-neutral">optional</span>') + '</td>';
      o += '<td class="hide-narrow"><span class="text-dim">' + h(dep.compatibility || '\u2014') + '</span></td>';
      // Status: resolved/external + warning if required dep is invalid
      if (exists) {
        var depSvc = state.services.find(function(sv) { return sv.name === depName; });
        var depInvalid = depSvc && (depSvc.phase === 'Invalid' || depSvc.phase === 'Degraded');
        if (dep.required && depInvalid) {
          o += '<td><span class="badge badge-warning">resolved</span> <span class="text-dim" style="color:var(--warning);font-size:11px">Required dependency is ' + h(depSvc.phase).toLowerCase() + '</span></td>';
        } else {
          o += '<td><span class="badge badge-ok">resolved</span></td>';
        }
      } else {
        o += '<td><span class="badge badge-neutral">external</span></td>';
      }
      o += '</tr>';
    }
    o += '</tbody></table></div>';
  } else {
    o += '<div style="color:var(--text-dim);font-size:var(--text-sm)">No dependencies declared</div>';
  }
  o += '</div>';

  // Dependents section — services that depend on this one
  // Compute transitive dependents count from blast radius in list data
  var blastRadius = 0;
  var thisSvc = state.services.find(function(sv) { return sv.name === d.name; });
  if (thisSvc) blastRadius = thisSvc.blastRadius || 0;

  o += '<div class="card"><div class="card-header"><div class="section-label">Dependents</div><div>';
  if (dependents.length) {
    o += '<span class="pill pill-accent">' + dependents.length + ' service' + (dependents.length > 1 ? 's' : '') + ' depend on this</span>';
  }
  if (blastRadius > dependents.length) {
    o += ' <span class="blast-radius">' + blastRadius + ' total affected</span>';
  }
  o += '</div></div>';

  if (dependents.length) {
    o += '<div class="table-wrapper"><table><thead><tr><th>Service</th><th>Status</th><th>Required</th></tr></thead><tbody>';
    for (var i = 0; i < dependents.length; i++) {
      var dep = dependents[i];
      var exists = serviceExists(dep.name);
      o += '<tr>';
      if (exists) {
        o += '<td><a class="dep-link" onclick="navigateTo(\'detail\',\'' + ha(dep.name) + '\')">' + h(dep.name) + '</a></td>';
      } else {
        o += '<td>' + h(dep.name) + '</td>';
      }
      o += '<td>' + phaseBadge(dep.phase) + '</td>';
      o += '<td>' + (dep.required ? '<span class="badge badge-info">required</span>' : '<span class="badge badge-neutral">optional</span>') + '</td>';
      o += '</tr>';
    }
    o += '</tbody></table></div>';
  } else {
    o += '<div style="color:var(--text-dim);font-size:var(--text-sm)">No services depend on this one</div>';
  }
  o += '</div>';

  // Cross-references from server (config/policy refs)
  var crossRefs = state.crossRefs || { references: [], referencedBy: [] };
  var refs = crossRefs.references || [];
  var referencedBy = crossRefs.referencedBy || [];

  if (refs.length) {
    o += '<div class="card"><div class="card-header"><div class="section-label">References</div>';
    o += '<span class="text-dim">' + refs.length + ' reference' + (refs.length > 1 ? 's' : '') + '</span></div>';
    o += '<div class="table-wrapper"><table><thead><tr><th>Contract</th><th>Type</th><th>Status</th><th class="hide-narrow">Reference</th></tr></thead><tbody>';
    for (var i = 0; i < refs.length; i++) {
      var ref = refs[i];
      var refExists = serviceExists(ref.name);
      o += '<tr>';
      o += '<td><a class="dep-link" onclick="navigateTo(\'detail\',\'' + ha(ref.name) + '\')">' + h(ref.name) + '</a>';
      if (!refExists) o += ' <span class="badge badge-neutral">external</span>';
      o += '</td>';
      o += '<td><span class="pill pill-dim">' + h(ref.refType) + '</span></td>';
      if (refExists && ref.phase) {
        o += '<td>' + phaseBadge(ref.phase) + '</td>';
      } else {
        o += '<td><span class="badge badge-neutral">untracked</span></td>';
      }
      o += '<td class="hide-narrow">' + (ref.ref ? '<code>' + h(ref.ref) + '</code>' : '\u2014') + '</td>';
      o += '</tr>';
    }
    o += '</tbody></table></div></div>';
  }

  if (referencedBy.length) {
    o += '<div class="card"><div class="card-header"><div class="section-label">Referenced By</div>';
    o += '<span class="text-dim">' + referencedBy.length + ' service' + (referencedBy.length > 1 ? 's' : '') + '</span></div>';
    o += '<div class="table-wrapper"><table><thead><tr><th>Service</th><th>Uses</th><th>Status</th></tr></thead><tbody>';
    for (var i = 0; i < referencedBy.length; i++) {
      var rb = referencedBy[i];
      o += '<tr>';
      o += '<td><a class="dep-link" onclick="navigateTo(\'detail\',\'' + ha(rb.name) + '\')">' + h(rb.name) + '</a></td>';
      o += '<td><span class="pill pill-dim">' + h(rb.refType) + '</span></td>';
      o += '<td>' + phaseBadge(rb.phase) + '</td>';
      o += '</tr>';
    }
    o += '</tbody></table></div></div>';
  }

  return o;
}

/* ─── Tab: Interfaces (matches operator partial-tab-interfaces.html) ─── */
function renderTabInterfaces(d) {
  /* ── Port of operator v8 partial-tab-interfaces.html ── */
  var ifaces = d.interfaces || [];
  if (!ifaces.length) return '<div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No interfaces declared in contract</div></div>';

  // Declared Interfaces summary table (v8: top-level table)
  var o = '<div class="card"><div class="section-label">Declared Interfaces</div>';
  o += '<div class="table-wrapper"><table><thead><tr>';
  o += '<th>Name</th><th>Type</th><th>Port</th><th>Visibility</th><th class="hide-narrow">Contract File</th>';
  o += '</tr></thead><tbody>';
  for (var i = 0; i < ifaces.length; i++) {
    var f = ifaces[i];
    o += '<tr>';
    o += '<td><strong>' + h(f.name) + '</strong></td>';
    o += '<td><span class="badge badge-info">' + h(f.type || 'http') + '</span></td>';
    o += '<td><code>' + (f.port != null ? f.port : '-') + '</code></td>';
    o += '<td>';
    if (f.visibility) {
      o += '<span class="pill ' + (f.visibility === 'public' ? 'pill-warning' : 'pill-dim') + '">' + h(f.visibility) + '</span>';
    } else {
      o += '<span class="text-dim">-</span>';
    }
    o += '</td>';
    o += '<td class="hide-narrow">';
    if (f.contractFile) { o += '<code>' + h(f.contractFile) + '</code>'; }
    else { o += '<span class="text-dim">-</span>'; }
    o += '</td>';
    o += '</tr>';
  }
  o += '</tbody></table></div></div>';

  // Per-interface detail cards (v8: one card per interface)
  for (var i = 0; i < ifaces.length; i++) {
    var f = ifaces[i];
    o += '<div class="card">';
    o += '<div class="card-header"><div class="section-label">' + h(f.name) + '</div><div>';
    o += '<span class="badge badge-info">' + h(f.type || 'http') + '</span>';
    if (f.visibility) o += '<span class="pill ' + (f.visibility === 'public' ? 'pill-warning' : 'pill-dim') + '" style="margin-left:6px">' + h(f.visibility) + '</span>';
    o += '</div></div>';
    o += '<table>';
    o += '<tr><td class="text-dim" style="width:120px">Port</td><td><code>' + (f.port != null ? f.port : '-') + '</code></td></tr>';
    if (f.contractFile) {
      o += '<tr><td class="text-dim">Contract File</td><td><code>' + h(f.contractFile) + '</code></td></tr>';
    }
    o += '</table>';

    // v8: Endpoints table (Method / Path / Summary) from parsed OpenAPI
    if (f.endpoints && f.endpoints.length) {
      o += '<div style="margin-top:8px;border-top:1px solid var(--border);padding:8px 0 0">';
      o += '<div class="text-dim" style="font-size:var(--text-xs);padding:0 12px 4px">' + h(f.contractFile || '') + ' \u2014 ' + f.endpoints.length + ' endpoint' + (f.endpoints.length !== 1 ? 's' : '') + '</div>';
      o += '<div class="table-wrapper"><table><thead><tr>';
      o += '<th style="width:80px">Method</th><th>Path</th><th class="hide-narrow">Summary</th>';
      o += '</tr></thead><tbody>';
      for (var j = 0; j < f.endpoints.length; j++) {
        var ep = f.endpoints[j];
        var meth = (ep.method || '').toUpperCase();
        var methodClass = 'badge-neutral';
        if (meth === 'GET') methodClass = 'badge-ok';
        else if (meth === 'POST') methodClass = 'badge-info';
        else if (meth === 'DELETE') methodClass = 'badge-critical';
        else if (meth === 'PUT' || meth === 'PATCH') methodClass = 'badge-warning';
        o += '<tr>';
        o += '<td><span class="badge ' + methodClass + '" style="font-size:10px;font-family:var(--font-mono)">' + h(meth) + '</span></td>';
        o += '<td><code style="font-size:var(--text-xs)">' + h(ep.path) + '</code></td>';
        o += '<td class="hide-narrow"><span class="text-dim" style="font-size:var(--text-xs)">' + h(ep.summary || '') + '</span></td>';
        o += '</tr>';
      }
      o += '</tbody></table></div></div>';
    } else if (f.contractContent) {
      // v8: raw contract content fallback
      o += '<div style="margin-top:8px;border-top:1px solid var(--border)">';
      o += '<div class="text-dim" style="font-size:var(--text-xs);padding:8px 12px 4px">' + h(f.contractFile || '') + '</div>';
      o += '<pre style="margin:0;padding:4px 12px 12px;font-size:var(--text-xs);overflow-x:auto;max-height:400px;overflow-y:auto">' + h(f.contractContent) + '</pre>';
      o += '</div>';
    }

    o += '</div>';
  }

  return o;
}

/* ─── Tab: Config (port of operator v8 partial-tab-config.html) ─── */
function renderTabConfig(d) {
  var cfg = d.configuration;
  if (!cfg) return '<div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No configuration declared in contract</div></div>';

  var o = '<div class="card"><div class="section-label">Configuration</div>';

  // v8: Schema line
  if (cfg.schema) {
    o += '<div style="margin-bottom:16px"><span class="text-dim">Schema:</span> <code>' + h(cfg.schema) + '</code></div>';
  }

  // v8: Ref line
  if (cfg.ref) {
    o += '<div style="margin-bottom:16px"><span class="text-dim">Ref:</span> ' + refLink(cfg.ref) + '</div>';
  }

  // v8: Values table (Key / Value / Type) — use flattened values if available
  var vals = cfg.values;
  if (vals && vals.length) {
    o += '<div class="table-wrapper"><table><thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead><tbody>';
    for (var i = 0; i < vals.length; i++) {
      var v = vals[i];
      o += '<tr>';
      o += '<td><code>' + h(v.key) + '</code></td>';
      o += '<td>';
      if (v.value === '(any)') { o += '<span class="text-dim">any</span>'; }
      else { o += '<code>' + h(v.value) + '</code>'; }
      o += '</td>';
      o += '<td><span class="pill pill-dim">' + h(v.type) + '</span></td>';
      o += '</tr>';
    }
    o += '</tbody></table></div>';
  } else if (cfg.valueKeys && cfg.valueKeys.length) {
    // Fallback for k8s source: only key names available, no values
    o += '<div class="table-wrapper"><table><thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead><tbody>';
    for (var i = 0; i < cfg.valueKeys.length; i++) {
      o += '<tr><td><code>' + h(cfg.valueKeys[i]) + '</code></td><td><span class="text-dim">-</span></td><td><span class="pill pill-dim">value</span></td></tr>';
    }
    o += '</tbody></table></div>';
  }

  // Secret keys (k8s source provides these separately)
  if (cfg.secretKeys && cfg.secretKeys.length) {
    if (vals && vals.length) {
      // If we already rendered a values table, close and start a new section
      o += '<div style="margin-top:16px;border-top:1px solid var(--border);padding-top:12px">';
      o += '<div class="text-dim" style="font-size:var(--text-xs);margin-bottom:8px">Secret Keys</div>';
    }
    o += '<div class="table-wrapper"><table><thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead><tbody>';
    for (var i = 0; i < cfg.secretKeys.length; i++) {
      o += '<tr><td><code>' + h(cfg.secretKeys[i]) + '</code></td><td><span class="text-dim">\u2022\u2022\u2022\u2022\u2022\u2022</span></td><td><span class="pill pill-warning">secret</span></td></tr>';
    }
    o += '</tbody></table></div>';
    if (vals && vals.length) o += '</div>';
  }

  // v8: empty state
  if (!vals && (!cfg.valueKeys || !cfg.valueKeys.length) && !cfg.schema && !cfg.ref && (!cfg.secretKeys || !cfg.secretKeys.length)) {
    o += '<div style="color:var(--text-dim);font-size:var(--text-sm)">Configuration section is empty</div>';
  }

  o += '</div>';
  return o;
}

/* ─── Tab: Policy (port of operator v8 partial-tab-policy.html) ─── */
function renderTabPolicy(d) {
  var pol = d.policy;
  if (!pol) return '<div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No policy declared in contract</div></div>';

  var o = '<div class="card"><div class="section-label">Policy</div>';

  // v8: Schema + Reference table
  o += '<table>';
  if (pol.schema) {
    o += '<tr><td class="text-dim" style="width:160px">Schema</td><td><span class="badge badge-info">' + h(pol.schema) + '</span></td></tr>';
  }
  if (pol.ref) {
    o += '<tr><td class="text-dim">Reference</td><td>' + refLink(pol.ref);
    if (pol.content) o += ' <span class="badge badge-info" style="margin-left:4px">in-bundle</span>';
    o += '</td></tr>';
  }
  o += '</table>';

  // v8: Values table (Key / Value / Type) — parsed from policy file
  if (pol.values && pol.values.length) {
    o += '<div class="table-wrapper" style="margin-top:16px"><table><thead><tr><th>Key</th><th>Value</th><th>Type</th></tr></thead><tbody>';
    for (var i = 0; i < pol.values.length; i++) {
      var v = pol.values[i];
      o += '<tr>';
      o += '<td><code>' + h(v.key) + '</code></td>';
      o += '<td>';
      if (v.value === '(any)') { o += '<span class="text-dim">any</span>'; }
      else { o += '<code>' + h(v.value) + '</code>'; }
      o += '</td>';
      o += '<td><span class="pill pill-dim">' + h(v.type) + '</span></td>';
      o += '</tr>';
    }
    o += '</tbody></table></div>';
  } else if (pol.content) {
    // v8: raw content fallback
    o += '<div style="margin-top:8px;border-top:1px solid var(--border)">';
    if (pol.ref) o += '<div class="text-dim" style="font-size:var(--text-xs);padding:8px 12px 4px">' + h(pol.ref) + '</div>';
    o += '<pre style="margin:0;padding:4px 12px 12px;font-size:var(--text-xs);overflow-x:auto;max-height:500px;overflow-y:auto">' + h(pol.content) + '</pre>';
    o += '</div>';
  }

  o += '</div>';
  return o;
}

/* ─── Tab: History (matches operator partial-tab-history.html) ─── */
function renderTabHistory(versions) {
  if (!versions.length) return '<div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No revision history available</div></div>';

  var canDiff = versions.length > 1;
  // Detect OCI repo from version refs for "Fetch all versions" button
  var ociRepo = '';
  for (var vi = 0; vi < versions.length; vi++) {
    if (versions[vi].ref) { var idx = versions[vi].ref.lastIndexOf(':'); if (idx > 0) ociRepo = versions[vi].ref.substring(0, idx); break; }
  }
  var o = '<div class="card"><div class="card-header"><div class="section-label">Version History</div><span class="text-dim">' + versions.length + ' revision' + (versions.length !== 1 ? 's' : '') + '</span>';
  if (ociRepo) o += ' <button class="filter-clear" style="font-size:11px;margin-left:8px" id="fetch-versions-btn" onclick="fetchAllVersions(\'' + ha(ociRepo) + '\')">Fetch all versions</button>';
  o += '</div>';
  o += '<div class="table-wrapper"><table><thead><tr><th>Version</th><th>Source</th><th>Hash</th><th>Created</th><th>Changes</th><th>Status</th><th></th></tr></thead><tbody>';
  for (var i = 0; i < versions.length; i++) {
    var v = versions[i];
    var isCurrent = v.version === (state.details[state.service] || {}).version;
    o += '<tr>';
    o += '<td><strong>' + h(v.version) + '</strong></td>';
    o += '<td><span class="text-dim">' + h(v.ref || '\u2014') + '</span></td>';
    o += '<td><code>' + h(v.contractHash ? v.contractHash.substring(0, 12) : '\u2014') + '</code></td>';
    o += '<td><span class="text-dim">' + (v.createdAt ? new Date(v.createdAt).toLocaleDateString() : '\u2014') + '</span></td>';
    o += '<td>' + classificationBadge(v.classification) + '</td>';
    o += '<td>' + (isCurrent ? '<span class="badge badge-ok">current</span>' : '<span class="badge badge-neutral">' + h(v.version) + '</span>') + '</td>';
    o += '<td>';
    if (canDiff && !isCurrent) {
      o += '<button class="filter-clear" style="font-size:11px" onclick="compareVersion(\'' + ha(v.version) + '\')">Compare with current</button>';
    }
    o += '</td>';
    o += '</tr>';
  }
  o += '</tbody></table></div></div>';
  return o;
}

function compareVersion(version) {
  var versions = state.versions[state.service] || [];
  if (versions.length < 2) return;
  // Switch to diff tab with this version as "from" and current (first) as "to"
  state.tab = 'diff';
  var d = state.details[state.service];
  var agg = state.aggregated[state.service];
  document.getElementById('tab-content').innerHTML = renderCurrentTab(d, versions, agg);
  document.querySelectorAll('.tab-btn').forEach(function(btn) {
    btn.classList.toggle('tab-active', btn.getAttribute('data-tab') === 'diff');
  });
  // Prefill selectors
  setTimeout(function() {
    var fromSel = document.getElementById('diff-from');
    var toSel = document.getElementById('diff-to');
    if (fromSel && toSel) {
      fromSel.value = version;
      toSel.value = versions[0].version;
      runDiff(state.service);
    }
  }, 50);
}

async function fetchAllVersions(ociRepo) {
  var btn = document.getElementById('fetch-versions-btn');
  if (btn) { btn.disabled = true; btn.textContent = 'Fetching\u2026'; }
  try {
    await api.listRemoteVersions(ociRepo, true);
    // Re-fetch versions from the API so enriched data (hash, classification)
    // from the now-populated cache source flows through.
    var versions = await api.getVersions(state.service);
    state.versions[state.service] = versions;
    document.getElementById('tab-content').innerHTML = renderTabHistory(versions);
  } catch (e) {
    if (btn) { btn.textContent = 'Error: ' + (e.message || 'failed'); btn.style.color = 'var(--danger)'; }
  }
}

/* ─── Tab: Diff (matches operator partial-tab-diff.html) ─── */
function renderTabDiff(versions) {
  if (versions.length < 2) return '<div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">At least two revisions are needed to compare versions.</div></div>';

  var opts = versions.map(function(v) { return '<option value="' + ha(v.version) + '">' + h(v.version) + '</option>'; }).join('');
  var svcName = ha(state.service);

  var o = '<div class="card"><div class="card-header"><div class="section-label">Compare Revisions</div><span class="text-dim">' + versions.length + ' revisions available</span></div>';
  o += '<div class="selector-form" style="margin-bottom:0"><div><label>From</label><select id="diff-from">' + opts + '</select></div>';
  o += '<div><label>To</label><select id="diff-to">' + opts + '</select></div>';
  o += '<div><label>&nbsp;</label><button type="button" onclick="runDiff(\'' + svcName + '\')">Compare</button></div></div></div>';
  o += '<div id="diff-result" style="margin-top:16px"></div>';
  return o;
}

async function runDiff(name) {
  var from = document.getElementById('diff-from').value;
  var to = document.getElementById('diff-to').value;
  var el = document.getElementById('diff-result');
  if (!from || !to) return;

  el.innerHTML = '<div class="loading"><div class="spinner"></div>Comparing...</div>';
  try {
    var r = await api.getDiff({ name: name, version: from }, { name: name, version: to });
    var o = '';
    if (r.classification) {
      o += '<div class="classification-banner classification-' + h(r.classification) + '">' + h(r.classification.replace(/_/g, ' ')) + '</div>';
    }
    if (r.changes && r.changes.length) {
      o += '<div class="card"><div class="table-wrapper"><table class="diff-table"><colgroup><col style="width:25%"><col style="width:12%"><col style="width:20%"><col style="width:20%"><col style="width:23%"></colgroup><thead><tr><th>Path</th><th>Type</th><th>Old</th><th>New</th><th class="hide-narrow">Reason</th></tr></thead><tbody>';
      for (var i = 0; i < r.changes.length; i++) {
        var c = r.changes[i];
        o += '<tr><td><code>' + h(c.path) + '</code></td>';
        o += '<td><span class="badge ' + (c.classification === 'BREAKING' ? 'badge-critical' : c.classification === 'NON_BREAKING' ? 'badge-ok' : 'badge-warning') + '">' + h(c.type) + '</span></td>';
        o += '<td>' + (c.oldValue != null ? '<span class="diff-old">' + h(String(c.oldValue)) + '</span>' : '<span class="text-dim">\u2014</span>') + '</td>';
        o += '<td>' + (c.newValue != null ? '<span class="diff-new">' + h(String(c.newValue)) + '</span>' : '<span class="text-dim">\u2014</span>') + '</td>';
        o += '<td class="hide-narrow"><span class="diff-reason">' + h(c.reason || '') + '</span></td></tr>';
      }
      o += '</tbody></table></div></div>';
    } else {
      o += '<div style="color:var(--text-dim);font-size:var(--text-sm);padding:16px">No changes detected between these versions.</div>';
    }
    el.innerHTML = o;
  } catch (e) {
    el.innerHTML = '<div style="background:var(--critical-bg);color:var(--critical);padding:14px 18px;border-radius:var(--radius-sm);border:1px solid var(--critical-border);font-size:var(--text-sm)">' + h(e.message) + '</div>';
  }
}

/* ─── Tab: Sources (multi-source per-source breakdown) ─── */
function renderTabSources(agg) {
  if (!agg || !agg.sources || !agg.sources.length) return '<div class="card"><div style="color:var(--text-dim);font-size:var(--text-sm);text-align:center;padding:24px">No source data</div></div>';

  var first = agg.sources[0].sourceType;
  var o = '<div class="source-tab-bar">';
  for (var i = 0; i < agg.sources.length; i++) {
    var s = agg.sources[i];
    o += '<button class="source-tab-item' + (s.sourceType === first ? ' active' : '') + '" data-source="' + ha(s.sourceType) + '" onclick="switchSourceTab(\'' + ha(s.sourceType) + '\')">' + sourcePill(s.sourceType) + '</button>';
  }
  o += '</div>';

  for (var i = 0; i < agg.sources.length; i++) {
    var src = agg.sources[i];
    var sd = src.service || {};
    o += '<div class="source-panel' + (src.sourceType !== first ? ' hidden' : '') + '" id="source-panel-' + h(src.sourceType) + '" style="border:1px solid var(--border);border-top:none;border-radius:0 0 var(--radius) var(--radius);padding:20px;background:var(--bg-surface)">';

    o += '<div style="margin-bottom:16px;display:flex;align-items:center;gap:12px">' + sourcePill(src.sourceType);
    if (sd.version) o += '<span class="pill pill-dim">' + h(sd.version) + '</span>';
    if (sd.phase) o += phaseBadge(sd.phase);
    o += '</div>';

    var hasData = false;
    if (sd.runtime) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Runtime</div><table>';
      if (sd.runtime.workload) o += '<tr><td class="text-dim">Workload</td><td>' + h(sd.runtime.workload) + '</td></tr>';
      if (sd.runtime.healthInterface) o += '<tr><td class="text-dim">Health</td><td><code>' + h(sd.runtime.healthInterface) + '</code></td></tr>';
      o += '</table></div>';
    }
    if (sd.interfaces && sd.interfaces.length) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Interfaces</div><table>';
      for (var j = 0; j < sd.interfaces.length; j++) {
        var ifc = sd.interfaces[j];
        o += '<tr><td><strong>' + h(ifc.name) + '</strong></td><td><span class="badge badge-info">' + h(ifc.type || 'http') + '</span></td><td>' + (ifc.visibility ? '<span class="pill pill-dim">' + h(ifc.visibility) + '</span>' : '') + '</td></tr>';
      }
      o += '</table></div>';
    }
    if (sd.dependencies && sd.dependencies.length) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Dependencies</div><table>';
      for (var j = 0; j < sd.dependencies.length; j++) {
        var dep = sd.dependencies[j];
        var depName = dep.name || extractServiceName(dep.ref);
        var exists = serviceExists(depName);
        o += '<tr><td>';
        if (exists) {
          o += '<a class="dep-link" onclick="navigateTo(\'detail\',\'' + ha(depName) + '\')">' + h(depName) + '</a>';
        } else {
          o += '<code>' + h(dep.ref) + '</code>';
        }
        o += '</td><td>' + (dep.required ? '<span class="badge badge-info">required</span>' : 'optional') + '</td></tr>';
      }
      o += '</table></div>';
    }
    if (sd.resources) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Resources</div><table>';
      if (sd.resources.serviceExists != null) o += '<tr><td class="text-dim">Service</td><td>' + (sd.resources.serviceExists ? '<span class="badge badge-ok">found</span>' : '<span class="badge badge-critical">not found</span>') + '</td></tr>';
      if (sd.resources.workloadExists != null) o += '<tr><td class="text-dim">Workload</td><td>' + (sd.resources.workloadExists ? '<span class="badge badge-ok">found</span>' : '<span class="badge badge-critical">not found</span>') + '</td></tr>';
      o += '</table></div>';
    }
    if (sd.scaling) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Scaling</div><table>';
      if (sd.scaling.replicas != null) o += '<tr><td class="text-dim">Replicas</td><td><code>' + sd.scaling.replicas + '</code></td></tr>';
      if (sd.scaling.min != null) o += '<tr><td class="text-dim">Min</td><td><code>' + sd.scaling.min + '</code></td></tr>';
      if (sd.scaling.max != null) o += '<tr><td class="text-dim">Max</td><td><code>' + sd.scaling.max + '</code></td></tr>';
      o += '</table></div>';
    }
    if (sd.validation) {
      var vErrs = (sd.validation.errors || []);
      var vWarns = (sd.validation.warnings || []);
      if (vErrs.length || vWarns.length) {
        hasData = true;
        o += '<div class="card"><div class="section-label">Validation</div>';
        o += '<div style="margin-bottom:8px">' + (sd.validation.valid ? '<span class="badge badge-ok">valid</span>' : '<span class="badge badge-critical">invalid</span>') + '</div>';
        o += '<div class="table-wrapper"><table><thead><tr><th>Severity</th><th>Code</th><th>Path</th><th>Message</th></tr></thead><tbody>';
        for (var j = 0; j < vErrs.length; j++) {
          o += '<tr><td><span class="badge badge-critical">error</span></td><td><code>' + h(vErrs[j].code) + '</code></td><td><code>' + h(vErrs[j].path) + '</code></td><td>' + h(vErrs[j].message) + '</td></tr>';
        }
        for (var j = 0; j < vWarns.length; j++) {
          o += '<tr><td><span class="badge badge-warning">warning</span></td><td><code>' + h(vWarns[j].code) + '</code></td><td><code>' + h(vWarns[j].path) + '</code></td><td>' + h(vWarns[j].message) + '</td></tr>';
        }
        o += '</tbody></table></div></div>';
      }
    }
    if (sd.checksSummary) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Checks Summary</div><table>';
      o += '<tr><td class="text-dim">Total</td><td>' + sd.checksSummary.total + '</td></tr>';
      o += '<tr><td class="text-dim">Passed</td><td><span class="count">' + sd.checksSummary.passed + '</span></td></tr>';
      o += '<tr><td class="text-dim">Failed</td><td><span class="count ' + (sd.checksSummary.failed > 0 ? 'count-error' : 'count-zero') + '">' + sd.checksSummary.failed + '</span></td></tr>';
      o += '</table></div>';
    }
    if (sd.endpoints && sd.endpoints.length) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Runtime Probes</div>';
      o += '<div class="table-wrapper"><table><thead><tr><th>Status</th><th>Probe</th><th>Interface</th><th>URL</th><th>Code</th><th>Latency</th><th>Error</th></tr></thead><tbody>';
      for (var j = 0; j < sd.endpoints.length; j++) {
        var ep = sd.endpoints[j];
        var epSt = ep.healthy === true ? '<span class="badge badge-ok">reachable</span>' : ep.healthy === false ? '<span class="badge badge-critical">failing</span>' : '<span class="badge badge-neutral">unknown</span>';
        var epType = ep.type ? '<span class="pill pill-dim">' + h(ep.type) + '</span>' : '\u2014';
        var epCode = ep.statusCode != null ? '<code>' + ep.statusCode + '</code>' : '\u2014';
        var epLatency = ep.latencyMs != null ? ep.latencyMs + 'ms' : '\u2014';
        var epErr = ep.error || ep.message || '';
        o += '<tr><td>' + epSt + '</td><td>' + epType + '</td><td>' + h(ep.interface) + '</td><td><code>' + h(ep.url || '\u2014') + '</code></td><td>' + epCode + '</td><td>' + epLatency + '</td><td><span class="text-dim">' + h(epErr) + '</span></td></tr>';
      }
      o += '</tbody></table></div></div>';
    }
    if (sd.ports) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Ports</div><table>';
      if (sd.ports.expected && sd.ports.expected.length) o += '<tr><td class="text-dim">Expected</td><td>' + sd.ports.expected.map(function(p) { return '<code>' + p + '</code>'; }).join(', ') + '</td></tr>';
      if (sd.ports.observed && sd.ports.observed.length) o += '<tr><td class="text-dim">Observed</td><td>' + sd.ports.observed.map(function(p) { return '<code>' + p + '</code>'; }).join(', ') + '</td></tr>';
      if (sd.ports.missing && sd.ports.missing.length) o += '<tr><td class="text-dim">Missing</td><td>' + sd.ports.missing.map(function(p) { return '<span class="count count-error"><code>' + p + '</code></span>'; }).join(', ') + '</td></tr>';
      if (sd.ports.unexpected && sd.ports.unexpected.length) o += '<tr><td class="text-dim">Unexpected</td><td>' + sd.ports.unexpected.map(function(p) { return '<span class="count count-warning"><code>' + p + '</code></span>'; }).join(', ') + '</td></tr>';
      o += '</table></div>';
    }
    if (sd.insights && sd.insights.length) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Insights</div>';
      for (var j = 0; j < sd.insights.length; j++) {
        var ins = sd.insights[j];
        o += '<div class="insight-card ' + insightClass(ins.severity) + '">';
        o += '<div class="insight-icon">' + insightIcon(ins.severity) + '</div>';
        o += '<div class="insight-body"><div class="insight-title">' + h(ins.title) + '</div>';
        if (ins.description) o += '<div class="insight-desc">' + h(ins.description) + '</div>';
        o += '</div></div>';
      }
      o += '</div>';
    }
    if (sd.conditions && sd.conditions.length) {
      hasData = true;
      o += '<div class="card"><div class="section-label">Conditions</div><div class="conditions-grid">';
      for (var j = 0; j < sd.conditions.length; j++) {
        var c = sd.conditions[j];
        o += '<div class="condition-card"><div class="condition-type">' + condBadge(c.status) + ' ' + h(c.type) + '</div>';
        if (c.message) o += '<div class="condition-message">' + h(c.message) + '</div>';
        o += '</div>';
      }
      o += '</div></div>';
    }
    if (!hasData) o += '<div style="color:var(--text-dim);font-size:var(--text-sm)">No detailed data from this source.</div>';
    o += '</div>';
  }
  return o;
}

function switchSourceTab(type) {
  document.querySelectorAll('.source-tab-item').forEach(function(el) {
    el.classList.toggle('active', el.getAttribute('data-source') === type);
  });
  document.querySelectorAll('.source-panel').forEach(function(el) {
    el.classList.toggle('hidden', el.id !== 'source-panel-' + type);
  });
}

/* ── Hash-based routing ─── */
function handleHash() {
  var hash = location.hash;
  if (hash === '#graph') {
    state.overviewView = 'graph';
  } else if (hash.startsWith('#service/')) {
    var svc = decodeURIComponent(hash.substring(9));
    state.view = 'detail';
    state.service = svc;
  }
}
handleHash();
window.addEventListener('popstate', function() {
  var hash = location.hash;
  if (hash.startsWith('#service/')) {
    var svc = decodeURIComponent(hash.substring(9));
    if (state.view !== 'detail' || state.service !== svc) navigateTo('detail', svc, null, null, true);
  } else if (hash === '#graph') {
    state.overviewView = 'graph';
    if (state.view !== 'list') {
      navigateTo('list', null, null, null, true);
      // navigateTo sets wantHash to '#' but we want '#graph' — restore it.
      history.replaceState(null, '', '#graph');
    } else if (!graphInitialized) {
      initGraph();
    }
  } else {
    if (state.view !== 'list') navigateTo('list', null, null, null, true);
  }
});

/* ── Init ─── */
navigateTo(state.view === 'detail' ? 'detail' : 'list', state.service);
