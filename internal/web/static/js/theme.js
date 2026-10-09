// runs before first paint so the theme doesnt flash
(function () {
  var t = null;
  try { t = localStorage.getItem('fg-theme'); } catch (e) {}
  if (t !== 'light' && t !== 'dark') t = matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  document.documentElement.dataset.theme = t;
})();
