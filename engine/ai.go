package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/nicolas-vp/go-bpm/bpm"
)

// AIRequest - запрос AI task к модели: промпты с подставленными переменными процесса
type AIRequest struct {
	// Model - модель из BPMN (gobpm:aiTask model), пусто - модель клиента по умолчанию
	Model        string
	SystemPrompt string
	Prompt       string
	// JSON - ответ пишется в поле-структуру (список, число): нужен JSON без пояснений вокруг
	JSON bool
	// Job - задача, которая спрашивает: экземпляр, элемент, попытка
	Job bpm.Job
}

// AIClient - модель, которой движок отправляет запросы AI task. Ошибка - как у обработчика service task:
// повтор до Retries, bpm.Error ловит граничное событие ошибки. Реализация - в сервисе: вызов нужной модели (Claude, OpenAI, локальная)
type AIClient interface {
	Complete(ctx context.Context, request AIRequest) (string, error)
}

// AIClientFunc - функция как AIClient, например заглушка в тестах
type AIClientFunc func(ctx context.Context, request AIRequest) (string, error)

func (f AIClientFunc) Complete(ctx context.Context, request AIRequest) (string, error) {
	return f(ctx, request)
}

// aiTaskTypes - jobType AI task всех процессов
func aiTaskTypes(deployment map[string]bpm.DeploymentProcess) map[string]bool {
	types := make(map[string]bool)
	for _, flow := range deployment {
		for _, expression := range flow.Expressions {
			if expression.AITask != nil {
				types[expression.TaskType] = true
			}
		}
	}
	return types
}

// aiHandler - обработчик jobType AI task: запрос к модели с промптами элемента, ответ - в ResultVariable
func (s *Engine) aiHandler(ctx context.Context, job bpm.Job, variables map[string]any) (map[string]any, error) {
	expression, err := s.getExpression(job.ProcessName, job.ElementId)
	if err != nil {
		return nil, err
	}
	task := expression.AITask
	if task == nil {
		return nil, fmt.Errorf("process %q: element %q is not an AI task", job.ProcessName, job.ElementId)
	}
	systemPrompt, err := renderPrompt(task.SystemPrompt, variables)
	if err != nil {
		return nil, fmt.Errorf("AI task %q: system prompt: %w", job.ElementId, err)
	}
	prompt, err := renderPrompt(task.Prompt, variables)
	if err != nil {
		return nil, fmt.Errorf("AI task %q: prompt: %w", job.ElementId, err)
	}
	if s.ai == nil {
		return nil, fmt.Errorf("AI task %q: engine.Config.AI is not set", job.ElementId)
	}
	answer, err := s.ai.Complete(ctx, AIRequest{
		Model:        task.Model,
		SystemPrompt: systemPrompt,
		Prompt:       prompt,
		JSON:         task.JSON,
		Job:          job,
	})
	if err != nil {
		return nil, err
	}
	var value any = answer
	if task.JSON {
		if value, err = parseJSONAnswer(answer); err != nil {
			return nil, fmt.Errorf("AI task %q: %w", job.ElementId, err)
		}
	}
	if variables == nil {
		variables = make(map[string]any)
	}
	if err := setVariable(variables, task.ResultVariable, value); err != nil {
		return nil, fmt.Errorf("AI task %q: %w", job.ElementId, err)
	}
	return variables, nil
}

// promptVariable - ${path} в промпте
var promptVariable = regexp.MustCompile(`\$\{\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*\}`)

// renderPrompt подставляет в промпт переменные ${path}: строка - как есть, остальное - JSON.
// Переменной нет - ошибка: модель получила бы промпт без данных
func renderPrompt(prompt string, variables map[string]any) (string, error) {
	var problems []string
	result := promptVariable.ReplaceAllStringFunc(prompt, func(placeholder string) string {
		path := promptVariable.FindStringSubmatch(placeholder)[1]
		value, ok := lookupVariable(variables, path)
		if !ok || value == nil {
			problems = append(problems, path)
			return placeholder
		}
		if text, ok := value.(string); ok {
			return text
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			problems = append(problems, path)
			return placeholder
		}
		return string(encoded)
	})
	if problems != nil {
		return "", fmt.Errorf("no variables %s", strings.Join(problems, ", "))
	}
	return result, nil
}

// parseJSONAnswer разбирает ответ модели как JSON; блок ```json ... ``` вокруг допускается
func parseJSONAnswer(answer string) (any, error) {
	text := strings.TrimSpace(answer)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimPrefix(text, "json")
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, fmt.Errorf("answer is not JSON: %w", err)
	}
	return value, nil
}

// setVariable записывает значение переменной по пути через точку, создавая вложенные объекты
func setVariable(variables map[string]any, path string, value any) error {
	names := strings.Split(path, ".")
	current := variables
	for i, name := range names[:len(names)-1] {
		next, found := current[name]
		if !found || next == nil {
			object := make(map[string]any)
			current[name] = object
			current = object
			continue
		}
		object, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("variable %q is %T, not an object", strings.Join(names[:i+1], "."), next)
		}
		current = object
	}
	current[names[len(names)-1]] = value
	return nil
}
