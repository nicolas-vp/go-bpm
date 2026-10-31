// Package aistub - заглушка модели для AI task демо (engine.AIClient): отвечает без запросов наружу.
// В своём сервисе реализуйте engine.AIClient вызовом нужной модели
package aistub

import (
	"context"

	"github.com/nicolas-vp/go-bpm/engine"
)

// Client отвечает на AI task TestAiTask: текстом - категорией, JSON - срочностью обращения
type Client struct {
	// Text - ответ на запрос текстом, по умолчанию "other"
	Text string
	// JSON - ответ на запрос JSON (поле результата - структура), по умолчанию срочность Triage
	JSON string
}

func (c Client) Complete(ctx context.Context, request engine.AIRequest) (string, error) {
	if request.JSON {
		if c.JSON != "" {
			return c.JSON, nil
		}
		return `{"urgent": false, "reason": "ответ заглушки: модель не подключена"}`, nil
	}
	if c.Text != "" {
		return c.Text, nil
	}
	return "other", nil
}
