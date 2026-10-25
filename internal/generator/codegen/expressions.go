package codegen

import (
	"fmt"
	"sort"
	"time"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

// ParseFromProcess - получить выражения из процесса. errorCodes - коды bpmn:error по id из definitions
func ParseFromProcess(process *bpmn.Process, queues *[]model.Queue, errorCodes map[string]string) (map[string]*model.Expression, error) {
	expressions := make(map[string]*model.Expression)
	for _, task := range process.ServiceTasks {
		expressions[task.Id] = CreateExpressionFromServiceTask(&task, process)
		*queues = append(*queues, model.Queue{
			TypeID:  task.GetType(),
			Retries: task.GetRetries(),
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
		expression, err := CreateExpressionFromIntermediateCatchEvent(&event, process)
		if err != nil {
			return nil, err
		}
		expressions[event.Id] = expression
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

// CreateExpressionFromIntermediateCatchEvent - промежуточный таймер: токен ждёт timeDuration и идёт дальше
func CreateExpressionFromIntermediateCatchEvent(event *bpmn.IntermediateCatchEvent, process *bpmn.Process) (*model.Expression, error) {
	if other := bpmn.UnsupportedDefinition(event.OtherDefinitions); other != "" {
		return nil, fmt.Errorf("intermediate catch event %s: %s не поддерживается, только таймер", event.Id, other)
	}
	if len(event.TimerEventDefinitions) != 1 {
		return nil, fmt.Errorf("intermediate catch event %s: поддерживается только таймер", event.Id)
	}
	timer, err := timerDuration(event.Id, event.TimerEventDefinitions[0])
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
	}, nil
}

// addBoundaryEvent добавляет граничное событие в процесс отдельным элементом (по нему токен уходит дальше)
// и в Boundaries задачи, к которой оно прикреплено
func addBoundaryEvent(expressions map[string]*model.Expression, event *bpmn.BoundaryEvent, process *bpmn.Process, errorCodes map[string]string) error {
	task, found := expressions[event.AttachedToRef]
	if !found {
		return fmt.Errorf("boundary event %s: элемент %s не найден", event.Id, event.AttachedToRef)
	}
	if task.ActivateType != bpm.ActivateTypeMethod && task.ActivateType != bpm.ActivateTypeQueue {
		return fmt.Errorf("boundary event %s: граничные события поддерживаются только у task, scriptTask и serviceTask, а %s - нет", event.Id, task.ElementId)
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
		timer, err := timerDuration(event.Id, event.TimerEventDefinitions[0])
		if err != nil {
			return err
		}
		boundary.Type = bpm.BoundaryTypeTimer
		boundary.Timer = timer
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

func timerDuration(eventId string, timer bpmn.TimerEventDefinition) (time.Duration, error) {
	if timer.TimeDuration == "" {
		return 0, fmt.Errorf("event %s: поддерживается только таймер с длительностью (timeDuration), без даты и цикла", eventId)
	}
	duration, err := utils.ParseISODuration(timer.TimeDuration)
	if err != nil {
		return 0, fmt.Errorf("event %s: %w", eventId, err)
	}
	return duration, nil
}

func CreateExpressionFromServiceTask(task *bpmn.ServiceTask, process *bpmn.Process) *model.Expression {
	completeType, outgoings, condition := MapOutgoings(&task.OutgoingList, task.Default, process, false)
	return &model.Expression{
		ElementId:    task.Id,
		ActivateType: bpm.ActivateTypeQueue,
		CompleteType: completeType,
		TaskType:     task.GetType(),
		NextElements: outgoings,
		Condition:    condition,
	}
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
	var isParallel bool
	var activateType bpm.ActivateType
	if length > 1 {
		isParallel = false
		activateType = bpm.ActivateTypeSyncQueue
	} else {
		isParallel = true
		activateType = bpm.ActivateTypeFlow
	}
	completeType, outgoings, condition := MapOutgoings(&gateway.OutgoingList, "", process, isParallel)
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
	calledElement := getCalledElement(callActivity)
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

func getCalledElement(callActivity *bpmn.CallActivity) bpmn.CalledElement {
	for _, element := range callActivity.ExtensionElementList {
		for _, ext := range element.CalledElements {
			return ext
		}
	}
	return bpmn.CalledElement{}
}

func MapOutgoingElementsSequenceFlow(s *bpmn.SequenceFlow) []string {
	return []string{s.TargetRef}
}
