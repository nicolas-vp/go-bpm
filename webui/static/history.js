// Страница истории: рисует BPMN процесса экземпляра, отмечает пройденные элементы по записям bpm_history,
// по клику на элемент показывает его проходы с переменными и кнопку перехода в историю подпроцесса
// (встроенного или callActivity). У задачи бизнес-правила - кнопка с таблицей решения DMN (dmn-js),
// где подсвечены сработавшие правила из rules_history. Упавшие задачи экземпляра и его дочерних (API /api/v1
// консоли) - над диаграммой, с перезапуском как есть или с исправленными переменными; корневой экземпляр можно отменить.
(function () {
  'use strict';

  var info = document.getElementById('info');
  var parentBox = document.getElementById('parent');
  var errorBox = document.getElementById('error');
  var panel = document.getElementById('panel');
  var panelTitle = document.getElementById('panel-title');
  var panelType = document.getElementById('panel-type');
  var panelBody = document.getElementById('panel-body');

  var viewer = new BpmnJS({ container: '#canvas' });

  var failuresBox = document.getElementById('failures');
  var actionsBox = document.getElementById('instance-actions');
  var cancelButton = document.getElementById('cancel-instance');
  var cancelConfirm = document.getElementById('cancel-confirm');
  var retryDialog = document.getElementById('retry');
  var retryVars = document.getElementById('retry-vars');
  var retryError = document.getElementById('retry-error');
  // retrying - упавшая задача, открытая в окне исправления переменных
  var retrying = null;
  var instanceStatuses = { running: 'выполняется', completed: 'завершён', failed: 'упал', cancelled: 'отменён' };

  var rulesDialog = document.getElementById('rules');
  var rulesTitle = document.getElementById('rules-title');
  var rulesSubtitle = document.getElementById('rules-subtitle');
  var rulesCalls = document.getElementById('rules-calls');
  var rulesMessage = document.getElementById('rules-message');
  var rulesResult = document.getElementById('rules-result');
  var rulesCanvas = document.getElementById('rules-canvas');
  // dmn-js создаётся при первом открытии окна: контейнер должен быть видим
  var dmnViewer = null;
  var dmnXml = null;
  // rulesRequest - номер последнего открытия окна: ответ на прежнее открытие не должен перерисовать новое
  var rulesRequest = 0;

  // текущая диаграмма: записи и дочерние экземпляры по id элемента
  var state = { data: null, recordsByElement: {}, childrenByElement: {}, selected: null };

  // переходы между экземплярами без перезагрузки, с записью в историю браузера
  window.addEventListener('popstate', function () {
    var instanceId = currentInstanceId();
    if (instanceId) {
      show(instanceId);
    }
  });

  document.getElementById('panel-close').addEventListener('click', closePanel);
  document.getElementById('rules-close').addEventListener('click', function () {
    rulesDialog.close();
  });
  rulesDialog.addEventListener('click', function (event) {
    if (event.target === rulesDialog) {
      rulesDialog.close(); // клик по затемнению вокруг окна
    }
  });

  viewer.on('element.click', function (event) {
    var element = event.element.labelTarget || event.element; // клик по подписи - это её элемент
    if (!element.parent) {
      closePanel(); // клик по пустому месту: корень диаграммы
      return;
    }
    select(element);
  });

  viewer.on('element.dblclick', function (event) {
    var element = event.element.labelTarget || event.element;
    var instances = subInstances(element.id);
    if (instances.length === 1) {
      navigate(instances[0].instance_uid);
    }
  });

  // экземпляр приходит в адресе (?instanceId=) из списка экземпляров консоли
  var initial = currentInstanceId();
  if (initial) {
    show(initial);
  }

  function currentInstanceId() {
    return new URLSearchParams(window.location.search).get('instanceId');
  }

  function navigate(instanceId) {
    var url = new URL(window.location.href);
    url.searchParams.set('instanceId', instanceId);
    url.searchParams.delete('element');
    window.history.pushState(null, '', url);
    show(instanceId);
  }

  async function show(instanceId) {
    errorBox.hidden = true;
    parentBox.hidden = true;
    failuresBox.hidden = true;
    actionsBox.hidden = true;
    info.textContent = 'Загрузка…';
    setPanelVisible(false); // без closePanel: ?element= в адресе нужен после загрузки

    var data;
    try {
      var resp = await fetch('api/diagram?instanceId=' + encodeURIComponent(instanceId));
      data = await resp.json().catch(function () { return {}; });
      if (resp.status === 404) {
        data = await withoutHistory(instanceId, data.error);
      } else if (!resp.ok) {
        throw new Error(data.error || resp.status + ' ' + resp.statusText);
      }
    } catch (err) {
      fail('Не удалось получить историю: ' + err.message);
      return;
    }

    try {
      await viewer.importXML(data.bpmn);
    } catch (err) {
      fail('Не удалось отрисовать BPMN: ' + err.message);
      return;
    }
    state = { data: data, recordsByElement: {}, childrenByElement: {}, selected: null };
    (data.records || []).forEach(function (record) {
      (state.recordsByElement[record.element_id] = state.recordsByElement[record.element_id] || []).push(record);
    });
    (data.children || []).forEach(function (child) {
      (state.childrenByElement[child.element_id] = state.childrenByElement[child.element_id] || []).push(child);
    });

    var finished = markRecords(data.records || [], data.instance_uid);
    markLinks();
    renderInfo(data, (data.records || []).length, finished ? 'completed' : 'running');
    renderParent(data.parent);
    // статус и упавшие задачи - из API консоли; без него страница остаётся историей
    loadOperations(data).catch(function (err) {
      showProblem('Не удалось получить упавшие задачи: ' + err.message);
    });

    // ?element=<id> - открыть панель элемента сразу, ссылкой можно поделиться
    var elementId = new URLSearchParams(window.location.search).get('element');
    var element = elementId && viewer.get('elementRegistry').get(elementId);
    if (element) {
      select(element);
    }
    // вписываем после панели: она отнимает ширину у холста
    viewer.get('canvas').zoom('fit-viewport', 'auto');
  }

  var eventNames = { completed: 'выполнен', activated: 'запущен', terminated: 'прерван' };

  // eventMarkers - маркер элемента по его последнему событию: в цикле элемент проходится несколько раз
  var eventMarkers = { completed: 'bpm-completed', activated: 'bpm-active', terminated: 'bpm-terminated' };

  // markRecords отмечает элементы и возвращает, дошёл ли сам экземпляр до End event
  function markRecords(records, instanceUid) {
    var canvas = viewer.get('canvas');
    var registry = viewer.get('elementRegistry');
    var lastEvent = new Map();
    var finished = false;

    records.forEach(function (record) {
      var element = registry.get(record.element_id);
      if (!element) {
        return;
      }
      lastEvent.set(record.element_id, record.event);
      if (record.event === 'completed' && element.type === 'bpmn:EndEvent' && record.instance_uid === instanceUid) {
        finished = true;
      }
    });

    lastEvent.forEach(function (event, id) {
      if (eventMarkers[event]) {
        canvas.addMarker(id, eventMarkers[event]);
      }
    });
    return finished;
  }

  function markLinks() {
    var canvas = viewer.get('canvas');
    var registry = viewer.get('elementRegistry');
    registry.getAll().forEach(function (element) {
      var id = element.id;
      if (subInstances(id).length) {
        canvas.addMarker(id, 'bpm-link');
      }
    });
  }

  function select(element) {
    var canvas = viewer.get('canvas');
    if (state.selected) {
      canvas.removeMarker(state.selected, 'bpm-selected');
    }
    state.selected = element.id;
    canvas.addMarker(element.id, 'bpm-selected');
    setElementParam(element.id);
    renderPanel(element);
  }

  function closePanel() {
    if (state.selected) {
      viewer.get('canvas').removeMarker(state.selected, 'bpm-selected');
      state.selected = null;
      setElementParam(null);
    }
    setPanelVisible(false);
  }

  function setElementParam(elementId) {
    var url = new URL(window.location.href);
    if (elementId) {
      url.searchParams.set('element', elementId);
    } else {
      url.searchParams.delete('element');
    }
    window.history.replaceState(null, '', url);
  }

  function renderPanel(element) {
    var bo = element.businessObject || {};
    panelTitle.textContent = bo.name || element.id;
    panelType.textContent = (element.type || '').replace('bpmn:', '') + (bo.name ? ' · ' + element.id : '');

    var nodes = [];
    var instances = subInstances(element.id);
    if (instances.length) {
      nodes.push(el('h2', instances.length === 1 ? 'Подпроцесс' : 'Экземпляры подпроцесса'));
      var list = el('ul', null, 'children');
      instances.forEach(function (instance) {
        list.appendChild(el('li', null, 'child', [
          el('div', null, null, [
            el('div', instance.process_name + ' · ' + shortId(instance.instance_uid)),
            el('div', 'запущен ' + formatTime(instance.started_at), 'muted'),
          ]),
          historyButton(instance.instance_uid),
        ]));
      });
      nodes.push(list);
    } else if (element.type === 'bpmn:CallActivity' || element.type === 'bpmn:SubProcess') {
      nodes.push(el('h2', 'Подпроцесс'));
      nodes.push(el('div', 'Экземпляр подпроцесса не найден в истории', 'muted'));
    }
    if (element.type === 'bpmn:BusinessRuleTask') {
      nodes.push(el('h2', 'Бизнес-правило'));
      var rulesButton = el('button', 'Показать сработавшие правила', 'button');
      rulesButton.type = 'button';
      rulesButton.addEventListener('click', function () {
        showRules(element);
      });
      nodes.push(rulesButton);
    }
    if (element.type === 'bpmn:UserTask') {
      nodes.push(el('h2', 'Назначение'));
      var assignment = el('div', 'Загрузка…', 'muted');
      nodes.push(assignment);
      showAssignment(element, assignment);
    }

    var records = state.recordsByElement[element.id] || [];
    nodes.push(el('h2', 'Проходы'));
    if (!records.length) {
      nodes.push(el('div', 'Элемент не выполнялся', 'muted'));
    }
    var instances = new Set((state.data.records || []).map(function (r) { return r.instance_uid; }));
    records.forEach(function (record) {
      var head = [
        el('span', eventNames[record.event] || record.event, 'pass-event'),
        el('span', formatTime(record.created_at), 'pass-time'),
      ];
      var body = [el('div', null, 'pass-head', head)];
      // на диаграмме бывают и экземпляры встроенных подпроцессов: показываем, чей это проход
      if (instances.size > 1) {
        body.push(el('div', record.process_name + ' · ' + shortId(record.instance_uid), 'muted'));
      }
      if (record.variables) {
        body.push(el('pre', JSON.stringify(record.variables, null, 2), 'vars'));
      } else {
        body.push(el('div', 'переменные пишутся только для старта процесса и задач', 'muted'));
      }
      nodes.push(el('div', null, 'pass', body));
    });

    panelBody.replaceChildren.apply(panelBody, nodes);
    setPanelVisible(true);
  }

  var taskStatuses = { created: 'ждёт исполнителя', claimed: 'в работе', completed: 'выполнена' };

  // showAssignment заполняет box задачами пользователя элемента: кому и когда назначена, кто может взять, кто выполнил.
  // Задача встроенного подпроцесса выполняется в его экземпляре, поэтому задачи берутся по всем экземплярам элемента
  async function showAssignment(element, box) {
    var instances = new Set((state.recordsByElement[element.id] || []).map(function (r) { return r.instance_uid; }));
    if (!instances.size) {
      box.textContent = 'Задача не создавалась';
      return;
    }
    var tasks = [];
    try {
      var responses = await Promise.all(Array.from(instances).map(async function (instanceId) {
        var resp = await fetch('api/tasks?instanceId=' + encodeURIComponent(instanceId) +
          '&elementId=' + encodeURIComponent(element.id));
        var data = await resp.json().catch(function () { return {}; });
        if (!resp.ok) {
          throw new Error(data.error || resp.status + ' ' + resp.statusText);
        }
        return data;
      }));
      responses.forEach(function (data) { tasks = tasks.concat(data.tasks || []); });
    } catch (err) {
      box.textContent = 'Не удалось загрузить задачи: ' + err.message;
      box.className = 'error';
      return;
    }
    if (!tasks.length) {
      box.textContent = 'Задача не создавалась';
      return;
    }
    // task_uid - UUID v7: порядок строк - порядок создания задач
    tasks.sort(function (a, b) { return a.task_uid < b.task_uid ? -1 : 1; });
    var cards = tasks.map(function (task, index) {
      var status = task.status === 'created' && task.assignee ? 'claimed' : task.status;
      var rows = [
        ['Исполнитель', task.assignee || 'не назначена'],
        ['Назначена', task.claimed_at ? formatTime(task.claimed_at) : '—'],
        ['Может взять', candidates(task)],
      ];
      if (task.status === 'completed') {
        rows.push(['Выполнил', task.completed_by + ' · ' + formatTime(task.completed_at)]);
      }
      if (task.form_key) {
        rows.push(['Форма', task.form_key]);
      }
      if (instances.size > 1) {
        rows.push(['Экземпляр', task.process_name + ' · ' + shortId(task.instance_uid)]);
      }
      var list = el('dl', null, 'assign');
      rows.forEach(function (row) {
        list.appendChild(el('dt', row[0]));
        list.appendChild(el('dd', row[1]));
      });
      return el('div', null, 'pass', [
        el('div', null, 'pass-head', [
          el('span', (tasks.length > 1 ? '#' + (index + 1) + ' · ' : '') + (taskStatuses[status] || status), 'pass-event'),
          el('span', 'создана ' + formatTime(task.created_at), 'pass-time'),
        ]),
        list,
      ]);
    });
    box.className = '';
    box.replaceChildren.apply(box, cards);
  }

  // candidates - кто может взять задачу; без кандидатов - только исполнитель, а без него - любой пользователь
  function candidates(task) {
    var parts = [];
    if (task.candidate_users && task.candidate_users.length) {
      parts.push('пользователи: ' + task.candidate_users.join(', '));
    }
    if (task.candidate_groups && task.candidate_groups.length) {
      parts.push('группы: ' + task.candidate_groups.join(', '));
    }
    if (!parts.length) {
      return task.assignee ? 'только исполнитель' : 'любой пользователь';
    }
    return parts.join('; ');
  }

  // subInstances - экземпляры, запущенные элементом: callActivity (children из API) и встроенного подпроцесса.
  // Встроенный подпроцесс нарисован на этой же диаграмме, его записи уже в records:
  // экземпляр подпроцесса - тот, чья первая запись ссылается на элемент родителя
  function subInstances(elementId) {
    var result = (state.childrenByElement[elementId] || []).slice();
    var seen = new Set();
    var current = state.data ? state.data.instance_uid : null;
    (state.data && state.data.records || []).forEach(function (record) {
      if (seen.has(record.instance_uid)) {
        return;
      }
      seen.add(record.instance_uid); // записи идут по порядку id: первая - старт экземпляра
      if (record.instance_uid !== current && record.parent_element_id === elementId) {
        result.push({
          instance_uid: record.instance_uid,
          process_name: record.process_name,
          element_id: elementId,
          started_at: record.created_at,
        });
      }
    });
    return result;
  }

  // showRules открывает окно с вызовами решения задачей element. Задача встроенного подпроцесса выполняется
  // в его экземпляре, поэтому история правил берётся по всем экземплярам, в которых элемент выполнялся
  async function showRules(element) {
    var request = ++rulesRequest;
    var bo = element.businessObject || {};
    rulesTitle.textContent = 'Сработавшие правила';
    rulesSubtitle.textContent = (bo.name ? bo.name + ' · ' : '') + element.id;
    rulesCalls.replaceChildren();
    rulesResult.hidden = true;
    rulesCanvas.hidden = true;
    showRulesMessage('Загрузка…');
    rulesDialog.showModal();

    var instances = new Set((state.recordsByElement[element.id] || []).map(function (r) { return r.instance_uid; }));
    if (!instances.size) {
      instances.add(state.data.instance_uid);
    }
    var records = [];
    var dmn = {};
    try {
      var responses = await Promise.all(Array.from(instances).map(async function (instanceId) {
        var resp = await fetch('api/rules?instanceId=' + encodeURIComponent(instanceId) +
          '&elementId=' + encodeURIComponent(element.id));
        var data = await resp.json().catch(function () { return {}; });
        if (!resp.ok) {
          throw new Error(data.error || resp.status + ' ' + resp.statusText);
        }
        return data;
      }));
      responses.forEach(function (data) {
        records = records.concat(data.records || []);
        Object.assign(dmn, data.dmn || {});
      });
    } catch (err) {
      if (request === rulesRequest) {
        showRulesMessage('Не удалось получить историю правил: ' + err.message);
      }
      return;
    }
    if (request !== rulesRequest) {
      return;
    }
    if (!records.length) {
      showRulesMessage('Вызовов решения нет в истории. Они пишутся в rules_history, когда включён флаг rules_history_db_enabled.');
      return;
    }
    records.sort(function (a, b) { return a.id - b.id; });

    var buttons = records.map(function (record, index) {
      var count = (record.rule_ids || []).length;
      var button = el('button', '#' + (index + 1) + ' · ' + formatTime(record.created_at) + ' · правил: ' + count, 'rules-call');
      button.type = 'button';
      button.addEventListener('click', function () {
        selectCall(request, buttons, button, record, dmn[record.decision_id]);
      });
      return button;
    });
    rulesCalls.replaceChildren.apply(rulesCalls, buttons);
    // последний вызов - текущее состояние задачи
    var last = records.length - 1;
    selectCall(request, buttons, buttons[last], records[last], dmn[records[last].decision_id]);
  }

  // selectCall показывает таблицу решения вызова record и подсвечивает его сработавшие правила
  async function selectCall(request, buttons, button, record, xml) {
    buttons.forEach(function (b) { b.setAttribute('aria-pressed', String(b === button)); });
    var ruleIds = record.rule_ids || [];
    rulesResult.textContent = 'решение ' + record.decision_id + ' · результат: ' + JSON.stringify(record.result, null, 2) +
      (ruleIds.length ? '' : '\nни одно правило не подошло');
    rulesResult.hidden = false;
    if (!xml) {
      rulesCanvas.hidden = true;
      showRulesMessage('DMN решения ' + record.decision_id + ' не найден: его больше нет в папке process.');
      return;
    }
    rulesMessage.hidden = true;
    rulesCanvas.hidden = false;
    try {
      if (!dmnViewer) {
        dmnViewer = new DmnJS({ container: rulesCanvas });
        // dmn-js перерисовывает таблицу при открытии вида, подсветку ставим после каждой отрисовки
        dmnViewer.on('views.changed', function () {
          markRules(dmnViewer.currentRuleIds || []);
        });
      }
      dmnViewer.currentRuleIds = ruleIds;
      if (dmnXml !== xml) {
        await dmnViewer.importXML(xml);
        dmnXml = xml;
      }
      var view = dmnViewer.getViews().find(function (v) {
        return v.type === 'decisionTable' && v.element.id === record.decision_id;
      });
      if (!view) {
        throw new Error('в DMN нет таблицы решения ' + record.decision_id);
      }
      if (dmnViewer.getActiveView() !== view) {
        await dmnViewer.open(view);
      }
      if (request !== rulesRequest) {
        return;
      }
      rulesSubtitle.textContent = rulesSubtitle.textContent.split(' → ')[0] + ' → ' + (view.element.name || view.element.id);
      markRules(ruleIds);
    } catch (err) {
      dmnXml = null;
      rulesCanvas.hidden = true;
      showRulesMessage('Не удалось отрисовать DMN: ' + err.message);
    }
  }

  // markRules подсвечивает ячейки строк сработавших правил: dmn-js помечает ячейки строки data-row-id
  function markRules(ruleIds) {
    var ids = new Set(ruleIds);
    rulesCanvas.querySelectorAll('td[data-row-id]').forEach(function (cell) {
      cell.classList.toggle('bpm-rule-hit', ids.has(cell.getAttribute('data-row-id')));
    });
  }

  function showRulesMessage(message) {
    rulesMessage.textContent = message;
    rulesMessage.hidden = false;
  }

  function historyButton(instanceId) {
    var button = el('button', 'Перейти в историю подпроцесса', 'button');
    button.type = 'button';
    button.addEventListener('click', function () {
      navigate(instanceId);
    });
    return button;
  }

  // setPanelVisible показывает или прячет панель и сообщает холсту о новом размере
  function setPanelVisible(visible) {
    if (panel.hidden === !visible) {
      return;
    }
    panel.hidden = !visible;
    viewer.get('canvas').resized();
  }

  function renderInfo(data, count, status) {
    info.replaceChildren(
      text('Процесс '), el('b', data.process_name),
      text(' · экземпляр '), el('code', data.instance_uid),
      text(' · записей: ' + count + ' · '),
      el('b', instanceStatuses[status] || status, 'status-' + status)
    );
  }

  // --- упавшие задачи и отмена ---------------------------------------------------------------------

  // withoutHistory - схема экземпляра без записей истории: экземпляр, упавший на первом шаге, записей не оставил
  // (шаг откатился), а разбирать его нужно именно здесь. Нет и строки экземпляра - ошибка истории как есть
  async function withoutHistory(instanceId, historyError) {
    var instance;
    try {
      instance = await api('GET', '/instances/' + encodeURIComponent(instanceId));
    } catch (err) {
      throw new Error(err.status === 404 ? historyError || err.message : err.message);
    }
    // встроенный подпроцесс "<процесс>.<подпроцесс>" нарисован в BPMN процесса
    var process = await api('GET', '/processes/' + encodeURIComponent(instance.process_name.split('.')[0]));
    return { instance_uid: instance.instance_uid, process_name: instance.process_name, bpmn: process.bpmn, records: [] };
  }

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
      var err = new Error(data.error || resp.status + ' ' + resp.statusText);
      err.status = resp.status;
      throw err;
    }
    return data;
  }

  // loadOperations показывает статус экземпляра из bpm_process_instance, упавшие задачи и кнопку отмены
  async function loadOperations(data) {
    var instanceId = data.instance_uid;
    var results = await Promise.all([
      // строки экземпляра нет, если история писалась без History.ToDB: тогда остаётся статус по истории
      api('GET', '/instances/' + instanceId).catch(function (err) {
        if (err.status === 404) {
          return null;
        }
        throw err;
      }),
      api('GET', '/instances/' + instanceId + '/failures'),
    ]);
    if (!state.data || state.data.instance_uid !== instanceId) {
      return; // пока ждали, открыли другой экземпляр
    }
    var instance = results[0];
    if (instance) {
      renderInfo(data, (data.records || []).length, instance.status);
    }
    var canCancel = instance && !instance.parent_instance_uid && (instance.status === 'running' || instance.status === 'failed');
    actionsBox.hidden = !canCancel;
    cancelConfirm.hidden = true;
    cancelButton.hidden = false;
    renderFailures(results[1].failures || [], instanceId);
  }

  function renderFailures(failures, instanceId) {
    var canvas = viewer.get('canvas');
    var registry = viewer.get('elementRegistry');
    registry.getAll().forEach(function (element) {
      canvas.removeMarker(element.id, 'bpm-failed');
    });
    failuresBox.replaceChildren();
    failuresBox.hidden = failures.length === 0;
    if (!failures.length) {
      return;
    }
    failuresBox.append(el('div', failures.length === 1 ? 'Упала задача — экземпляр стоит' : 'Упали задачи: ' + failures.length + ' — экземпляр стоит', 'failures-title'));
    failures.forEach(function (failure) {
      var element = failedElement(failure);
      // упавший элемент отмечается, если он на этой диаграмме: экземпляр callActivity нарисован в своём BPMN
      var onDiagram = failure.instance_uid === instanceId ||
        (state.data.records || []).some(function (record) { return record.instance_uid === failure.instance_uid; });
      if (onDiagram && registry.get(element)) {
        canvas.addMarker(element, 'bpm-failed');
      }
      var where = [el('b', element)];
      if (failure.instance_uid !== instanceId) {
        where.push(text(' в '), link(failure.instance_uid, failure.process_name + ' · ' + shortId(failure.instance_uid)));
      }
      var retry = el('button', 'Повторить', 'button');
      retry.type = 'button';
      retry.addEventListener('click', function () {
        retryFailure(failure, undefined, retry).catch(function (err) {
          showProblem('Не удалось перезапустить задачу: ' + err.message);
        });
      });
      var fix = el('button', 'Исправить переменные…', 'button-quiet');
      fix.type = 'button';
      fix.addEventListener('click', function () {
        openRetry(failure);
      });
      failuresBox.append(el('div', null, 'failure', [
        el('div', null, 'failure-main', [
          el('div', null, null, [text('Элемент ')].concat(where, [
            text(' · попыток: ' + failure.attempts + ' · ' + formatTime(failure.failed_at)),
          ])),
          el('pre', failure.error, 'failure-error'),
        ]),
        el('div', null, 'failure-actions', [retry, fix]),
      ]));
    });
  }

  // failedElement - где упало: движок пишет в ошибку шага 'element "<id>"', а токен задачи стоит на элементе,
  // с которого шаг начался
  function failedElement(failure) {
    var match = /element "([^"]+)"/.exec(failure.error || '');
    return match ? match[1] : failure.element_id;
  }

  async function retryFailure(failure, variables, button) {
    if (button) {
      button.disabled = true;
    }
    try {
      await api('POST', '/failures/' + failure.task_id + '/retry', variables === undefined ? {} : { variables: variables });
    } catch (err) {
      if (button) {
        button.disabled = false;
      }
      throw err;
    }
    // шаг выполнит очередь чуть позже: показываем результат через секунду
    failuresBox.replaceChildren(el('div', 'Задача возвращена в очередь, обновляем…', 'failures-title'));
    setTimeout(function () {
      show(state.data.instance_uid);
    }, 1000);
  }

  function openRetry(failure) {
    retrying = failure;
    document.getElementById('retry-subtitle').textContent = failedElement(failure) + ' · ' + failure.process_name;
    retryVars.value = JSON.stringify(failure.variables || {}, null, 2);
    retryError.hidden = true;
    retryDialog.showModal();
  }

  document.getElementById('retry-form').addEventListener('submit', async function (event) {
    event.preventDefault();
    var variables;
    try {
      variables = JSON.parse(retryVars.value);
      if (!variables || typeof variables !== 'object' || Array.isArray(variables)) {
        throw new Error('нужен JSON-объект');
      }
    } catch (err) {
      retryError.textContent = 'Переменные: ' + err.message;
      retryError.hidden = false;
      return;
    }
    try {
      await retryFailure(retrying, variables);
    } catch (err) {
      retryError.textContent = err.message;
      retryError.hidden = false;
      return;
    }
    retryDialog.close();
  });
  ['retry-close', 'retry-cancel'].forEach(function (id) {
    document.getElementById(id).addEventListener('click', function () {
      retryDialog.close();
    });
  });

  // отмена с подтверждением на странице: confirm() в браузере легко нажать не глядя
  cancelButton.addEventListener('click', function () {
    cancelButton.hidden = true;
    cancelConfirm.hidden = false;
  });
  document.getElementById('cancel-no').addEventListener('click', function () {
    cancelConfirm.hidden = true;
    cancelButton.hidden = false;
  });
  document.getElementById('cancel-yes').addEventListener('click', async function () {
    var instanceId = state.data.instance_uid;
    try {
      await api('POST', '/instances/' + instanceId + '/cancel');
    } catch (err) {
      showProblem('Не удалось отменить экземпляр: ' + err.message);
      return;
    }
    show(instanceId);
  });

  function showProblem(message) {
    errorBox.textContent = message;
    errorBox.hidden = false;
  }

  function renderParent(parent) {
    if (!parent) {
      parentBox.hidden = true;
      return;
    }
    parentBox.replaceChildren(
      text('← вызван из '),
      link(parent.instance_uid, (parent.process_name || 'экземпляра') + ' · ' + shortId(parent.instance_uid)),
      text(', элемент ' + parent.element_id)
    );
    parentBox.hidden = false;
  }

  function fail(message) {
    info.textContent = '';
    errorBox.textContent = message;
    errorBox.hidden = false;
    state = { data: null, recordsByElement: {}, childrenByElement: {}, selected: null };
    viewer.clear();
  }

  function link(instanceId, label) {
    var a = el('a', label);
    a.href = '?instanceId=' + encodeURIComponent(instanceId);
    a.addEventListener('click', function (event) {
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) {
        return; // открыть в новой вкладке - как обычная ссылка
      }
      event.preventDefault();
      navigate(instanceId);
    });
    return a;
  }

  // shortId - хвост id: начало UUID v7 - время, у родителя и подпроцесса оно совпадает
  function shortId(id) {
    return '…' + String(id).slice(-12);
  }

  function formatTime(value) {
    var date = new Date(value);
    if (isNaN(date)) {
      return value;
    }
    return date.toLocaleString('ru-RU') + '.' + String(date.getMilliseconds()).padStart(3, '0');
  }

  function el(tag, content, className, children) {
    var node = document.createElement(tag);
    if (content != null) {
      node.textContent = content;
    }
    if (className) {
      node.className = className;
    }
    (children || []).forEach(function (child) {
      node.appendChild(child);
    });
    return node;
  }

  function text(value) {
    return document.createTextNode(value);
  }
})();
