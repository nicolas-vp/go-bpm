package codegen

import (
	"fmt"
	"strings"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

// messageSubscription - на какое сообщение подписывается элемент elementId: имя bpmn:message messageRef и ключ
// корреляции из zeebe:subscription (переменная - "${path}"); без ключа движок берёт бизнес-ключ экземпляра
func messageSubscription(elementId, messageRef string, messages map[string]bpmn.Message) (*bpm.Message, error) {
	if messageRef == "" {
		return nil, fmt.Errorf("%s: не задано сообщение (messageRef)", elementId)
	}
	message, found := messages[messageRef]
	if !found {
		return nil, fmt.Errorf("%s: сообщение %s не найдено в definitions", elementId, messageRef)
	}
	name := strings.TrimSpace(message.Name)
	if name == "" || strings.HasPrefix(name, "=") {
		return nil, fmt.Errorf("%s: у сообщения %s нужно имя-константа, выражения не поддерживаются", elementId, messageRef)
	}
	subscription := &bpm.Message{Name: name}
	key := strings.TrimSpace(message.CorrelationKey())
	switch {
	case key == "":
	case strings.HasPrefix(key, "=") || strings.HasPrefix(key, "${") || strings.HasPrefix(key, "#{"):
		variable, err := assignmentValue(elementId, "correlationKey", key)
		if err != nil {
			return nil, fmt.Errorf("%s: correlationKey %q сообщения %s: только переменная =name, ${name} или константа", elementId, key, messageRef)
		}
		subscription.CorrelationKey = variable
	default:
		subscription.CorrelationKey = key
	}
	return subscription, nil
}

// CreateExpressionFromReceiveTask - receive task: токен ждёт сообщение и идёт дальше
func CreateExpressionFromReceiveTask(task *bpmn.ReceiveTask, process *bpmn.Process, messages map[string]bpmn.Message) (*model.Expression, error) {
	subscription, err := messageSubscription("receive task "+task.Id, task.MessageRef, messages)
	if err != nil {
		return nil, err
	}
	completeType, outgoings, condition := MapOutgoings(&task.OutgoingList, task.Default, process, false)
	return &model.Expression{
		ElementId:    task.Id,
		ActivateType: bpm.ActivateTypeMessage,
		CompleteType: completeType,
		NextElements: outgoings,
		Condition:    condition,
		Message:      subscription,
	}, nil
}
