package codegen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
	"github.com/nicolas-vp/go-bpm/internal/generator/templates"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

// loopFilePrefix - условия завершения циклов лежат в пакете процесса файлами loop_<id элемента>.go
const loopFilePrefix = "loop_"

// activityLoops - multi-instance задач, callActivity и встроенных подпроцессов процесса по id элемента
func activityLoops(process *bpmn.Process) map[string]*bpmn.MultiInstance {
	loops := make(map[string]*bpmn.MultiInstance)
	add := func(id string, loop *bpmn.MultiInstance) {
		if loop != nil {
			loops[id] = loop
		}
	}
	for _, task := range process.Tasks {
		add(task.Id, task.MultiInstance)
	}
	for _, task := range process.ServiceTasks {
		add(task.Id, task.MultiInstance)
	}
	for _, task := range process.ScriptTasks {
		add(task.Id, task.MultiInstance)
	}
	for _, task := range process.BusinessRuleTasks {
		add(task.Id, task.MultiInstance)
	}
	for _, task := range process.UserTasks {
		add(task.Id, task.MultiInstance)
	}
	for _, callActivity := range process.CallActivityList {
		add(callActivity.Id, callActivity.MultiInstance)
	}
	for _, subProcess := range process.SubProcessList {
		add(subProcess.Id, subProcess.MultiInstance)
	}
	return loops
}

// createLoop - последовательный цикл элемента id: число выполнений - целая константа, условие завершения -
// выражение на переменных процесса, как условие шлюза. Параллельный multi-instance и цикл по коллекции не поддерживаются
func createLoop(id string, loop *bpmn.MultiInstance) (*model.Loop, error) {
	if !loop.IsSequential {
		return nil, fmt.Errorf("%s: параллельный multi-instance не поддерживается, только последовательный (isSequential=\"true\")", id)
	}
	if loop.Collection != "" {
		return nil, fmt.Errorf("%s: multi-instance по коллекции (camunda:collection) не поддерживается, только loopCardinality и completionCondition", id)
	}
	for _, characteristics := range loop.LoopCharacteristics {
		if characteristics.InputCollection != "" {
			return nil, fmt.Errorf("%s: multi-instance по коллекции (zeebe:loopCharacteristics inputCollection) не поддерживается, только loopCardinality и completionCondition", id)
		}
	}
	result := &model.Loop{
		CompletionCondition: strings.TrimSpace(removeFirstEqual(strings.TrimSpace(loop.CompletionCondition))),
	}
	if cardinality := strings.TrimSpace(removeFirstEqual(strings.TrimSpace(loop.LoopCardinality))); cardinality != "" {
		n, err := strconv.Atoi(cardinality)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%s: loopCardinality %q - нужно целое число больше нуля, выражения не поддерживаются", id, loop.LoopCardinality)
		}
		result.Cardinality = n
	}
	if result.Cardinality == 0 && result.CompletionCondition == "" {
		return nil, fmt.Errorf("%s: у multi-instance нужно число выполнений (loopCardinality) или условие завершения (completionCondition)", id)
	}
	if result.CompletionCondition != "" {
		result.ConditionFunc = utils.ToCamelCase(id) + "Completed"
	}
	return result, nil
}

// addLoops задаёт циклы элементам процесса. Вызывается до граничных событий: их проверка смотрит на цикл
func addLoops(expressions map[string]*model.Expression, process *bpmn.Process) error {
	for id, loop := range activityLoops(process) {
		expression, found := expressions[id]
		if !found {
			return fmt.Errorf("%s: элемент с multi-instance не найден", id)
		}
		var err error
		if expression.Loop, err = createLoop(id, loop); err != nil {
			return err
		}
	}
	return nil
}

type loopFile struct {
	DataModelImport string
	Package         string
	FuncName        string
	ElementId       string
	Expression      string
	// Source - условие как оно записано в процессе, если Expression от него отличается
	Source string
}

// prepareLoopFiles генерирует processPath/loop_<id элемента>.go для каждого цикла с условием завершения.
// Условие работает с моделью данных, как условие шлюза: недостающие поля попадают в gaps, и файл не пишется
func prepareLoopFiles(processes []model.DeploymentProcess, processId, processPath, pkg string,
	imports dataImports, m *modelPackage, gaps *modelGaps) error {
	t, err := template.New("loop").Parse(templates.LoopTemplate)
	if err != nil {
		return err
	}
	for _, process := range processes {
		for _, expression := range process.Expressions {
			if expression.Loop == nil || expression.Loop.ConditionFunc == "" {
				continue
			}
			ctx := exprContext{model: m, gaps: gaps, owner: "процесс " + processId + ": условие завершения цикла " + expression.ElementId}
			gapsBefore := gaps.len()
			source := expression.Loop.CompletionCondition
			code, err := ctx.rewrite(source, kindBool)
			if err != nil {
				return fmt.Errorf("loop %s: %w", expression.ElementId, err)
			}
			if gaps.len() > gapsBefore {
				continue
			}
			file := loopFile{
				DataModelImport: imports.DataModelImport,
				Package:         pkg,
				FuncName:        expression.Loop.ConditionFunc,
				ElementId:       expression.ElementId,
				Expression:      code,
			}
			if code != source {
				file.Source = source
			}
			var buf bytes.Buffer
			if err := t.Execute(&buf, file); err != nil {
				return err
			}
			path := filepath.Join(processPath, loopFilePrefix+utils.ToSnakeCase(expression.ElementId)+".go")
			if err := utils.SaveGoFile(path, buf.Bytes()); err != nil {
				return fmt.Errorf("loop %s: %w", expression.ElementId, err)
			}
		}
	}
	return nil
}

// loopTimerError - граничный таймер у элемента с циклом: в BPMN он ограничивает весь цикл, а движок ставит
// таймер на каждое выполнение, поэтому такое сочетание не поддерживается
func loopTimerError(event *bpmn.BoundaryEvent, task *model.Expression, boundary bpm.Boundary) error {
	if task.Loop == nil || boundary.Type != bpm.BoundaryTypeTimer {
		return nil
	}
	return fmt.Errorf("boundary event %s: граничный таймер у элемента %s с multi-instance не поддерживается, только событие ошибки", event.Id, task.ElementId)
}
