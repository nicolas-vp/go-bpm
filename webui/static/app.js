// Консоль go-bpm: вкладки процессов, запуска, экземпляров, задач пользователей, термокарты и динамики поверх /api/v1.
// Вкладка и её параметры живут в адресе после # (#/instances?process=X&status=failed), поэтому ссылки
// на отфильтрованный список можно передать, а «назад» в браузере возвращает к прежнему виду.
// История экземпляра - отдельная страница /ui/history/ с той же шапкой.
(function () {
  'use strict';

  var PAGE_SIZE = 20;
  var STATUS_LABELS = { running: 'выполняется', completed: 'завершён', failed: 'упал', cancelled: 'отменён' };
  var errorBox = document.getElementById('error');

  // processes - процессы для выпадающих списков, загружаются один раз и по «Обновить»
  var processes = [];

  // --- общее -----------------------------------------------------------------------------------

  // api делает запрос к /api/v1 и возвращает JSON; ошибка API - исключение с её текстом
  async function api(method, path, body) {
    var options = { method: method, headers: {} };
    if (body !== undefined) {
      options.headers['Content-Type'] = 'application/json';
      options.body = JSON.stringify(body);
    }
    var resp = await fetch('/api/v1' + path, options);
    if (resp.status === 204) {
      return null;
    }
    var data = await resp.json().catch(function () { return {}; });
    if (!resp.ok) {
      throw new Error(data.error || resp.status + ' ' + resp.statusText);
    }
    return data;
  }

  // el создаёт элемент: attrs - атрибуты и обработчики on<событие>, children - узлы и строки (как текст)
  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (key) {
      var value = attrs[key];
      if (value === undefined || value === null || value === false) {
        return;
      }
      if (key.slice(0, 2) === 'on') {
        node.addEventListener(key.slice(2), value);
      } else if (key === 'className') {
        node.className = value;
      } else {
        node.setAttribute(key, value === true ? '' : value);
      }
    });
    [].concat(children || []).forEach(function (child) {
      if (child !== undefined && child !== null && child !== false) {
        node.append(child instanceof Node ? child : String(child));
      }
    });
    return node;
  }

  function showError(err) {
    errorBox.textContent = err ? (err.message || String(err)) : '';
    errorBox.hidden = !err;
  }

  function formatTime(value) {
    if (!value) {
      return '';
    }
    return new Date(value).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'medium' });
  }

  // shortId - хвост id: начало UUID v7 - время, у экземпляров, запущенных подряд, оно совпадает
  function shortId(id) {
    return id ? '…' + id.slice(-12) : '';
  }

  function historyUrl(instanceId) {
    return '/ui/history/?instanceId=' + encodeURIComponent(instanceId);
  }

  function statusBadge(status) {
    return el('span', { className: 'badge badge-' + status }, STATUS_LABELS[status] || status);
  }

  function processTitle(id) {
    var process = processes.find(function (p) { return p.id === id; });
    return process && process.name ? process.name : id;
  }

  async function loadProcesses() {
    var data = await api('GET', '/processes');
    processes = data.processes || [];
    fillProcessSelect(document.getElementById('start-process'), false);
    fillProcessSelect(document.getElementById('instances-process'), true);
    fillProcessSelect(document.getElementById('heatmap-process'), false);
    fillProcessSelect(document.getElementById('stats-process'), false);
    return processes;
  }

  function fillProcessSelect(select, withAll) {
    var current = select.value;
    select.replaceChildren();
    if (withAll) {
      select.append(el('option', { value: '' }, 'все'));
    }
    processes.forEach(function (process) {
      select.append(el('option', { value: process.id }, process.name ? process.name + ' (' + process.id + ')' : process.id));
    });
    if (current) {
      select.value = current;
    }
  }

  // --- переходы между вкладками ------------------------------------------------------------------

  var tabs = {
    processes: showProcesses,
    start: showStart,
    instances: showInstances,
    tasks: showTasks,
    heatmap: showHeatmap,
    stats: showStats,
  };

  // route разбирает адрес после #: "#/instances?status=failed" -> {tab: "instances", params}
  function route() {
    var hash = location.hash.replace(/^#\/?/, '');
    var parts = hash.split('?');
    var tab = tabs[parts[0]] ? parts[0] : 'processes';
    return { tab: tab, params: new URLSearchParams(parts[1] || '') };
  }

  function go(tab, params) {
    var query = params ? new URLSearchParams(params).toString() : '';
    location.hash = '#/' + tab + (query ? '?' + query : '');
  }

  function render() {
    var current = route();
    showError(null);
    document.querySelectorAll('.tabs a').forEach(function (link) {
      if (link.dataset.tab === current.tab) {
        link.setAttribute('aria-current', 'page');
      } else {
        link.removeAttribute('aria-current');
      }
    });
    document.querySelectorAll('.tab').forEach(function (section) {
      section.hidden = section.id !== 'tab-' + current.tab;
    });
    tabs[current.tab](current.params).catch(showError);
  }

  window.addEventListener('hashchange', render);

  // --- процессы ----------------------------------------------------------------------------------

  var processesBody = document.getElementById('processes-body');
  var diagramBox = document.getElementById('diagram-box');
  var diagramTitle = document.getElementById('diagram-title');
  // bpmn-js создаётся при первом показе схемы: контейнер должен быть видим
  var viewer = null;

  document.getElementById('processes-refresh').addEventListener('click', function () {
    showProcesses().catch(showError);
  });
  document.getElementById('diagram-close').addEventListener('click', function () {
    diagramBox.hidden = true;
  });

  async function showProcesses() {
    await loadProcesses();
    processesBody.replaceChildren();
    if (processes.length === 0) {
      processesBody.append(el('tr', {}, el('td', { colspan: 6, className: 'empty' }, 'Процессов нет')));
      return;
    }
    processes.forEach(function (process) {
      processesBody.append(el('tr', {}, [
        el('td', {}, [
          el('div', { className: 'strong' }, process.name || process.id),
          process.name ? el('code', { className: 'muted' }, process.id) : null,
        ]),
        countCell(process, 'running'),
        countCell(process, 'completed'),
        countCell(process, 'failed'),
        countCell(process, 'cancelled'),
        el('td', { className: 'row-actions' }, [
          el('button', { className: 'button-quiet', type: 'button', onclick: function () { showDiagram(process).catch(showError); } }, 'Схема'),
          el('button', { className: 'button-quiet', type: 'button', onclick: function () { go('heatmap', { process: process.id }); } }, 'Термокарта'),
          el('button', { className: 'button', type: 'button', onclick: function () { go('start', { process: process.id }); } }, 'Запустить'),
        ]),
      ]));
    });
  }

  // countCell - число экземпляров в статусе, ссылкой на отфильтрованный список
  function countCell(process, status) {
    var count = (process.instances || {})[status] || 0;
    var cell = el('td', { className: 'num' });
    if (count === 0) {
      cell.append(el('span', { className: 'muted' }, '0'));
    } else {
      var params = new URLSearchParams({ process: process.id, status: status });
      cell.append(el('a', { href: '#/instances?' + params, className: status === 'failed' ? 'count-failed' : null }, count));
    }
    return cell;
  }

  async function showDiagram(process) {
    var data = await api('GET', '/processes/' + encodeURIComponent(process.id));
    diagramTitle.textContent = 'Схема: ' + (process.name || process.id);
    diagramBox.hidden = false;
    if (!data.bpmn) {
      showError(new Error('У процесса ' + process.id + ' нет BPMN'));
      return;
    }
    if (!viewer) {
      viewer = new BpmnJS({ container: '#diagram' });
    }
    await importProcess(viewer, data.bpmn, process.id);
    diagramBox.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }

  // importProcess рисует BPMN; в файле может быть несколько процессов: показываем корень нужного, если он отдельный
  async function importProcess(bpmnViewer, bpmn, processId) {
    await bpmnViewer.importXML(bpmn);
    var canvas = bpmnViewer.get('canvas');
    var root = bpmnViewer.get('elementRegistry').get(processId);
    if (root && root.type === 'bpmn:Process' && canvas.getRootElement() !== root) {
      canvas.setRootElement(root);
    }
    canvas.zoom('fit-viewport');
  }

  // --- редактор переменных -----------------------------------------------------------------------

  // varsEditor - переменные строками «имя, тип, значение» или одним JSON-объектом. value() возвращает объект
  // переменных или бросает ошибку с понятным текстом
  function varsEditor(container) {
    var rows = el('div', { className: 'vars-rows' });
    var json = el('textarea', { className: 'vars-json', rows: 8, spellcheck: 'false', hidden: true });
    var modeButton = el('button', { className: 'button-quiet', type: 'button' }, 'Как JSON');
    var addButton = el('button', { className: 'button-quiet', type: 'button' }, '+ переменная');
    var jsonMode = false;

    addButton.addEventListener('click', function () {
      addRow('', 'string', '');
      rows.lastChild.querySelector('input').focus();
    });
    modeButton.addEventListener('click', function () {
      try {
        if (jsonMode) {
          setRows(parseJSON());
        } else {
          json.value = JSON.stringify(fromRows(), null, 2);
        }
      } catch (err) {
        showError(err);
        return;
      }
      jsonMode = !jsonMode;
      rows.hidden = jsonMode;
      addButton.hidden = jsonMode;
      json.hidden = !jsonMode;
      modeButton.textContent = jsonMode ? 'Как поля' : 'Как JSON';
    });

    container.replaceChildren(rows, json, el('div', { className: 'vars-tools' }, [addButton, modeButton]));

    function addRow(name, type, value) {
      var typeSelect = el('select', { 'aria-label': 'Тип' }, [
        el('option', { value: 'string' }, 'строка'),
        el('option', { value: 'number' }, 'число'),
        el('option', { value: 'boolean' }, 'да/нет'),
        el('option', { value: 'json' }, 'JSON'),
      ]);
      typeSelect.value = type;
      var row = el('div', { className: 'vars-row' }, [
        el('input', { type: 'text', placeholder: 'имя', 'aria-label': 'Имя', value: name, spellcheck: 'false' }),
        typeSelect,
        valueInput(type, value),
        el('button', { className: 'panel-close', type: 'button', 'aria-label': 'Удалить', onclick: function () { row.remove(); } }, '×'),
      ]);
      // да/нет - выпадающий список, остальное - поле ввода
      typeSelect.addEventListener('change', function () {
        row.children[2].replaceWith(valueInput(typeSelect.value, row.children[2].value));
      });
      rows.append(row);
    }

    function valueInput(type, value) {
      if (type === 'boolean') {
        var select = el('select', { 'aria-label': 'Значение' }, [el('option', { value: 'true' }, 'да'), el('option', { value: 'false' }, 'нет')]);
        select.value = value === 'false' || value === false ? 'false' : 'true';
        return select;
      }
      return el('input', { type: 'text', placeholder: 'значение', 'aria-label': 'Значение', value: value === undefined ? '' : value, spellcheck: 'false' });
    }

    function fromRows() {
      var vars = {};
      Array.from(rows.children).forEach(function (row) {
        var name = row.children[0].value.trim();
        var type = row.children[1].value;
        var raw = row.children[2].value;
        if (!name) {
          if (raw.trim()) {
            throw new Error('У значения «' + raw + '» нет имени переменной');
          }
          return;
        }
        if (type === 'number') {
          var number = Number(raw.replace(',', '.'));
          if (raw.trim() === '' || Number.isNaN(number)) {
            throw new Error('Переменная ' + name + ': «' + raw + '» — не число');
          }
          vars[name] = number;
        } else if (type === 'boolean') {
          vars[name] = raw === 'true';
        } else if (type === 'json') {
          try {
            vars[name] = JSON.parse(raw);
          } catch (err) {
            throw new Error('Переменная ' + name + ': неверный JSON — ' + err.message);
          }
        } else {
          vars[name] = raw;
        }
      });
      return vars;
    }

    function parseJSON() {
      var text = json.value.trim();
      if (!text) {
        return {};
      }
      var value;
      try {
        value = JSON.parse(text);
      } catch (err) {
        throw new Error('Неверный JSON переменных: ' + err.message);
      }
      if (value === null || typeof value !== 'object' || Array.isArray(value)) {
        throw new Error('Переменные — JSON-объект: {"имя": значение}');
      }
      return value;
    }

    function setRows(vars) {
      rows.replaceChildren();
      Object.keys(vars).forEach(function (name) {
        var value = vars[name];
        if (typeof value === 'number') {
          addRow(name, 'number', String(value));
        } else if (typeof value === 'boolean') {
          addRow(name, 'boolean', String(value));
        } else if (typeof value === 'string') {
          addRow(name, 'string', value);
        } else {
          addRow(name, 'json', JSON.stringify(value));
        }
      });
    }

    return {
      value: function () { return jsonMode ? parseJSON() : fromRows(); },
      reset: function () {
        json.value = '';
        setRows({});
        addRow('', 'string', '');
      },
    };
  }

  // --- запуск ------------------------------------------------------------------------------------

  var startForm = document.getElementById('start-form');
  var startProcess = document.getElementById('start-process');
  var startKey = document.getElementById('start-key');
  var startResult = document.getElementById('start-result');
  var startVars = varsEditor(document.getElementById('start-vars'));
  startVars.reset();

  async function showStart(params) {
    if (processes.length === 0) {
      await loadProcesses();
    }
    if (params.get('process')) {
      startProcess.value = params.get('process');
    }
  }

  startForm.addEventListener('submit', async function (event) {
    event.preventDefault();
    showError(null);
    startResult.hidden = true;
    var body;
    try {
      body = { variables: startVars.value() };
    } catch (err) {
      showError(err);
      return;
    }
    if (startKey.value.trim()) {
      body.businessKey = startKey.value.trim();
    }
    var processId = startProcess.value;
    try {
      var data = await api('POST', '/processes/' + encodeURIComponent(processId) + '/instances', body);
      startResult.replaceChildren(
        el('div', {}, ['Запущен экземпляр процесса ', el('b', {}, processTitle(processId)), ': ', el('code', {}, data.id)]),
        el('div', { className: 'notice-links' }, [
          el('a', { href: historyUrl(data.id) }, 'История экземпляра'),
          el('a', { href: '#/instances?' + new URLSearchParams({ process: processId }) }, 'Экземпляры процесса'),
          el('a', { href: '#/tasks' }, 'Задачи'),
        ])
      );
      startResult.hidden = false;
    } catch (err) {
      showError(err);
    }
  });

  // --- экземпляры --------------------------------------------------------------------------------

  var instancesForm = document.getElementById('instances-form');
  var instancesProcess = document.getElementById('instances-process');
  var instancesStatus = document.getElementById('instances-status');
  var instancesKey = document.getElementById('instances-key');
  // время запуска - в адресе локальное, как в полях (#/instances?from=2026-10-01T09:00:05); "по" - включая эту секунду
  var instancesFrom = document.getElementById('instances-from');
  var instancesTo = document.getElementById('instances-to');
  var instancesBody = document.getElementById('instances-body');
  var instancesPrev = document.getElementById('instances-prev');
  var instancesNext = document.getElementById('instances-next');
  var instancesPage = document.getElementById('instances-page');
  // страницы - по курсору before (id последнего экземпляра предыдущей страницы); cursors[i] - курсор страницы i
  var pages = { cursors: [''], index: 0, filterKey: '' };

  instancesForm.addEventListener('submit', function (event) {
    event.preventDefault();
    var params = {};
    if (instancesProcess.value) params.process = instancesProcess.value;
    if (instancesStatus.value) params.status = instancesStatus.value;
    if (instancesKey.value.trim()) params.key = instancesKey.value.trim();
    if (instancesFrom.value) params.from = instancesFrom.value;
    if (instancesTo.value) params.to = instancesTo.value;
    // тот же адрес не вызовет hashchange: ищем заново сами
    if (new URLSearchParams(params).toString() === route().params.toString()) {
      pages.filterKey = null;
      render();
    } else {
      go('instances', params);
    }
  });
  instancesPrev.addEventListener('click', function () {
    pages.index--;
    loadInstancesPage().catch(showError);
  });
  instancesNext.addEventListener('click', function () {
    pages.index++;
    loadInstancesPage().catch(showError);
  });

  async function showInstances(params) {
    if (processes.length === 0) {
      await loadProcesses();
    }
    instancesProcess.value = params.get('process') || '';
    instancesStatus.value = params.get('status') || '';
    instancesKey.value = params.get('key') || '';
    instancesFrom.value = params.get('from') || '';
    instancesTo.value = params.get('to') || '';
    // новый фильтр - с первой страницы; возврат на вкладку с тем же фильтром оставляет страницу
    var filterKey = params.toString();
    if (pages.filterKey !== filterKey) {
      pages = { cursors: [''], index: 0, filterKey: filterKey };
    }
    await loadInstancesPage();
  }

  async function loadInstancesPage() {
    var params = route().params;
    var query = new URLSearchParams({ limit: PAGE_SIZE + 1 });
    if (params.get('process')) query.set('processName', params.get('process'));
    if (params.get('status')) query.set('status', params.get('status'));
    if (params.get('key')) query.set('businessKey', params.get('key'));
    // API берёт [from, to): верхняя граница - начало следующей секунды
    if (params.get('from')) query.set('from', new Date(params.get('from')).toISOString());
    if (params.get('to')) query.set('to', new Date(new Date(params.get('to')).getTime() + 1000).toISOString());
    if (pages.cursors[pages.index]) query.set('before', pages.cursors[pages.index]);
    var data = await api('GET', '/instances?' + query);
    // лишний экземпляр сверх страницы только говорит, что есть следующая
    var list = data.instances || [];
    var hasNext = list.length > PAGE_SIZE;
    list = list.slice(0, PAGE_SIZE);
    if (hasNext) {
      pages.cursors[pages.index + 1] = list[list.length - 1].instance_uid;
    }
    instancesPrev.disabled = pages.index === 0;
    instancesNext.disabled = !hasNext;
    instancesPage.textContent = list.length ? 'стр. ' + (pages.index + 1) : '';

    instancesBody.replaceChildren();
    if (list.length === 0) {
      instancesBody.append(el('tr', {}, el('td', { colspan: 7, className: 'empty' }, 'Экземпляров не найдено')));
      return;
    }
    list.forEach(function (instance) {
      var row = el('tr', { tabindex: 0, title: 'Открыть историю' }, [
        el('td', {}, el('a', { href: historyUrl(instance.instance_uid), title: instance.instance_uid }, el('code', {}, shortId(instance.instance_uid)))),
        el('td', {}, processTitle(instance.process_name)),
        el('td', {}, instance.business_key || el('span', { className: 'muted' }, '—')),
        el('td', {}, statusBadge(instance.status)),
        el('td', { className: 'nowrap' }, formatTime(instance.created_at)),
        el('td', { className: 'nowrap' }, formatTime(instance.updated_at)),
        el('td', {}, instance.parent_instance_uid
          ? el('a', { href: historyUrl(instance.parent_instance_uid), title: instance.parent_instance_uid }, el('code', {}, shortId(instance.parent_instance_uid)))
          : el('span', { className: 'muted' }, '—')),
      ]);
      // вся строка ведёт в историю, ссылки в ней работают сами
      row.addEventListener('click', function (event) {
        if (!event.target.closest('a')) {
          location.href = historyUrl(instance.instance_uid);
        }
      });
      row.addEventListener('keydown', function (event) {
        if (event.key === 'Enter') {
          location.href = historyUrl(instance.instance_uid);
        }
      });
      instancesBody.append(row);
    });
  }

  // --- динамика ----------------------------------------------------------------------------------

  // процесс и период - в адресе (#/stats?process=X&days=30); дни режутся в часовом поясе браузера
  var statsForm = document.getElementById('stats-form');
  var statsProcess = document.getElementById('stats-process');
  var statsDays = document.getElementById('stats-days');
  var statsTooltip = document.getElementById('stats-tooltip');
  var SVG = 'http://www.w3.org/2000/svg';
  // исходы - в порядке слоёв столбца снизу вверх; цвета проверены на различимость при дальтонизме
  // (валидатор палитры dataviz), у жёлтого контраст с фоном ниже 3:1 - поэтому легенда, подсказка и таблица
  var OUTCOMES = [
    { key: 'completed', label: 'завершены', color: '#2a78d6' },
    { key: 'running', label: 'выполняются', color: '#eda100' },
    { key: 'failed', label: 'упали', color: '#e34948' },
    { key: 'cancelled', label: 'отменены', color: '#6250d6' },
  ];
  var DURATIONS = [
    { key: 'median_ms', label: 'медиана', color: '#2a78d6' },
    { key: 'p95_ms', label: 'p95', color: '#eb6834' },
  ];
  // stats - последний ответ: графики перерисовываются по ширине окна без запроса
  var stats = null;

  statsForm.addEventListener('submit', function (event) {
    event.preventDefault();
    var params = { process: statsProcess.value, days: statsDays.value };
    if (new URLSearchParams(params).toString() === route().params.toString()) {
      render();
    } else {
      go('stats', params);
    }
  });

  // графики рисуются в пикселях контейнера: при смене его ширины (окно, поворот телефона) перерисовываются
  var drawnWidth = 0;
  var resizeTimer = null;
  new ResizeObserver(function (entries) {
    var width = Math.round(entries[0].contentRect.width);
    if (!stats || width === 0 || width === drawnWidth) {
      return;
    }
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(drawStats, 100);
  }).observe(document.getElementById('stats-outcomes'));

  async function showStats(params) {
    if (processes.length === 0) {
      await loadProcesses();
    }
    var processId = params.get('process');
    statsDays.value = ['7', '30', '90', '365'].indexOf(params.get('days')) >= 0 ? params.get('days') : '30';
    if (!processId) {
      document.getElementById('stats-result').hidden = true;
      document.getElementById('stats-hint').hidden = false;
      return;
    }
    statsProcess.value = processId;
    // период - целые дни: с полуночи (days - 1) дней назад до конца сегодняшнего
    var days = Number(statsDays.value);
    var to = new Date();
    to.setHours(24, 0, 0, 0);
    var from = new Date(to);
    from.setDate(from.getDate() - days);
    var query = new URLSearchParams({
      from: from.toISOString(),
      to: to.toISOString(),
      tz: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
    });
    var result = document.getElementById('stats-result');
    // перезапрос держит прежний вид приглушённым, без скачка раскладки
    result.classList.add('is-loading');
    try {
      stats = await api('GET', '/processes/' + encodeURIComponent(processId) + '/stats?' + query);
    } finally {
      result.classList.remove('is-loading');
    }
    document.getElementById('stats-hint').hidden = true;
    result.hidden = false;
    renderStatTiles(stats.total, processId);
    drawStats();
    renderStatsTable(stats.days);
  }

  function drawStats() {
    drawnWidth = Math.round(document.getElementById('stats-outcomes').clientWidth);
    renderLegend(document.getElementById('stats-legend-outcomes'), OUTCOMES, 'rect');
    renderLegend(document.getElementById('stats-legend-durations'), DURATIONS, 'line');
    drawOutcomes(document.getElementById('stats-outcomes'), stats.days);
    drawDurations(document.getElementById('stats-durations'), stats.days);
  }

  function renderStatTiles(total, processId) {
    var link = function (status) {
      return '#/instances?' + new URLSearchParams({ process: processId, status: status });
    };
    var tiles = [
      { label: 'Запущено', value: formatCount(total.started) },
      { label: 'Завершено', value: formatCount(total.completed), share: total.started ? total.completed / total.started : null },
      { label: 'Выполняются', value: formatCount(total.running), href: total.running ? link('running') : null },
      { label: 'Упали', value: formatCount(total.failed), href: total.failed ? link('failed') : null, alert: total.failed > 0 },
      { label: 'Отменены', value: formatCount(total.cancelled) },
      { label: 'Медиана длительности', value: total.median_ms == null ? '—' : formatDuration(total.median_ms) },
      { label: 'p95 длительности', value: total.p95_ms == null ? '—' : formatDuration(total.p95_ms) },
    ];
    var box = document.getElementById('stats-tiles');
    box.replaceChildren();
    tiles.forEach(function (tile) {
      var value = el('div', { className: 'stat-value' + (tile.alert ? ' stat-alert' : '') }, tile.value);
      box.append(el(tile.href ? 'a' : 'div', { className: 'stat-tile', href: tile.href }, [
        el('div', { className: 'stat-label' }, tile.label),
        value,
        tile.share != null ? el('div', { className: 'stat-note' }, Math.round(tile.share * 100) + '% запущенных') : null,
      ]));
    });
  }

  function renderLegend(box, series, shape) {
    box.replaceChildren();
    series.forEach(function (item) {
      box.append(el('span', { className: 'legend-item' }, [
        el('span', { className: 'legend-key legend-key-' + shape, style: 'background:' + item.color }),
        item.label,
      ]));
    });
  }

  function renderStatsTable(days) {
    var body = document.getElementById('stats-body');
    body.replaceChildren();
    // новые дни сверху: обычно смотрят вчера и сегодня
    days.slice().reverse().forEach(function (day) {
      var zero = function (value) { return value ? formatCount(value) : el('span', { className: 'muted' }, '0'); };
      body.append(el('tr', {}, [
        el('td', { className: 'nowrap' }, formatDay(day.day, true)),
        el('td', { className: 'num' }, zero(day.started)),
        el('td', { className: 'num' }, zero(day.completed)),
        el('td', { className: 'num' }, zero(day.running)),
        el('td', { className: 'num' }, day.failed ? el('span', { className: 'count-failed' }, formatCount(day.failed)) : zero(0)),
        el('td', { className: 'num' }, zero(day.cancelled)),
        el('td', { className: 'num nowrap' }, day.median_ms == null ? el('span', { className: 'muted' }, '—') : formatDuration(day.median_ms)),
        el('td', { className: 'num nowrap' }, day.p95_ms == null ? el('span', { className: 'muted' }, '—') : formatDuration(day.p95_ms)),
      ]));
    });
  }

  // --- графики: SVG без библиотек ----------------------------------------------------------------

  var CHART = { height: 220, top: 12, right: 12, bottom: 28, left: 56 };

  function svg(tag, attrs) {
    var node = document.createElementNS(SVG, tag);
    Object.keys(attrs || {}).forEach(function (key) {
      node.setAttribute(key, attrs[key]);
    });
    return node;
  }

  // DURATION_STEPS - шаги оси длительности, кратные единицам времени: 15 мин, 1 ч, 6 ч, а не 5 ч 33 мин
  var DURATION_STEPS = [1, 2, 5, 10, 20, 50, 100, 200, 500, 1e3, 2e3, 5e3, 10e3, 15e3, 30e3, 60e3, 120e3, 300e3, 600e3,
    900e3, 1800e3, 3600e3, 7200e3, 10800e3, 21600e3, 43200e3, 86400e3, 172800e3, 604800e3];

  // chartFrame - SVG по ширине контейнера, сетка и подписи осей. format - подпись значения оси Y,
  // steps - допустимые шаги делений (по умолчанию 1, 2, 2,5, 5 × 10ⁿ)
  function chartFrame(box, days, top, format, steps) {
    var width = Math.max(box.clientWidth, 280);
    var plot = { x: CHART.left, y: CHART.top, width: width - CHART.left - CHART.right, height: CHART.height - CHART.top - CHART.bottom };
    var root = svg('svg', { width: width, height: CHART.height, viewBox: '0 0 ' + width + ' ' + CHART.height, role: 'img' });
    var ticks = niceTicks(top, steps);
    var max = ticks[ticks.length - 1] || 1;
    ticks.forEach(function (tick) {
      var y = plot.y + plot.height - tick / max * plot.height;
      root.append(svg('line', { x1: plot.x, x2: plot.x + plot.width, y1: y, y2: y, class: tick === 0 ? 'chart-baseline' : 'chart-grid' }));
      var label = svg('text', { x: plot.x - 8, y: y + 4, 'text-anchor': 'end', class: 'chart-axis' });
      label.textContent = format(tick);
      root.append(label);
    });
    var band = plot.width / days.length;
    // подписи дней - не чаще, чем помещаются
    var every = Math.ceil(44 / band);
    days.forEach(function (day, i) {
      if ((days.length - 1 - i) % every !== 0) {
        return; // последний день подписан всегда
      }
      var label = svg('text', { x: plot.x + band * (i + 0.5), y: CHART.height - 8, 'text-anchor': 'middle', class: 'chart-axis' });
      label.textContent = formatDay(day.day, false);
      root.append(label);
    });
    box.replaceChildren(root);
    return { root: root, plot: plot, band: band, max: max };
  }

  function drawOutcomes(box, days) {
    var top = days.reduce(function (acc, day) { return Math.max(acc, day.started); }, 0);
    var frame = chartFrame(box, days, top, formatCount);
    var plot = frame.plot;
    var barWidth = Math.max(Math.min(24, frame.band * 0.7), 2);
    days.forEach(function (day, i) {
      var x = plot.x + frame.band * (i + 0.5) - barWidth / 2;
      var base = plot.y + plot.height;
      var segments = OUTCOMES.filter(function (outcome) { return day[outcome.key] > 0; });
      segments.forEach(function (outcome, j) {
        var height = day[outcome.key] / frame.max * plot.height;
        // 2px фона между слоями столбца; верхний слой со скруглёнными углами
        var gap = j < segments.length - 1 ? 2 : 0;
        var drawn = Math.max(height - gap, 1);
        var y = base - height;
        frame.root.append(svg('path', {
          d: j === segments.length - 1 ? roundedTop(x, y, barWidth, drawn, Math.min(4, barWidth / 2, drawn)) : rectPath(x, y + gap, barWidth, drawn),
          fill: outcome.color,
        }));
        base -= height;
      });
      // попадание - вся полоса дня, а не закрашенные пиксели
      var hit = svg('rect', { x: plot.x + frame.band * i, y: plot.y, width: frame.band, height: plot.height, class: 'chart-hit', tabindex: 0 });
      var show = function (event) {
        hit.classList.add('is-active');
        showTooltip(event, formatDay(day.day, true), OUTCOMES.map(function (outcome) {
          return { color: outcome.color, label: outcome.label, value: formatCount(day[outcome.key]) };
        }).concat([{ label: 'запущено', value: formatCount(day.started) }]), 'rect');
      };
      hit.addEventListener('pointermove', show);
      hit.addEventListener('focus', show);
      hit.addEventListener('pointerleave', function () { hit.classList.remove('is-active'); hideTooltip(); });
      hit.addEventListener('blur', function () { hit.classList.remove('is-active'); hideTooltip(); });
      frame.root.append(hit);
    });
  }

  function drawDurations(box, days) {
    var top = days.reduce(function (acc, day) {
      return Math.max(acc, day.median_ms || 0, day.p95_ms || 0);
    }, 0);
    var frame = chartFrame(box, days, top, formatDuration, DURATION_STEPS);
    var plot = frame.plot;
    var pointX = function (i) { return plot.x + frame.band * (i + 0.5); };
    var pointY = function (value) { return plot.y + plot.height - value / frame.max * plot.height; };
    // перекрестие под линиями: ближайший день под указателем, подсказка со всеми сериями
    var crosshair = svg('line', { y1: plot.y, y2: plot.y + plot.height, class: 'chart-crosshair', visibility: 'hidden' });
    frame.root.append(crosshair);
    DURATIONS.forEach(function (series) {
      // дни без завершённых рвут линию: соединять через них - выдумывать значения
      var path = '';
      var previous = false;
      days.forEach(function (day, i) {
        var value = day[series.key];
        if (value == null) {
          previous = false;
          return;
        }
        path += (previous ? 'L' : 'M') + pointX(i).toFixed(1) + ' ' + pointY(value).toFixed(1);
        previous = true;
        // одиночную точку без соседей линией не нарисовать - ставим маркер
        var next = days[i + 1];
        var prev = days[i - 1];
        if ((!prev || prev[series.key] == null) && (!next || next[series.key] == null)) {
          frame.root.append(svg('circle', { cx: pointX(i), cy: pointY(value), r: 4, fill: series.color, class: 'chart-dot' }));
        }
      });
      if (path) {
        frame.root.append(svg('path', { d: path, stroke: series.color, class: 'chart-line' }));
      }
    });
    var markers = DURATIONS.map(function (series) {
      var dot = svg('circle', { r: 4, fill: series.color, class: 'chart-dot', visibility: 'hidden' });
      frame.root.append(dot);
      return dot;
    });
    var hit = svg('rect', { x: plot.x, y: plot.y, width: plot.width, height: plot.height, class: 'chart-hit', tabindex: 0 });
    var focusIndex = days.length - 1;
    var showDay = function (i, event) {
      var day = days[i];
      var x = pointX(i);
      crosshair.setAttribute('x1', x);
      crosshair.setAttribute('x2', x);
      crosshair.setAttribute('visibility', 'visible');
      DURATIONS.forEach(function (series, j) {
        var value = day[series.key];
        markers[j].setAttribute('visibility', value == null ? 'hidden' : 'visible');
        if (value != null) {
          markers[j].setAttribute('cx', x);
          markers[j].setAttribute('cy', pointY(value));
        }
      });
      showTooltip(event, formatDay(day.day, true), DURATIONS.map(function (series) {
        return { color: series.color, label: series.label, value: day[series.key] == null ? '—' : formatDuration(day[series.key]) };
      }).concat([{ label: 'завершено', value: formatCount(day.completed) }]), 'line');
    };
    var hide = function () {
      crosshair.setAttribute('visibility', 'hidden');
      markers.forEach(function (dot) { dot.setAttribute('visibility', 'hidden'); });
      hideTooltip();
    };
    hit.addEventListener('pointermove', function (event) {
      var rect = hit.getBoundingClientRect();
      focusIndex = Math.min(Math.max(Math.floor((event.clientX - rect.left) / frame.band), 0), days.length - 1);
      showDay(focusIndex, event);
    });
    hit.addEventListener('pointerleave', hide);
    hit.addEventListener('blur', hide);
    hit.addEventListener('focus', function (event) { showDay(focusIndex, event); });
    // с клавиатуры день выбирается стрелками
    hit.addEventListener('keydown', function (event) {
      if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault();
        focusIndex = Math.min(Math.max(focusIndex + (event.key === 'ArrowLeft' ? -1 : 1), 0), days.length - 1);
        showDay(focusIndex, event);
      }
    });
    frame.root.append(hit);
  }

  function rectPath(x, y, width, height) {
    return 'M' + x + ' ' + y + 'h' + width + 'v' + height + 'h' + -width + 'z';
  }

  // roundedTop - столбец со скруглённым верхом и прямым основанием
  function roundedTop(x, y, width, height, r) {
    return 'M' + x + ' ' + (y + height) + 'V' + (y + r) + 'Q' + x + ' ' + y + ' ' + (x + r) + ' ' + y +
      'H' + (x + width - r) + 'Q' + (x + width) + ' ' + y + ' ' + (x + width) + ' ' + (y + r) + 'V' + (y + height) + 'Z';
  }

  // niceTicks - круглые деления оси от нуля: 0, 5, 10, 15 или 0, 200, 400; около четырёх делений
  function niceTicks(top, steps) {
    if (!(top > 0)) {
      return [0, 1];
    }
    var rough = top / 4;
    var power = Math.pow(10, Math.floor(Math.log10(rough)));
    var step = (steps || [1, 2, 2.5, 5, 10].map(function (m) { return m * power; })).find(function (s) { return s >= rough; }) ||
      Math.ceil(rough / steps[steps.length - 1]) * steps[steps.length - 1];
    var ticks = [];
    for (var value = 0; value < top + step / 2; value += step) {
      ticks.push(value);
    }
    if (ticks[ticks.length - 1] < top) {
      ticks.push(ticks[ticks.length - 1] + step);
    }
    return ticks;
  }

  function showTooltip(event, title, rows, shape) {
    statsTooltip.replaceChildren(el('div', { className: 'tooltip-title' }, title));
    rows.forEach(function (row) {
      statsTooltip.append(el('div', { className: 'tooltip-row' }, [
        row.color ? el('span', { className: 'legend-key legend-key-' + shape, style: 'background:' + row.color }) : el('span', { className: 'legend-key' }),
        el('span', { className: 'tooltip-value' }, row.value),
        el('span', { className: 'tooltip-label' }, row.label),
      ]));
    });
    statsTooltip.hidden = false;
    var target = event && event.clientX !== undefined ? { x: event.clientX, y: event.clientY } : null;
    if (!target) {
      var rect = event.target.getBoundingClientRect();
      target = { x: rect.left + rect.width / 2, y: rect.top };
    }
    var width = statsTooltip.offsetWidth;
    var left = target.x + 14 + width > window.innerWidth - 8 ? target.x - 14 - width : target.x + 14;
    statsTooltip.style.left = Math.max(8, left) + 'px';
    statsTooltip.style.top = Math.max(8, target.y - statsTooltip.offsetHeight - 8) + 'px';
  }

  function hideTooltip() {
    statsTooltip.hidden = true;
  }

  function formatCount(value) {
    return Number(value || 0).toLocaleString('ru-RU');
  }

  // formatDay - "2026-10-27" -> "27.10" на оси, "пн, 27 окт. 2026" в подсказке и таблице
  function formatDay(day, long) {
    var parts = day.split('-').map(Number);
    var date = new Date(parts[0], parts[1] - 1, parts[2]);
    return long
      ? date.toLocaleDateString('ru-RU', { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' })
      : date.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' });
  }

  // --- термокарта --------------------------------------------------------------------------------

  // процесс и период - в адресе (#/heatmap?process=X&from=2026-10-01T00:00&to=...): время локальное, как в полях
  var heatmapForm = document.getElementById('heatmap-form');
  var heatmapProcess = document.getElementById('heatmap-process');
  var heatmapFrom = document.getElementById('heatmap-from');
  var heatmapTo = document.getElementById('heatmap-to');
  var heatmapMetric = document.getElementById('heatmap-metric');
  var heatmapResult = document.getElementById('heatmap-result');
  var heatmapBody = document.getElementById('heatmap-body');
  // шкала от бледного к красному: доля от самого долгого элемента
  var HEAT_COLORS = [[255, 243, 196], [255, 196, 102], [245, 120, 66], [214, 40, 40], [140, 0, 30]];
  var heatmapViewer = null;
  // heatmap - последний ответ API и процесс, BPMN которого нарисован: метрика меняется без запроса
  var heatmap = { data: null, drawn: '', key: '' };

  heatmapForm.addEventListener('submit', function (event) {
    event.preventDefault();
    var params = { process: heatmapProcess.value };
    if (heatmapFrom.value) params.from = heatmapFrom.value;
    if (heatmapTo.value) params.to = heatmapTo.value;
    // тот же адрес не вызовет hashchange: строим заново сами
    if (new URLSearchParams(params).toString() === route().params.toString()) {
      heatmap.key = '';
      render();
    } else {
      go('heatmap', params);
    }
  });
  heatmapMetric.addEventListener('change', function () {
    paintHeatmap();
  });

  async function showHeatmap(params) {
    if (processes.length === 0) {
      await loadProcesses();
    }
    heatmapFrom.value = params.get('from') || '';
    heatmapTo.value = params.get('to') || '';
    var processId = params.get('process');
    if (!processId) {
      heatmapResult.hidden = true;
      document.getElementById('heatmap-hint').hidden = false;
      return;
    }
    heatmapProcess.value = processId;
    // возврат на вкладку с тем же фильтром не перестраивает карту: схема уже нарисована
    if (heatmap.key === params.toString() && heatmap.data) {
      return;
    }
    var query = new URLSearchParams();
    if (params.get('from')) query.set('from', new Date(params.get('from')).toISOString());
    if (params.get('to')) query.set('to', new Date(params.get('to')).toISOString());
    var path = '/processes/' + encodeURIComponent(processId);
    var results = await Promise.all([
      heatmap.drawn === processId ? null : api('GET', path),
      api('GET', path + '/heatmap?' + query),
    ]);
    document.getElementById('heatmap-hint').hidden = true;
    heatmapResult.hidden = false;
    if (results[0]) {
      if (!results[0].bpmn) {
        throw new Error('У процесса ' + processId + ' нет BPMN');
      }
      // bpmn-js создаётся, когда контейнер видим
      if (!heatmapViewer) {
        heatmapViewer = new BpmnJS({ container: '#heatmap-diagram' });
      }
      heatmap.drawn = '';
      await importProcess(heatmapViewer, results[0].bpmn, processId);
      heatmap.drawn = processId;
    }
    heatmap.data = results[1];
    heatmap.key = params.toString();
    var summary = 'Экземпляров: ' + heatmap.data.instances;
    if (heatmap.data.truncated) {
      summary += ' — за период их больше, посчитаны последние запущенные';
    }
    if (heatmap.data.instances === 0) {
      summary = 'За период экземпляров нет';
    }
    document.getElementById('heatmap-summary').textContent = summary;
    paintHeatmap();
  }

  // paintHeatmap красит элементы схемы по выбранной метрике и заполняет таблицу
  function paintHeatmap() {
    if (!heatmap.data || !heatmapViewer) {
      return;
    }
    var metric = heatmapMetric.value;
    var registry = heatmapViewer.get('elementRegistry');
    var overlays = heatmapViewer.get('overlays');
    var canvas = heatmapViewer.get('canvas');
    overlays.remove({ type: 'heat' });
    registry.forEach(function (element) {
      var visual = heatVisual(registry, element);
      if (visual) {
        heatStyle(visual, 'fill', null);
        heatStyle(visual, 'fillOpacity', null);
        heatLabel(visual, null);
      }
      canvas.removeMarker(element.id, 'heat-selected');
    });

    // переходы проходятся мгновенно: красим только фигуры, шкала - от самой долгой из них
    var shapes = heatmap.data.elements.filter(function (timing) {
      var element = registry.get(timing.element_id);
      return element && !element.waypoints;
    });
    var top = shapes.reduce(function (acc, timing) { return Math.max(acc, timing[metric]); }, 0);
    shapes.forEach(function (timing) {
      var element = registry.get(timing.element_id);
      var visual = heatVisual(registry, element);
      if (visual) {
        var share = top > 0 ? timing[metric] / top : 0;
        heatStyle(visual, 'fill', heatColor(share));
        // у раскрытого подпроцесса внутри свои элементы: заливка полупрозрачная
        if (element.type === 'bpmn:SubProcess' && element.di && element.di.isExpanded) {
          heatStyle(visual, 'fillOpacity', '0.35');
        } else if (share > 0.6) {
          heatLabel(visual, '#fff'); // на тёмно-красном чёрная подпись не читается
        }
      }
      // бейдж над левым верхним углом: под событиями их подписи
      overlays.add(element.id, 'heat', {
        position: { top: -22, left: 0 },
        html: el('div', { className: 'heat-badge', title: timingTitle(timing) }, metricText(timing[metric], metric)),
      });
    });
    document.getElementById('heatmap-legend-min').textContent = metricText(0, metric);
    document.getElementById('heatmap-legend-max').textContent = metricText(top, metric);
    renderHeatmapTable(registry, metric);
  }

  // heatVisual - контур фигуры: первый элемент её рисунка (rect задачи, circle события, polygon шлюза)
  function heatVisual(registry, element) {
    if (!element || element.waypoints || element.type === 'label' || !element.businessObject) {
      return null;
    }
    var gfx = registry.getGraphics(element);
    return gfx ? gfx.querySelector('.djs-visual > :first-child') : null;
  }

  // heatLabel - цвет подписи внутри фигуры, null - как нарисовал bpmn-js
  function heatLabel(visual, color) {
    visual.parentNode.querySelectorAll('text').forEach(function (text) {
      heatStyle(text, 'fill', color);
    });
  }

  // heatStyle меняет свойство стиля SVG-узла, null возвращает прежнее. bpmn-js сам задаёт заливку в style:
  // пустое значение вместо неё - чёрная фигура
  function heatStyle(node, property, value) {
    var saved = 'heat' + property;
    if (value === null) {
      if (saved in node.dataset) {
        node.style[property] = node.dataset[saved];
        delete node.dataset[saved];
      }
      return;
    }
    if (!(saved in node.dataset)) {
      node.dataset[saved] = node.style[property];
    }
    node.style[property] = value;
  }

  function heatColor(share) {
    var position = Math.min(Math.max(share, 0), 1) * (HEAT_COLORS.length - 1);
    var index = Math.min(Math.floor(position), HEAT_COLORS.length - 2);
    var t = position - index;
    var from = HEAT_COLORS[index];
    var to = HEAT_COLORS[index + 1];
    return 'rgb(' + from.map(function (c, i) { return Math.round(c + (to[i] - c) * t); }).join(',') + ')';
  }

  function metricText(value, metric) {
    return metric === 'count' ? String(value) : formatDuration(value);
  }

  function timingTitle(timing) {
    var lines = [
      'Проходов: ' + timing.count,
      'Среднее: ' + formatDuration(timing.avg_ms),
      'Мин: ' + formatDuration(timing.min_ms) + ', макс: ' + formatDuration(timing.max_ms),
      'Суммарно: ' + formatDuration(timing.total_ms),
    ];
    if (timing.active) {
      lines.push('Ждут сейчас: ' + timing.active);
    }
    return lines.join('\n');
  }

  function renderHeatmapTable(registry, metric) {
    heatmapBody.replaceChildren();
    // элементы без переходов, от больших значений метрики
    var list = heatmap.data.elements.filter(function (timing) {
      var element = registry.get(timing.element_id);
      return !element || !element.waypoints;
    }).sort(function (a, b) { return b[metric] - a[metric]; });
    if (list.length === 0) {
      heatmapBody.append(el('tr', {}, el('td', { colspan: 7, className: 'empty' }, 'Проходов нет')));
      return;
    }
    list.forEach(function (timing) {
      var element = registry.get(timing.element_id);
      var name = element && element.businessObject && element.businessObject.name;
      var row = el('tr', { tabindex: 0, title: 'Показать на схеме' }, [
        el('td', {}, [
          el('div', {}, [el('span', { className: 'heat-swatch', style: 'background:' + heatSwatch(registry, timing) }), name || timing.element_id]),
          name ? el('code', { className: 'muted' }, timing.element_id) : null,
        ]),
        el('td', { className: 'num' }, timing.count),
        el('td', { className: 'num' }, timing.active || el('span', { className: 'muted' }, '0')),
        el('td', { className: 'num nowrap' }, formatDuration(timing.avg_ms)),
        el('td', { className: 'num nowrap' }, formatDuration(timing.min_ms)),
        el('td', { className: 'num nowrap' }, formatDuration(timing.max_ms)),
        el('td', { className: 'num nowrap' }, formatDuration(timing.total_ms)),
      ]);
      row.addEventListener('click', function () { focusElement(timing.element_id); });
      row.addEventListener('keydown', function (event) {
        if (event.key === 'Enter') {
          focusElement(timing.element_id);
        }
      });
      heatmapBody.append(row);
    });
  }

  // heatSwatch - цвет элемента на схеме для строки таблицы
  function heatSwatch(registry, timing) {
    var visual = heatVisual(registry, registry.get(timing.element_id));
    return visual && visual.style.fill ? visual.style.fill : 'transparent';
  }

  // focusElement выделяет элемент на схеме и прокручивает к нему
  function focusElement(id) {
    var canvas = heatmapViewer.get('canvas');
    var element = heatmapViewer.get('elementRegistry').get(id);
    if (!element) {
      return;
    }
    heatmapViewer.get('elementRegistry').forEach(function (other) {
      canvas.removeMarker(other.id, 'heat-selected');
    });
    canvas.addMarker(id, 'heat-selected');
    canvas.scrollToElement(element);
    document.getElementById('heatmap-diagram').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }

  // formatDuration - миллисекунды коротко: 0,4 мс, 120 мс, 3,2 с, 5 мин 10 с, 2 ч 15 мин, 3 д 4 ч
  function formatDuration(ms) {
    if (!ms) {
      return '0';
    }
    var number = function (value, digits) { return value.toLocaleString('ru-RU', { maximumFractionDigits: digits }); };
    if (ms < 1000) {
      return number(ms, ms < 10 ? 1 : 0) + ' мс';
    }
    var seconds = ms / 1000;
    if (seconds < 60) {
      return number(seconds, 1) + ' с';
    }
    // старшая единица и следующая за ней, если она не ноль
    var units = [['д', 86400], ['ч', 3600], ['мин', 60], ['с', 1]];
    var rest = Math.round(seconds);
    var first = units.findIndex(function (unit) { return rest >= unit[1]; });
    var high = Math.floor(rest / units[first][1]);
    var low = Math.floor((rest - high * units[first][1]) / units[first + 1][1]);
    return high + ' ' + units[first][0] + (low ? ' ' + low + ' ' + units[first + 1][0] : '');
  }

  // --- задачи ------------------------------------------------------------------------------------

  var tasksForm = document.getElementById('tasks-form');
  var tasksUser = document.getElementById('tasks-user');
  var tasksGroups = document.getElementById('tasks-groups');
  var tasksLists = document.getElementById('tasks-lists');
  var tasksHint = document.getElementById('tasks-hint');
  var tasksMine = document.getElementById('tasks-mine');
  var tasksOpen = document.getElementById('tasks-open');
  var completeDialog = document.getElementById('complete');
  var completeForm = document.getElementById('complete-form');
  var completeError = document.getElementById('complete-error');
  var completeEditor = varsEditor(document.getElementById('complete-editor'));
  var completing = null;
  // refreshTimer - повторное обновление списка: следующая задача появляется, когда очередь проведёт токен
  var refreshTimer = null;

  // пользователь и группы - в адресе (#/tasks?user=bob&groups=managers), ссылку на очередь можно передать;
  // последние запоминаются в браузере и подставляются, когда вкладку открывают без них
  function savedUser() {
    try {
      return JSON.parse(localStorage.getItem('go-bpm.tasks') || '{}');
    } catch (err) {
      return {}; // хранилище недоступно - просто не запоминаем
    }
  }

  tasksForm.addEventListener('submit', function (event) {
    event.preventDefault();
    var params = { user: tasksUser.value.trim(), groups: currentUser().groups.join(',') };
    try {
      localStorage.setItem('go-bpm.tasks', JSON.stringify(params));
    } catch (err) {
      // хранилище недоступно
    }
    if (new URLSearchParams(params).toString() === route().params.toString()) {
      showTasks(route().params).catch(showError);
    } else {
      go('tasks', params);
    }
  });
  document.getElementById('tasks-refresh').addEventListener('click', function () {
    showTasks(route().params).catch(showError);
  });

  function currentUser() {
    return {
      user: tasksUser.value.trim(),
      groups: tasksGroups.value.split(',').map(function (g) { return g.trim(); }).filter(Boolean),
    };
  }

  async function showTasks(params) {
    if (params.get('user') === null && params.get('groups') === null) {
      var saved = savedUser();
      if (saved.user || saved.groups) {
        go('tasks', { user: saved.user || '', groups: saved.groups || '' });
        return;
      }
    }
    tasksUser.value = params.get('user') || '';
    tasksGroups.value = (params.get('groups') || '').split(',').join(', ');
    var who = currentUser();
    // очередь видна и по одним группам: пользователь нужен, чтобы взять задачу и увидеть свои
    var any = !!who.user || who.groups.length > 0;
    tasksHint.hidden = any;
    tasksLists.hidden = !any;
    document.getElementById('tasks-mine-box').hidden = !who.user;
    document.getElementById('tasks-no-user').hidden = !!who.user;
    if (!any) {
      return;
    }
    if (processes.length === 0) {
      await loadProcesses();
    }
    var query = new URLSearchParams({ limit: 200 });
    if (who.user) {
      query.set('assignee', who.user);
      query.set('candidateUser', who.user);
    }
    who.groups.forEach(function (group) { query.append('candidateGroup', group); });
    var data = await api('GET', '/tasks?' + query);
    var tasks = data.tasks || [];
    var mine = who.user ? tasks.filter(function (task) { return task.assignee === who.user; }) : [];
    var open = tasks.filter(function (task) { return !task.assignee; });
    var whom = [who.user, who.groups.join(', ')].filter(Boolean).join(' и групп ');
    renderTasks(tasksMine, mine, true, 'Задач в работе нет');
    renderTasks(tasksOpen, open, false, 'Свободных задач для ' + (who.user ? whom : 'групп ' + whom) + ' нет');
    document.getElementById('tasks-mine-count').textContent = mine.length;
    document.getElementById('tasks-open-count').textContent = open.length;
  }

  function renderTasks(list, tasks, mine, emptyText) {
    list.replaceChildren();
    if (tasks.length === 0) {
      list.append(el('li', { className: 'empty' }, emptyText));
      return;
    }
    tasks.forEach(function (task) {
      var who = [];
      if (task.candidate_users && task.candidate_users.length) who.push('пользователи: ' + task.candidate_users.join(', '));
      if (task.candidate_groups && task.candidate_groups.length) who.push('группы: ' + task.candidate_groups.join(', '));
      list.append(el('li', { className: 'card' }, [
        el('div', { className: 'card-main' }, [
          el('div', { className: 'strong' }, task.name || task.element_id),
          el('div', { className: 'muted' }, [
            processTitle(task.process_name),
            task.business_key ? ' · ' + task.business_key : '',
            ' · создана ' + formatTime(task.created_at),
          ]),
          !mine && who.length ? el('div', { className: 'muted' }, who.join(' · ')) : null,
        ]),
        el('div', { className: 'row-actions' }, [
          el('a', { href: historyUrl(task.instance_uid) }, 'История'),
          mine ? el('button', { className: 'button-quiet', type: 'button', onclick: function () { taskAction(task, 'unclaim'); } }, 'Вернуть') : null,
          mine
            ? el('button', { className: 'button', type: 'button', onclick: function () { openComplete(task).catch(showError); } }, 'Выполнить')
            : el('button', {
              className: 'button', type: 'button', disabled: !currentUser().user,
              title: currentUser().user ? null : 'Укажите пользователя, чтобы взять задачу',
              onclick: function () { taskAction(task, 'claim'); },
            }, 'Взять'),
        ]),
      ]));
    });
  }

  async function taskAction(task, action) {
    showError(null);
    var who = currentUser();
    try {
      await api('POST', '/tasks/' + task.task_uid + '/' + action, { userId: who.user, groups: who.groups });
    } catch (err) {
      showError(err);
    }
    await showTasks(route().params).catch(showError);
  }

  async function openComplete(task) {
    var full = await api('GET', '/tasks/' + task.task_uid);
    completing = full;
    document.getElementById('complete-title').textContent = full.name || full.element_id;
    document.getElementById('complete-subtitle').textContent = processTitle(full.process_name) +
      (full.business_key ? ' · ' + full.business_key : '') + (full.form_key ? ' · форма ' + full.form_key : '');
    document.getElementById('complete-vars').textContent = JSON.stringify(full.variables || {}, null, 2);
    completeError.hidden = true;
    completeEditor.reset();
    completeDialog.showModal();
  }

  function closeComplete() {
    completeDialog.close();
    completing = null;
  }

  document.getElementById('complete-close').addEventListener('click', closeComplete);
  document.getElementById('complete-cancel').addEventListener('click', closeComplete);
  completeDialog.addEventListener('click', function (event) {
    if (event.target === completeDialog) {
      closeComplete(); // клик по затемнению вокруг окна
    }
  });

  completeForm.addEventListener('submit', async function (event) {
    event.preventDefault();
    if (!completing) {
      return;
    }
    completeError.hidden = true;
    try {
      var variables = completeEditor.value();
      await api('POST', '/tasks/' + completing.task_uid + '/complete', { userId: currentUser().user, variables: variables });
    } catch (err) {
      completeError.textContent = err.message;
      completeError.hidden = false;
      return;
    }
    closeComplete();
    await showTasks(route().params).catch(showError);
    // следующую задачу процесса создаёт очередь чуть позже
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(function () {
      if (route().tab === 'tasks') {
        showTasks(route().params).catch(showError);
      }
    }, 1000);
  });

  render();
})();
