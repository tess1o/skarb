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
