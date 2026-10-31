// Charts use the same server totals as the tables; no external chart library.
document.addEventListener('DOMContentLoaded', function () {
  document.querySelectorAll('[data-chart]').forEach(function (host, chartIndex) {
    var multi = host.dataset.chart === 'categories';
    var series = Array.from(host.querySelectorAll('[data-series]')).map(function (el) {
      return { id: el.dataset.id, name: el.dataset.name, color: el.dataset.color,
        points: Array.from(el.querySelectorAll('[data-point]')).map(function (p) { return p.dataset; }) };
    });
    var points = multi ? (series[0] ? series[0].points : []) : Array.from(host.querySelectorAll('[data-point]')).map(function (p) { return p.dataset; });
    var line = host.dataset.chart === 'line';
    var params = new URL(window.location.href).searchParams;
    var enabled = new Set(params.has('series') ? params.get('series').split(',') : series.map(function (s) { return s.id; }));
    function visibleSeries() { return series.filter(function (s) { return enabled.has(s.id); }); }
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
    var output = document.createElement(multi ? 'div' : 'p');
    output.className = 'chart-readout';
    output.setAttribute('aria-live', 'polite');
    var tooltip = document.createElement('div');
    tooltip.className = 'chart-tooltip chart-readout';
    tooltip.id = 'chart-tooltip-' + chartIndex;
    tooltip.setAttribute('role', 'tooltip');
    tooltip.hidden = true;
    var tooltipPoint;
    function hideTooltip() { tooltip.hidden = true; }
    tooltip.addEventListener('pointerleave', function (event) {
      var svg = host.querySelector('svg');
      if (!svg || (!svg.contains(event.relatedTarget) && !svg.contains(document.activeElement))) hideTooltip();
    });
    document.addEventListener('keydown', function (event) { if (event.key === 'Escape') hideTooltip(); });
    document.addEventListener('pointerdown', function (event) { if (!host.contains(event.target)) hideTooltip(); });

    function description(p) {
      if (multi) return p.label + ': ' + visibleSeries().map(function (s) { return s.name + ' ' + pointAmount(s.points[points.indexOf(p)]); }).join(' · ');
      return p.label + ': ' + (line ? (p.value === undefined ? 'неповні дані' : pointAmount(p)) : 'доходи ' + format(+p.income) + ' · витрати ' + format(+p.expense) + ' · залишок ' + format(+p.income - +p.expense));
    }
    function pointAmount(p) { return format(+p.value) + (+p.missing > 0 ? ' · неповно: операцій без курсу ' + p.missing : ''); }
    var selected = points[points.length - 1];
    function show(p) {
      selected = p;
      if (multi) {
        var index = points.indexOf(p);
        var total = visibleSeries().reduce(function (sum, s) { return sum + +s.points[index].value; }, 0);
        var incomplete = visibleSeries().some(function (s) { return +s.points[index].missing > 0; });
        output.textContent = p.label + ' · Вибрані категорії: ' + format(total) + (incomplete ? ' · неповно' : '');
        var list = document.createElement('ul');
        visibleSeries().forEach(function (s) {
          var point = s.points[points.indexOf(p)];
          var item = document.createElement('li');
          var link = document.createElement('a');
          link.href = point.url;
          var name = document.createElement('span');
          name.textContent = s.name;
          var amount = document.createElement('span');
          amount.className = 'series-readout-amount';
          amount.textContent = pointAmount(point);
          link.title = 'Транзакції: ' + s.name + ' · ' + p.label;
          link.append(name, amount);
          item.appendChild(link);
          list.appendChild(item);
        });
        output.appendChild(list);
      } else {
        output.textContent = description(p);
      }
    }
    function draw() {
      hideTooltip();
      var old = host.querySelector('svg');
      if (old) old.remove();
      var width = host.clientWidth;
      if (!width || !points.length) { output.textContent = 'Немає даних за період.'; return; }
      if (multi && !visibleSeries().length) { output.textContent = 'Оберіть хоча б одну категорію в легенді.'; return; }
      var height = multi ? 320 : 250, left = width < 400 ? 65 : 90, right = 18, top = 18, bottom = 35;
      var plotW = width - left - right, plotH = height - top - bottom;
      var active = visibleSeries();
      // A missing rate makes the whole stack incomplete for that month:
      // do not show partial amounts as a complete stacked total.
      var complete = points.map(function (_, i) { return active.every(function (s) { return !+s.points[i].missing; }); });
      var values = multi ? points.map(function (_, i) {
        return complete[i] ? active.reduce(function (sum, s) { return sum + +s.points[i].value; }, 0) : 0;
      }) : points.flatMap(function (p) { return line ? (p.value === undefined ? [] : [+p.value]) : [+p.income, +p.expense]; });
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
      if (multi) {
        var baseline = points.map(function () { return 0; });
        var stackWidth = Math.min(56, plotW / points.length * .68);
        active.forEach(function (s) {
          s.points.forEach(function (p, i) {
            if (!complete[i]) return;
            var lower = baseline[i];
            baseline[i] += +p.value;
            svg.appendChild(node('rect', {
              x: x(i) - stackWidth / 2, y: y(baseline[i]), width: stackWidth,
              height: y(lower) - y(baseline[i]), class: 'chart-stack',
              'data-stack-series': s.id, 'data-month-index': i,
              style: '--series-color:' + s.color
            }));
          });
        });
      } else if (line) {
        var path = '', connected = false;
        points.forEach(function (p, i) {
          if (p.value === undefined) { connected = false; return; }
          path += (connected ? ' L ' : ' M ') + x(i) + ' ' + y(+p.value);
          connected = true;
          svg.appendChild(node('circle', { cx: x(i), cy: y(+p.value), r: 3, class: 'chart-point' }));
        });
        svg.appendChild(node('path', { d: path, class: 'chart-line' }));
      }
      var step = Math.max(1, Math.ceil(points.length / Math.max(2, Math.floor(plotW / 65))));
      points.forEach(function (p, i) {
        var cx = x(i);
        if (!line && !multi) {
          var barW = Math.min(24, plotW / points.length * .32);
          ['income', 'expense'].forEach(function (key, series) {
            var value = +p[key];
            svg.appendChild(node('rect', { x: cx + (series - 1) * barW, y: Math.min(y(value), y(0)), width: Math.max(1, barW - 1), height: Math.abs(y(value) - y(0)), rx: 2, class: 'chart-' + key }));
          });
        }
        if (i % step === 0) svg.appendChild(node('text', { x: cx, y: height - 10, 'text-anchor': 'middle', class: 'chart-axis' }, p.label));
        var hitLeft = i ? (x(i - 1) + cx) / 2 : left;
        var hitRight = i + 1 < points.length ? (cx + x(i + 1)) / 2 : width - right;
        var hit = node('rect', { x: hitLeft, y: top, width: hitRight - hitLeft, height: plotH, tabindex: 0, role: 'button', 'aria-label': description(p), 'aria-describedby': tooltip.id, class: 'chart-hit' });
        function select(event) {
          if (selected !== p) show(p);
          var reposition = tooltip.hidden || tooltipPoint !== p || event.type === 'focus' || event.type === 'click';
          if (tooltip.hidden || tooltipPoint !== p) {
            tooltip.replaceChildren(...Array.from(output.childNodes).map(function (child) { return child.cloneNode(true); }));
            // Keep transaction links in the persistent readout below the chart;
            // the floating copy is scrollable text.
            tooltip.querySelectorAll('a').forEach(function (link) {
              var row = document.createElement('span');
              row.className = 'tooltip-row';
              row.append(...link.childNodes);
              link.replaceWith(row);
            });
            tooltipPoint = p;
          }
          tooltip.hidden = false;
          // Keep a month’s tooltip still so the pointer can enter and scroll it.
          if (!reposition) return;
          var bounds = host.getBoundingClientRect(), hitBounds = hit.getBoundingClientRect();
          var anchorX = event.clientX === undefined ? hitBounds.left + hitBounds.width / 2 : event.clientX;
          var anchorY = event.clientY === undefined ? hitBounds.top + 20 : event.clientY;
          tooltip.style.left = Math.max(8, Math.min(anchorX - bounds.left + 12, bounds.width - tooltip.offsetWidth - 8)) + 'px';
          tooltip.style.top = Math.max(8 - bounds.top, Math.min(anchorY - bounds.top + 12, window.innerHeight - bounds.top - tooltip.offsetHeight - 8)) + 'px';
        }
        hit.addEventListener('pointerenter', select);
        hit.addEventListener('pointermove', select);
        hit.addEventListener('click', select);
        hit.addEventListener('focus', select);
        hit.addEventListener('blur', hideTooltip);
        hit.addEventListener('keydown', function (event) {
          if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); select(event); }
        });
        svg.appendChild(hit);
      });
      svg.addEventListener('pointerleave', function (event) {
        if (!tooltip.contains(event.relatedTarget) && !svg.contains(document.activeElement)) hideTooltip();
      });
      host.insertBefore(svg, output);
      show(selected);
    }
    host.append(output, tooltip);
    if (multi) {
      function updateLegend() {
        host.querySelectorAll('[data-series-toggle]').forEach(function (button) {
          button.setAttribute('aria-pressed', enabled.has(button.dataset.seriesToggle) ? 'true' : 'false');
        });
      }
      function updateSelection() {
        var value = series.filter(function (s) { return enabled.has(s.id); }).map(function (s) { return s.id; }).join(',');
        var url = new URL(window.location.href);
        url.searchParams.set('series', value);
        window.history.replaceState(null, '', url);
        document.querySelectorAll('.view-toggle a, .year-nav a').forEach(function (link) {
          var target = new URL(link.href); target.searchParams.set('series', value); link.href = target.href;
        });
        var form = document.querySelector('.stats-period');
        var input = form.querySelector('[name="series"]');
        if (!input) { input = document.createElement('input'); input.type = 'hidden'; input.name = 'series'; form.appendChild(input); }
        input.value = value;
        updateLegend();
        draw();
      }
      host.querySelectorAll('[data-series-toggle]').forEach(function (button) {
        button.addEventListener('click', function () {
          var id = button.dataset.seriesToggle;
          if (enabled.has(id)) enabled.delete(id); else enabled.add(id);
          updateSelection();
        });
      });
      host.querySelector('[data-series-all]').addEventListener('click', function () { series.forEach(function (s) { enabled.add(s.id); }); updateSelection(); });
      host.querySelector('[data-series-none]').addEventListener('click', function () { enabled.clear(); updateSelection(); });
      updateLegend();
    }
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
