package codegen

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

// assignmentVariable - переменная процесса в назначении: путь через точку
var assignmentVariable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// CreateExpressionFromUserTask - задача пользователя: движок создаёт её и ждёт завершения через API
func CreateExpressionFromUserTask(task *bpmn.UserTask, process *bpmn.Process) (*model.Expression, error) {
	assignment := task.GetAssignment()
	assignee, err := assignmentValue(task.Id, "assignee", assignment.Assignee)
	if err != nil {
		return nil, err
	}
	candidateUsers, err := assignmentList(task.Id, "candidateUsers", assignment.CandidateUsers)
	if err != nil {
		return nil, err
	}
	candidateGroups, err := assignmentList(task.Id, "candidateGroups", assignment.CandidateGroups)
	if err != nil {
		return nil, err
	}
	completeType, outgoings, condition := MapOutgoings(&task.OutgoingList, task.Default, process, false)
	return &model.Expression{
		ElementId:    task.Id,
		ActivateType: bpm.ActivateTypeUserTask,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
		UserTask: &bpm.UserTask{
			Name:            strings.TrimSpace(task.Name),
			Assignee:        assignee,
			CandidateUsers:  candidateUsers,
			CandidateGroups: candidateGroups,
			FormKey:         task.GetFormKey(),
		},
	}, nil
}

// assignmentValue переводит значение назначения в форму контракта: литерал как есть, переменная - "${path}".
// Переменная пишется как в Camunda 7 (${path}, #{path}) или как в Zeebe (=path); другие выражения не поддерживаются
func assignmentValue(taskId, attribute, value string) (string, error) {
	value = strings.TrimSpace(value)
	var expression string
	switch {
	case strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}"), strings.HasPrefix(value, "#{") && strings.HasSuffix(value, "}"):
		expression = value[2 : len(value)-1]
	case strings.HasPrefix(value, "="):
		expression = value[1:]
	case strings.ContainsAny(value, "${}#"):
		return "", fmt.Errorf("user task %s: %s %q: выражение не поддерживается, только переменная ${name}, #{name} или =name", taskId, attribute, value)
	default:
		return value, nil
	}
	expression = strings.TrimSpace(expression)
	if !assignmentVariable.MatchString(expression) {
		return "", fmt.Errorf("user task %s: %s %q: выражение не поддерживается, только переменная ${name}, #{name} или =name", taskId, attribute, value)
	}
	return "${" + expression + "}", nil
}

// assignmentList - список через запятую или одна переменная, в которой строка или список строк
func assignmentList(taskId, attribute, value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	// =[...] FEEL-список и выражение с запятой внутри не делятся по запятым: их отвергнет assignmentValue
	if strings.HasPrefix(value, "=") {
		item, err := assignmentValue(taskId, attribute, value)
		if err != nil {
			return nil, err
		}
		return []string{item}, nil
	}
	var items []string
	for _, part := range strings.Split(value, ",") {
		item, err := assignmentValue(taskId, attribute, part)
		if err != nil {
			return nil, err
		}
		if item != "" {
			items = append(items, item)
		}
	}
	return items, nil
}
