// AI task на схемах консоли: service task с расширением gobpm:aiTask рисуется как в моделере go-bpm -
// обычная задача без шестерёнки, на её месте иконка «звёздочки» (lucide Sparkles, ISC). Модуль bpmn-js
// для additionalModules: window.goBpmAiTask.module, проверка элемента - window.goBpmAiTask.isAiTask
(function () {
  'use strict';

  var GOBPM_NAMESPACE = 'https://github.com/nicolas-vp/go-bpm/schema/1.0';
  var SVG_NAMESPACE = 'http://www.w3.org/2000/svg';

  // lucide Sparkles 1.66.0
  var SPARKLES = [
    ['path', { d: 'M11.017 2.814a1 1 0 0 1 1.966 0l1.051 5.558a2 2 0 0 0 1.594 1.594l5.558 1.051a1 1 0 0 1 0 1.966l-5.558 1.051a2 2 0 0 0-1.594 1.594l-1.051 5.558a1 1 0 0 1-1.966 0l-1.051-5.558a2 2 0 0 0-1.594-1.594l-5.558-1.051a1 1 0 0 1 0-1.966l5.558-1.051a2 2 0 0 0 1.594-1.594z' }],
    ['path', { d: 'M20 2v4' }],
    ['path', { d: 'M22 4h-4' }],
    ['circle', { cx: '4', cy: '20', r: '2' }],
  ];

  // isGoBpm - расширение пространства имён go-bpm. Консоль не знает схему gobpm, и bpmn-js читает расширение
  // как общий элемент: имя и пространство имён - в $descriptor.ns. Префикс без xmlns - как в генераторе
  function isGoBpm(value, localName) {
    var ns = value && value.$descriptor && value.$descriptor.ns;
    return !!ns && ns.localName === localName && (ns.uri === GOBPM_NAMESPACE || (!ns.uri && ns.prefix === 'gobpm'));
  }

  // isAiTask - service task с gobpm:aiTask; zeebe:aiTask генератор не читает, такая задача - обычная
  function isAiTask(element) {
    var bo = element && (element.businessObject || element);
    if (!bo || bo.$type !== 'bpmn:ServiceTask') {
      return false;
    }
    var extensions = bo.extensionElements;
    var values = (extensions && extensions.values) || [];
    return values.some(function (value) {
      return isGoBpm(value, 'aiTask');
    });
  }

  function svg(tag, attrs) {
    var node = document.createElementNS(SVG_NAMESPACE, tag);
    Object.keys(attrs).forEach(function (name) {
      node.setAttribute(name, attrs[name]);
    });
    return node;
  }

  // AiTaskRenderer - отрисовщик выше BpmnRenderer (1000), как BaseRenderer diagram-js: в собранном
  // bpmn-js его класса нет, поэтому подписка на render.* напрямую
  function AiTaskRenderer(eventBus, bpmnRenderer) {
    eventBus.on('render.shape', 1500, function (event, context) {
      var element = context.element;
      if (element.labelTarget || !isAiTask(element)) {
        return;
      }
      var parent = context.gfx;
      var shape = bpmnRenderer.handlers['bpmn:Task'](parent, element, context.attrs);
      var stroke = shape.getAttribute('stroke') || 'black';
      var icon = svg('g', {
        transform: 'translate(6 6) scale(0.75)',
        fill: 'none',
        stroke: stroke,
        'stroke-width': 2,
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
      });
      SPARKLES.forEach(function (node) {
        icon.appendChild(svg(node[0], node[1]));
      });
      parent.appendChild(icon);
      return shape;
    });
    eventBus.on('render.getShapePath', 1500, function (event, element) {
      if (!element.labelTarget && isAiTask(element)) {
        return bpmnRenderer.getShapePath(element);
      }
    });
  }
  AiTaskRenderer.$inject = ['eventBus', 'bpmnRenderer'];

  window.goBpmAiTask = {
    isAiTask: isAiTask,
    module: {
      __init__: ['goBpmAiTaskRenderer'],
      goBpmAiTaskRenderer: ['type', AiTaskRenderer],
    },
  };
})();
