// Консоль go-bpm: вкладки процессов, запуска, экземпляров, задач пользователей и термокарты поверх /api/v1.
// Вкладка и её параметры живут в адресе после # (#/instances?process=X&status=failed), поэтому ссылки
// на отфильтрованный список можно передать, а «назад» в браузере возвращает к прежнему виду.
// История экземпляра - отдельная страница /ui/history/ с той же шапкой.
(function () {
  'use strict';

  var PAGE_SIZE = 20;
  var STATUS_LABELS = { running: 'выполняется', completed: 'завершён', failed: 'упал' };
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
      processesBody.append(el('tr', {}, el('td', { colspan: 5, className: 'empty' }, 'Процессов нет')));
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
