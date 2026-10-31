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

// Keep period mode explicit so changing views does not reset filters.
document.addEventListener('DOMContentLoaded', function () {
  var mode = document.querySelector('[data-period-mode]');
  if (!mode) return;
  function update() {
    document.querySelectorAll('[data-custom-field]').forEach(function (el) { el.hidden = mode.value !== 'custom'; });
    document.querySelectorAll('[data-year-field]').forEach(function (el) { el.hidden = mode.value === 'custom'; });
  }
  var kind = document.querySelector('.stats-period [name="kind"]');
  var initialKind = kind.value;
  kind.addEventListener('change', function () {
    // Expense and income categories have different IDs. A new kind starts
    // with its own categories rather than an invisible old selection.
    var selection = document.querySelector('.stats-period [name="series"]');
    if (selection) selection.disabled = kind.value !== initialKind;
  });
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
