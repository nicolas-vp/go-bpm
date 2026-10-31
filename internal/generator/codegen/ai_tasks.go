package codegen

import (
	"fmt"
	"go/ast"
	"regexp"
	"strings"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

// DefaultAiJobType - jobType AI task без gobpm:taskDefinition. Моделер пишет type="ai" сам
const DefaultAiJobType = "ai"

// promptPlaceholder - переменная в тексте промпта: ${path}
var promptPlaceholder = regexp.MustCompile(`\$\{([^}]*)\}`)

// createAiTask переводит gobpm:aiTask в контракт: промпт =path - "${path}", текст - как есть, переменные
// ${path} в тексте остаются, их подставит движок. Обязательны промпт и переменная результата
func createAiTask(taskId string, ai bpmn.AiTask) (*bpm.AITask, error) {
	prompt, err := promptValue(taskId, "prompt", ai.Prompt)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("AI task %s: не задан промпт (gobpm:aiTask prompt)", taskId)
	}
	systemPrompt, err := promptValue(taskId, "systemPrompt", ai.SystemPrompt)
	if err != nil {
		return nil, err
	}
	resultVariable := strings.TrimSpace(ai.ResultVariable)
	if resultVariable == "" {
		return nil, fmt.Errorf("AI task %s: не задана переменная результата (gobpm:aiTask resultVariable)", taskId)
	}
	if !assignmentVariable.MatchString(resultVariable) {
		return nil, fmt.Errorf("AI task %s: resultVariable %q - не имя переменной", taskId, resultVariable)
	}
	return &bpm.AITask{
		Model:          strings.TrimSpace(ai.Model),
		SystemPrompt:   systemPrompt,
		Prompt:         prompt,
		ResultVariable: resultVariable,
	}, nil
}

// promptValue - промпт в форме контракта: =path - вся переменная "${path}", иначе текст как есть,
// с переменными ${path} внутри. FEEL-выражения (="a" + b) не поддерживаются
func promptValue(taskId, attribute, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "=") {
		path := strings.TrimSpace(trimmed[1:])
		if !assignmentVariable.MatchString(path) {
			return "", fmt.Errorf("AI task %s: %s %q: выражение не поддерживается, только =переменная или текст с ${переменная}", taskId, attribute, value)
		}
		return "${" + path + "}", nil
	}
	for _, match := range promptPlaceholder.FindAllStringSubmatch(value, -1) {
		if !assignmentVariable.MatchString(strings.TrimSpace(match[1])) {
			return "", fmt.Errorf("AI task %s: %s: %s - не переменная, в тексте поддерживается только ${путь.через.точку}", taskId, attribute, match[0])
		}
	}
	return value, nil
}

// promptVariables - пути переменных ${path} в промпте
func promptVariables(prompt string) []string {
	var paths []string
	for _, match := range promptPlaceholder.FindAllStringSubmatch(prompt, -1) {
		paths = append(paths, strings.TrimSpace(match[1]))
	}
	return paths
}

// checkAiTasks сверяет AI task процесса с моделью данных: переменные промптов и переменная результата - поля
// DataModel. По типу поля результата решается, разбирать ли ответ модели как JSON: строка (и any) - текст как
// есть, остальное (структура, список, число) - JSON
func checkAiTasks(processes []model.DeploymentProcess, processId string, m *modelPackage, gaps *modelGaps) {
	for _, process := range processes {
		for _, expression := range process.Expressions {
			ai := expression.AITask
			if ai == nil {
				continue
			}
			owner := "процесс " + processId + ": AI task " + expression.ElementId
			for _, path := range append(promptVariables(ai.SystemPrompt), promptVariables(ai.Prompt)...) {
				if _, gap := m.resolvePath(strings.Split(path, ".")); gap != nil {
					gaps.add(owner, *gap, "string")
				}
			}
			segments := strings.Split(ai.ResultVariable, ".")
			resolved, gap := m.resolvePath(segments)
			if gap != nil {
				gaps.add(owner, *gap, "string")
				continue
			}
			ai.JSON = m.isModelPath(segments) && !isTextField(resolved.leaf)
		}
	}
}

// isModelPath - каждый сегмент пути - поле структуры модели. Путь внутри типа не из модели (map) - нет: тип
// последнего сегмента неизвестен
func (m *modelPackage) isModelPath(segments []string) bool {
	current := dataModelType
	for _, segment := range segments {
		if current == "" {
			return false
		}
		field, ok := m.field(current, segment)
		if !ok {
			return false
		}
		current, _ = m.structName(field.Type)
	}
	return true
}

// isTextField - поле принимает ответ модели как есть: строка, указатель на строку, any
func isTextField(t ast.Expr) bool {
	if leafKind(t) == kindString {
		return true
	}
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	switch e := t.(type) {
	case *ast.InterfaceType:
		return true
	case *ast.Ident:
		return e.Name == "any"
	}
	return false
}
