// Apply the saved theme before first paint. A file rather than an inline script
// so the dashboard's Content-Security-Policy can refuse every inline script.
(function () {
  var t = localStorage.getItem('pacto-theme');
  if (t) document.documentElement.setAttribute('data-theme', t);
  else if (window.matchMedia('(prefers-color-scheme: light)').matches)
    document.documentElement.setAttribute('data-theme', 'light');
})();
