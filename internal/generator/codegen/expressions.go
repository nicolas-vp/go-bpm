package codegen

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

// ParseFromProcess - получить выражения из процесса. definition - файл процесса: в нём bpmn:error и bpmn:message,
// на которые ссылаются события и задачи
func ParseFromProcess(process *bpmn.Process, queues *[]model.Queue, definition *bpmn.Definitions) (map[string]*model.Expression, error) {
	if err := prepareProcess(process); err != nil {
		return nil, err
	}
	errorCodes, messages := definition.ErrorCodes(), definition.MessagesById()
	expressions := make(map[string]*model.Expression)
	for _, task := range process.ServiceTasks {
		expression, err := CreateExpressionFromServiceTask(&task, process)
		if err != nil {
			return nil, err
		}
		expressions[task.Id] = expression
		*queues = append(*queues, model.Queue{
			TypeID:  expression.TaskType,
			Retries: task.GetRetries(),
			AI:      expression.AITask != nil,
		})
	}
	for _, task := range process.Tasks {
		expressions[task.Id] = CreateExpressionFromTask(&task, process)
	}
	for _, task := range process.ScriptTasks {
		expressions[task.Id] = CreateExpressionFromScriptTask(&task, process)
	}
	for _, task := range process.BusinessRuleTasks {
		expression, err := CreateExpressionFromBusinessRuleTask(&task, process)
		if err != nil {
			return nil, err
		}
		expressions[task.Id] = expression
	}
	for _, task := range process.UserTasks {
		expression, err := CreateExpressionFromUserTask(&task, process)
		if err != nil {
			return nil, err
		}
		expressions[task.Id] = expression
	}
	for _, task := range process.ReceiveTasks {
		expression, err := CreateExpressionFromReceiveTask(&task, process, messages)
		if err != nil {
			return nil, err
		}
		expressions[task.Id] = expression
	}
	for _, flow := range process.SequenceFlowList {
		expressions[flow.Id] = CreateExpressionFromSequenceFlow(&flow)
	}
	for _, event := range process.StartEvents {
		expressions[event.Id] = CreateExpressionFromStartEvent(&event, process)
	}
	for _, event := range process.EndEvents {
		expressions[event.Id] = CreateExpressionFromEndEvent(&event)
	}
	for _, gateway := range process.ExclusiveGateways {
		expressions[gateway.Id] = CreateExpressionFromExclusiveGateway(&gateway, process)
	}
	for _, gateway := range process.ParallelGateways {
		expressions[gateway.Id] = CreateExpressionFromParallelGateway(&gateway, process)
	}
	for _, subProcess := range process.SubProcessList {
		expressions[subProcess.Id] = CreateExpressionFromSubProcess(&subProcess, process)
	}
	for _, callActivity := range process.CallActivityList {
		expressions[callActivity.Id] = CreateExpressionFromCallActivity(&callActivity, process)
	}
	for _, event := range process.IntermediateCatchEvents {
		expression, err := CreateExpressionFromIntermediateCatchEvent(&event, process, messages)
		if err != nil {
			return nil, err
		}
		expressions[event.Id] = expression
	}
	if err := addLoops(expressions, process); err != nil {
		return nil, err
	}
	for _, event := range process.BoundaryEvents {
		if err := addBoundaryEvent(expressions, &event, process, errorCodes); err != nil {
			return nil, err
		}
	}
	for _, expression := range expressions {
		if expression.Condition != nil {
			expression.ConditionFunc = utils.ToCamelCase(expression.ElementId)
		}
		// порядок событий в BPMN не важен, а сгенерированный код не должен меняться от него
		sort.Slice(expression.Boundaries, func(i, j int) bool {
			return expression.Boundaries[i].ElementId < expression.Boundaries[j].ElementId
		})
	}
	return expressions, nil
}

// CreateExpressionFromIntermediateCatchEvent - промежуточное событие: таймер (timeDuration, timeDate) или сообщение
func CreateExpressionFromIntermediateCatchEvent(event *bpmn.IntermediateCatchEvent, process *bpmn.Process, messages map[string]bpmn.Message) (*model.Expression, error) {
	if other := bpmn.UnsupportedDefinition(event.OtherDefinitions); other != "" {
		return nil, fmt.Errorf("intermediate catch event %s: %s не поддерживается, только таймер и сообщение", event.Id, other)
	}
	if len(event.MessageEventDefinitions) == 1 && len(event.TimerEventDefinitions) == 0 {
		subscription, err := messageSubscription("intermediate catch event "+event.Id, event.MessageEventDefinitions[0].MessageRef, messages)
		if err != nil {
			return nil, err
		}
		completeType, outgoings, condition := MapOutgoings(&event.OutgoingList, "", process, false)
		return &model.Expression{
			ElementId:    event.Id,
			ActivateType: bpm.ActivateTypeMessage,
			CompleteType: completeType,
			NextElements: outgoings,
			Condition:    condition,
			Message:      subscription,
		}, nil
	}
	if len(event.TimerEventDefinitions) != 1 || len(event.MessageEventDefinitions) != 0 {
		return nil, fmt.Errorf("intermediate catch event %s: нужно одно определение события, таймер или сообщение", event.Id)
	}
	timer, date, err := timerSpec(event.Id, event.TimerEventDefinitions[0])
	if err != nil {
		return nil, err
	}
	completeType, outgoings, condition := MapOutgoings(&event.OutgoingList, "", process, false)
	return &model.Expression{
		ElementId:    event.Id,
		ActivateType: bpm.ActivateTypeTimer,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
		Timer:        timer,
		TimerDate:    date,
	}, nil
}

// addBoundaryEvent добавляет граничное событие в процесс отдельным элементом (по нему токен уходит дальше)
// и в Boundaries задачи, к которой оно прикреплено
func addBoundaryEvent(expressions map[string]*model.Expression, event *bpmn.BoundaryEvent, process *bpmn.Process, errorCodes map[string]string) error {
	task, found := expressions[event.AttachedToRef]
	if !found {
		return fmt.Errorf("boundary event %s: элемент %s не найден", event.Id, event.AttachedToRef)
	}
	userTask := task.ActivateType == bpm.ActivateTypeUserTask
	if task.ActivateType != bpm.ActivateTypeMethod && task.ActivateType != bpm.ActivateTypeQueue && !userTask {
		return fmt.Errorf("boundary event %s: граничные события поддерживаются только у task, scriptTask, serviceTask и userTask, а %s - нет", event.Id, task.ElementId)
	}
	if !event.Interrupting() {
		return fmt.Errorf("boundary event %s: непрерывающие события (cancelActivity=false) не поддерживаются", event.Id)
	}
	if other := bpmn.UnsupportedDefinition(event.OtherDefinitions); other != "" {
		return fmt.Errorf("boundary event %s: %s не поддерживается, только таймер и ошибка", event.Id, other)
	}

	boundary := bpm.Boundary{ElementId: event.Id}
	switch {
	case len(event.TimerEventDefinitions) == 1 && len(event.ErrorEventDefinitions) == 0:
		timer, date, err := timerSpec(event.Id, event.TimerEventDefinitions[0])
		if err != nil {
			return err
		}
		boundary.Type = bpm.BoundaryTypeTimer
		boundary.Timer = timer
		boundary.TimerDate = date
	case len(event.ErrorEventDefinitions) == 1 && len(event.TimerEventDefinitions) == 0:
		boundary.Type = bpm.BoundaryTypeError
		if ref := event.ErrorEventDefinitions[0].ErrorRef; ref != "" {
			code, found := errorCodes[ref]
			if !found {
				return fmt.Errorf("boundary event %s: ошибка %s не найдена в definitions", event.Id, ref)
			}
			// ошибка без errorCode ловит любую, как и событие без errorRef
			boundary.ErrorCode = code
		}
	default:
		return fmt.Errorf("boundary event %s: нужно одно определение события, таймер или ошибка", event.Id)
	}
	// задача пользователя не возвращает ошибок: её завершает человек через API
	if userTask && boundary.Type != bpm.BoundaryTypeTimer {
		return fmt.Errorf("boundary event %s: у userTask %s поддерживается только граничный таймер", event.Id, task.ElementId)
	}
	if err := loopTimerError(event, task, boundary); err != nil {
		return err
	}
	task.Boundaries = append(task.Boundaries, boundary)

	completeType, outgoings, condition := MapOutgoings(&event.OutgoingList, "", process, false)
	expressions[event.Id] = &model.Expression{
		ElementId:    event.Id,
		ActivateType: bpm.ActivateTypeBoundary,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
	}
	return nil
}

// timerSpec - длительность (timeDuration) или срок (timeDate) таймера. Срок - дата и время RFC 3339 с зоной
// или переменная процесса (=name, ${name}) с такой строкой, вычисляется движком при приходе токена (bpm.Boundary.TimerDate).
// Цикл (timeCycle) не поддерживается
func timerSpec(eventId string, timer bpmn.TimerEventDefinition) (time.Duration, string, error) {
	duration, date := strings.TrimSpace(timer.TimeDuration), strings.TrimSpace(timer.TimeDate)
	switch {
	case duration != "" && date == "":
		parsed, err := utils.ParseISODuration(duration)
		if err != nil {
			return 0, "", fmt.Errorf("event %s: %w", eventId, err)
		}
		return parsed, "", nil
	case date != "" && duration == "":
		value, err := timerDate(eventId, date)
		return 0, value, err
	default:
		return 0, "", fmt.Errorf("event %s: поддерживается таймер с длительностью (timeDuration) или датой (timeDate), без цикла", eventId)
	}
}

// timerDate переводит timeDate в форму контракта: дата RFC 3339 как есть, переменная - "${path}"
func timerDate(eventId, value string) (string, error) {
	if strings.HasPrefix(value, "=") || strings.HasPrefix(value, "${") || strings.HasPrefix(value, "#{") {
		variable, err := assignmentValue(eventId, "timeDate", value)
		if err != nil {
			return "", fmt.Errorf("event %s: timeDate %q: только дата RFC 3339 или переменная =name, ${name}", eventId, value)
		}
		return variable, nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return "", fmt.Errorf("event %s: timeDate %q: нужна дата и время RFC 3339 с зоной, например 2026-12-01T09:00:00+03:00, или переменная =name", eventId, value)
	}
	return value, nil
}

// CreateExpressionFromServiceTask - service task: задача очереди jobType, выполняется асинхронно обработчиком
// из handlers/. AI task (gobpm:aiTask) - тоже задача очереди, но её выполняет движок запросом к модели
func CreateExpressionFromServiceTask(task *bpmn.ServiceTask, process *bpmn.Process) (*model.Expression, error) {
	jobType := strings.TrimSpace(task.GetType())
	var aiTask *bpm.AITask
	if ai, ok := task.GetAiTask(); ok {
		var err error
		if aiTask, err = createAiTask(task.Id, ai); err != nil {
			return nil, err
		}
		if jobType == "" {
			jobType = DefaultAiJobType
		}
	}
	if jobType == "" {
		return nil, fmt.Errorf("service task %s: не задан тип задачи (gobpm:taskDefinition или zeebe:taskDefinition type)", task.Id)
	}
	completeType, outgoings, condition := MapOutgoings(&task.OutgoingList, task.Default, process, false)
	return &model.Expression{
		ElementId:    task.Id,
		ActivateType: bpm.ActivateTypeQueue,
		CompleteType: completeType,
		TaskType:     jobType,
		NextElements: outgoings,
		Condition:    condition,
		AITask:       aiTask,
	}, nil
}

// CreateExpressionFromTask - обычная задача, выполняется синхронно методом из methods/
func CreateExpressionFromTask(task *bpmn.Task, process *bpmn.Process) *model.Expression {
	completeType, outgoings, condition := MapOutgoings(&task.OutgoingList, task.Default, process, false)
	return &model.Expression{
		ElementId:    task.Id,
		ActivateType: bpm.ActivateTypeMethod,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
		MethodFunc:   utils.ToPascalCase(task.Id),
	}
}

// CreateExpressionFromScriptTask - скриптовая задача, выполняется синхронно сгенерированным методом из methods/
func CreateExpressionFromScriptTask(task *bpmn.ScriptTask, process *bpmn.Process) *model.Expression {
	completeType, outgoings, condition := MapOutgoings(&task.OutgoingList, task.Default, process, false)
	return &model.Expression{
		ElementId:    task.Id,
		ActivateType: bpm.ActivateTypeMethod,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
		MethodFunc:   utils.ToPascalCase(task.Id),
	}
}

// CreateExpressionFromBusinessRuleTask - задача бизнес-правила, выполняется синхронно сгенерированным методом,
// который вызывает решение DMN и пишет его результат в resultVariable
func CreateExpressionFromBusinessRuleTask(task *bpmn.BusinessRuleTask, process *bpmn.Process) (*model.Expression, error) {
	if task.HasTaskDefinition() {
		return nil, fmt.Errorf("business rule task %s: реализация обработчиком (job worker) не поддерживается, только вызов решения DMN", task.Id)
	}
	called := task.GetCalledDecision()
	if called.DecisionId == "" || called.ResultVariable == "" {
		return nil, fmt.Errorf("business rule task %s: нужно решение DMN (zeebe:calledDecision) с decisionId и resultVariable", task.Id)
	}
	completeType, outgoings, condition := MapOutgoings(&task.OutgoingList, task.Default, process, false)
	return &model.Expression{
		ElementId:    task.Id,
		ActivateType: bpm.ActivateTypeMethod,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
		MethodFunc:   utils.ToPascalCase(task.Id),
	}, nil
}

func CreateExpressionFromSequenceFlow(flow *bpmn.SequenceFlow) *model.Expression {
	return &model.Expression{
		ElementId:    flow.Id,
		ActivateType: bpm.ActivateTypeFlow,
		CompleteType: bpm.CompleteTypeStraight,
		NextElements: MapOutgoingElementsSequenceFlow(flow),
	}
}

func CreateExpressionFromStartEvent(event *bpmn.StartEvent, process *bpmn.Process) *model.Expression {
	completeType, outgoings, condition := MapOutgoings(&event.OutgoingList, event.Default, process, false)
	return &model.Expression{
		ElementId:    event.Id,
		ActivateType: bpm.ActivateTypeFlow,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
	}
}

func CreateExpressionFromEndEvent(event *bpmn.EndEvent) *model.Expression {
	return &model.Expression{
		ElementId:    event.Id,
		ActivateType: bpm.ActivateTypeEnd,
		CompleteType: bpm.CompleteTypeStraight,
		NextElements: []string{},
	}
}

func CreateExpressionFromExclusiveGateway(gateway *bpmn.ExclusiveGateway, process *bpmn.Process) *model.Expression {
	completeType, outgoings, condition := MapOutgoings(&gateway.OutgoingList, gateway.Default, process, false)
	return &model.Expression{
		ElementId:    gateway.Id,
		ActivateType: bpm.ActivateTypeFlow,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
	}
}

func CreateExpressionFromParallelGateway(gateway *bpmn.ParallelGateway, process *bpmn.Process) *model.Expression {
	length := len(gateway.IncomingList)
	activateType := bpm.ActivateTypeFlow
	if length > 1 {
		activateType = bpm.ActivateTypeSyncQueue
	}
	// токен уходит во все исходящие ветки, в том числе после слияния: шлюз может и сливать, и разветвлять
	completeType, outgoings, condition := MapOutgoings(&gateway.OutgoingList, "", process, true)
	return &model.Expression{
		ElementId:            gateway.Id,
		ActivateType:         activateType,
		CompleteType:         completeType,
		NextElements:         outgoings,
		Condition:            condition,
		ParallelSyncQuantity: length,
	}
}

func CreateExpressionFromSubProcess(subProcess *bpmn.Process, process *bpmn.Process) *model.Expression {
	completeType, outgoings, condition := MapOutgoings(&subProcess.OutgoingList, "", process, true)
	return &model.Expression{
		ElementId:    subProcess.Id,
		ActivateType: bpm.ActivateTypeStartSubprocess,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
		ProcessId:    process.Id + "." + subProcess.Id,
	}
}

// CreateExpressionFromCallActivity - callActivity запускает отдельный экземпляр процесса и ждёт его завершения
func CreateExpressionFromCallActivity(callActivity *bpmn.CallActivity, process *bpmn.Process) *model.Expression {
	completeType, outgoings, condition := MapOutgoings(&callActivity.OutgoingList, "", process, true)
	calledElement := callActivity.GetCalledElement()
	return &model.Expression{
		ElementId:               callActivity.Id,
		ActivateType:            bpm.ActivateTypeCallActivity,
		CompleteType:            completeType,
		NextElements:            outgoings,
		Condition:               condition,
		ProcessId:               calledElement.ProcessId,
		PropagateChildVariables: calledElement.PropagateChildVariables(),
	}
}

func MapOutgoingElementsSequenceFlow(s *bpmn.SequenceFlow) []string {
	return []string{s.TargetRef}
}
