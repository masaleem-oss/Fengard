(function () {
  var $ = function (id) { return document.getElementById(id); };
  var back = $('back');
  if (back) {
    if (history.length < 2) back.textContent = 'Close';
    back.onclick = function () { if (history.length > 1) history.back(); else window.close(); };
  }
  if ($('reload')) $('reload').onclick = function () { location.reload(); };

  var ask = $('ask'), form = $('request');
  if (!ask || !form) return;
  ask.onclick = function () {
    form.hidden = false;
    ask.hidden = true;
    $('note').focus();
  };
  form.onsubmit = function (e) {
    e.preventDefault();
    var send = $('send');
    send.disabled = true;
    send.textContent = 'Sending…';
    $('err').hidden = true;
    fetch('/__fengard/request', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Fengard': '1' },
      body: JSON.stringify({ domain: $('site').textContent, note: $('note').value }),
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (d) {
        if (!r.ok) throw new Error(d.error || 'Something went wrong.');
      });
    }).then(function () {
      form.hidden = true;
      $('sent').hidden = false;
    }).catch(function (x) {
      $('err').textContent = x.message;
      $('err').hidden = false;
      send.disabled = false;
      send.textContent = 'Send request';
    });
  };
})();
