(function () {
  var $ = function (id) { return document.getElementById(id); };
  var minutes = 30;
  var ask = $('ask');
  if (ask) {
    ask.addEventListener('click', function (e) {
      var b = e.target.closest('[data-min]');
      if (!b) return;
      minutes = +b.dataset.min;
      Array.prototype.forEach.call(ask.querySelectorAll('[data-min]'), function (x) { x.classList.toggle('on', x === b); });
    });
  }
  var send = $('send');
  if (send) {
    send.onclick = function () {
      send.disabled = true;
      send.textContent = 'Sending…';
      $('err').hidden = true;
      fetch('/__fengard/me/request', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Fengard': '1' },
        body: JSON.stringify({ minutes: minutes, note: $('note').value }),
      }).then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (d) {
          if (!r.ok) throw new Error(d.error || 'Something went wrong.');
        });
      }).then(function () {
        $('sent').hidden = false;
        $('note').hidden = true;
        ask.hidden = true;
        send.hidden = true;
      }).catch(function (x) {
        $('err').textContent = x.message;
        $('err').hidden = false;
        send.disabled = false;
        send.textContent = 'Ask for more time';
      });
    };
  }
  // server rendered so just reload every minute unless someone is typing
  setInterval(function () {
    var note = $('note');
    if (document.hidden || (note && document.activeElement === note && note.value)) return;
    location.reload();
  }, 60000);
})();
