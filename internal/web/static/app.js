document.addEventListener('submit', function (event) {
  var form = event.target;
  if (!(form instanceof HTMLFormElement)) return;
  var message = form.getAttribute('data-confirm');
  if (message && !window.confirm(message)) {
    event.preventDefault();
  }
});

// On narrow screens the nav scrolls sideways; keep the current section visible.
document.addEventListener('DOMContentLoaded', function () {
  var nav = document.querySelector('.nav');
  var active = nav && nav.querySelector('a.active');
  if (!active || nav.scrollWidth <= nav.clientWidth) return;
  nav.scrollLeft = active.offsetLeft - nav.offsetLeft - (nav.clientWidth - active.offsetWidth) / 2;
});

// Bulk selection on the transactions list: select-all, live count, and
// shift-click to toggle a range of rows.
document.addEventListener('DOMContentLoaded', function () {
  var form = document.getElementById('tx-bulk');
  if (!form) return;
  var boxes = Array.prototype.slice.call(document.querySelectorAll('input[name="uid"][form="tx-bulk"]'));
  var all = form.querySelector('[data-select-all]');
  var count = form.querySelector('[data-selected-count]');
  var submit = form.querySelector('[data-bulk-submit]');
  var last = null;

  function plural(n, one, few, many) {
    var m10 = n % 10, m100 = n % 100;
    if (m10 === 1 && m100 !== 11) return one;
    if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
    return many;
  }

  function update() {
    var n = boxes.filter(function (b) { return b.checked; }).length;
    all.checked = n > 0 && n === boxes.length;
    all.indeterminate = n > 0 && n < boxes.length;
    all.disabled = boxes.length === 0;
    submit.disabled = n === 0;
    count.textContent = n ? 'Вибрано: ' + n : '';
    form.setAttribute('data-confirm', 'Видалити ' + n + ' ' + plural(n, 'транзакцію', 'транзакції', 'транзакцій') + '?');
  }

  all.addEventListener('change', function () {
    boxes.forEach(function (b) { b.checked = all.checked; });
    update();
  });
  boxes.forEach(function (box) {
    box.addEventListener('click', function (event) {
      if (event.shiftKey && last && last !== box) {
        var a = boxes.indexOf(last), b = boxes.indexOf(box);
        boxes.slice(Math.min(a, b), Math.max(a, b) + 1).forEach(function (x) { x.checked = box.checked; });
      }
      last = box;
      update();
    });
  });
  update();
});

// Transaction form: the Витрата/Дохід toggle limits the category list to that
// type. Options are rebuilt rather than hidden because iOS Safari ignores
// hidden <option>s in its native picker. Without JS the grouped list remains.
document.addEventListener('DOMContentLoaded', function () {
  var toggle = document.querySelector('[data-kind-toggle]');
  var select = document.querySelector('[data-category-select]');
  if (!toggle || !select) return;
  var placeholder = select.querySelector('option[value=""]');
  var groups = {};
  Array.prototype.forEach.call(select.querySelectorAll('optgroup'), function (g) {
    groups[g.getAttribute('data-kind')] = Array.prototype.slice.call(g.children);
  });

  function show(kind) {
    var current = select.value;
    select.textContent = '';
    select.appendChild(placeholder);
    (groups[kind] || []).forEach(function (o) { select.appendChild(o); });
    var keep = (groups[kind] || []).some(function (o) { return o.value === current; });
    select.value = keep ? current : '';
  }

  toggle.hidden = false;
  toggle.addEventListener('change', function (e) { show(e.target.value); });
  show(toggle.querySelector('input:checked').value);
});

// Filters are always visible on wide screens; on phones they collapse.
document.addEventListener('DOMContentLoaded', function () {
  var box = document.querySelector('[data-filters]');
  if (box && window.matchMedia('(min-width: 641px)').matches) box.open = true;
});

// Charts use the same server totals as the tables; no external chart library.
document.addEventListener('DOMContentLoaded', function () {
  document.querySelectorAll('[data-chart]').forEach(function (host) {
    var points = Array.from(host.querySelectorAll('[data-point]')).map(function (p) { return p.dataset; });
    var line = host.dataset.chart === 'line';
    var category = host.dataset.chart === 'category';
    var currency = host.dataset.currency;
    var money = new Intl.NumberFormat('uk-UA', { style: 'currency', currency: currency, maximumFractionDigits: 1, notation: 'compact', currencyDisplay: 'narrowSymbol' });
    var exact = new Intl.NumberFormat('uk-UA', { style: 'currency', currency: currency });
    var scale = Math.pow(10, exact.resolvedOptions().maximumFractionDigits);
    var format = function (v) { return exact.format(v / scale); };
    var ns = 'http://www.w3.org/2000/svg';
    function node(tag, attrs, text) {
      var el = document.createElementNS(ns, tag);
      Object.keys(attrs).forEach(function (key) { el.setAttribute(key, attrs[key]); });
      if (text !== undefined) el.textContent = text;
      return el;
    }
    var output = document.createElement('p');
    output.className = 'chart-readout';
    output.setAttribute('aria-live', 'polite');
    function description(p) {
      return p.label + ': ' + ((line || category) ? (p.value === undefined ? 'неповні дані' : format(+p.value) + (+p.missing > 0 ? ' · неповно: операцій без курсу ' + p.missing : '')) : 'доходи ' + format(+p.income) + ' · витрати ' + format(+p.expense) + ' · залишок ' + format(+p.income - +p.expense));
    }
    var link;
    if (category) {
      link = document.createElement('a');
      link.className = 'chart-drilldown';
      link.textContent = 'Транзакції за цей місяць →';
    }
    var selected = points[points.length - 1];
    function show(p) {
      selected = p;
      output.textContent = description(p);
      if (link) link.href = p.url;
    }
    function draw() {
      var old = host.querySelector('svg');
      if (old) old.remove();
      var width = host.clientWidth;
      if (!width || !points.length) { output.textContent = 'Немає даних за період.'; return; }
      var height = 250, left = width < 400 ? 65 : 90, right = 18, top = 18, bottom = 35;
      var plotW = width - left - right, plotH = height - top - bottom;
      var values = points.flatMap(function (p) { return (line || category) ? (p.value === undefined ? [] : [+p.value]) : [+p.income, +p.expense]; });
      var min = Math.min(0, ...values), max = Math.max(0, ...values);
      if (max === min) max = min + 100;
      var y = function (v) { return top + (max - v) / (max - min) * plotH; };
      var dates = points.map(function (p) { return Date.parse(p.date); });
      var span = dates[dates.length - 1] - dates[0];
      var x = function (i) { return line ? left + (span ? (dates[i] - dates[0]) / span : .5) * plotW : left + (i + .5) * plotW / points.length; };
      var svg = node('svg', { viewBox: '0 0 ' + width + ' ' + height, role: 'group', 'aria-label': host.dataset.label });
      for (var tick = 0; tick <= 4; tick++) {
        var value = min + (max - min) * tick / 4;
        svg.appendChild(node('line', { x1: left, x2: width - right, y1: y(value), y2: y(value), class: 'chart-grid' }));
        svg.appendChild(node('text', { x: left - 7, y: y(value) + 4, 'text-anchor': 'end', class: 'chart-axis' }, money.format(value / scale)));
      }
      if (line) {
        var path = '', connected = false;
        points.forEach(function (p, i) {
          if (p.value === undefined) { connected = false; return; }
          path += (connected ? ' L ' : ' M ') + x(i) + ' ' + y(+p.value);
          connected = true;
        });
        svg.appendChild(node('path', { d: path, class: 'chart-line' }));
      }
      var step = Math.max(1, Math.ceil(points.length / Math.max(2, Math.floor(plotW / 65))));
      points.forEach(function (p, i) {
        var cx = x(i);
        if (line && p.value !== undefined) svg.appendChild(node('circle', { cx: cx, cy: y(+p.value), r: 4, class: 'chart-point' }));
        if (category) {
          var singleW = Math.min(30, plotW / points.length * .65);
          svg.appendChild(node('rect', {x: cx - singleW / 2, y: y(+p.value), width: singleW, height: Math.max(0, y(0) - y(+p.value)), rx: 2, class: 'chart-category' + (+p.missing > 0 ? ' chart-incomplete' : '')}));
        } else if (!line) {
          var barW = Math.min(24, plotW / points.length * .32);
          ['income', 'expense'].forEach(function (key, series) {
            var value = +p[key];
            svg.appendChild(node('rect', { x: cx + (series - 1) * barW, y: Math.min(y(value), y(0)), width: Math.max(1, barW - 1), height: Math.abs(y(value) - y(0)), rx: 2, class: 'chart-' + key }));
          });
        }
        if (i % step === 0) svg.appendChild(node('text', { x: cx, y: height - 10, 'text-anchor': 'middle', class: 'chart-axis' }, p.label));
        var hitW = line ? 28 : Math.max(24, plotW / points.length);
        var hit = node('rect', { x: Math.max(left - 14, cx - hitW / 2), y: top, width: hitW, height: plotH, tabindex: 0, role: 'button', 'aria-label': description(p), class: 'chart-hit' });
        function select() { show(p); }
        hit.addEventListener('pointerenter', select);
        hit.addEventListener('click', select);
        hit.addEventListener('focus', select);
        hit.addEventListener('keydown', function (event) {
          if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); select(); }
        });
        svg.appendChild(hit);
      });
      host.insertBefore(svg, output);
      show(selected);
    }
    host.appendChild(output);
    if (link) host.appendChild(link);
    var drawnWidth = 0;
    new ResizeObserver(function () {
      // Selecting a point can change the readout height. Only width changes
      // need new geometry; height changes must not replace the focused point.
      if (host.clientWidth === drawnWidth) return;
      drawnWidth = host.clientWidth;
      draw();
    }).observe(host);
  });
});

// Keep period mode explicit so changing views does not reset filters.
document.addEventListener('DOMContentLoaded', function () {
  var mode = document.querySelector('[data-period-mode]');
  if (!mode) return;
  function update() {
    document.querySelectorAll('[data-custom-field]').forEach(function (el) { el.hidden = mode.value !== 'custom'; });
    document.querySelectorAll('[data-year-field]').forEach(function (el) { el.hidden = mode.value === 'custom'; });
  }
  mode.addEventListener('change', update);
  update();
});

// Native details keeps the admin menu usable without JavaScript.
document.addEventListener('click', function (event) {
  document.querySelectorAll('.admin-menu[open]').forEach(function (menu) {
    if (!menu.contains(event.target) || event.target.closest('a')) menu.open = false;
  });
});
document.addEventListener('keydown', function (event) {
  if (event.key !== 'Escape') return;
  document.querySelectorAll('.admin-menu[open]').forEach(function (menu) {
    menu.open = false;
    menu.querySelector('summary').focus();
  });
});

// Keep feedback next to the form when a save returns to an anchored section.
document.addEventListener('DOMContentLoaded', function () {
  var section = document.getElementById(window.location.hash.slice(1));
  var flash = document.querySelector('main > .flash[role="status"]');
  if (section && section.matches('main section[id], main details[id]') && flash) {
    section.prepend(flash);
    section.scrollIntoView();
  }
});
