# Процессы демо

BPMN и DMN демо-сервиса. После изменения: `make generate` в корне репозитория.

| Файл | Процесс | Что показывает |
|---|---|---|
| `test-bpm-engine.bpmn` | `TestBpmProcess` | шлюз по `amount`, scriptTask, параллельные ветки со слиянием, service task, callActivity процесса `testSubProcess` |
| `testSubProcess.bpmn` | `testSubProcess` | параллельные service task (`paralel1`…`paralel4`) со слиянием |
| `testTimerAndException.bpmn` | `TestTimerAndException` | промежуточный таймер, граничные таймеры и ошибки |
| `testBusinessRule.bpmn` + `businessRule.dmn` | `TestDecision` | две задачи бизнес-правил подряд: `Decision_1`, `SubDecision` |
