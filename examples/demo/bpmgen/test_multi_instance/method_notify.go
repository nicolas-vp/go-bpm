package test_multi_instance

import (
	"context"

	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/model"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
)

// Notify - метод задачи Notify «Отправить уведомление». Задача выполняется три раза подряд (multi-instance),
// номер выполнения - data.LoopCounter
func Notify(ctx context.Context, svc *services.Services, data *model.DataModel) error {
	data.Notifications = data.LoopCounter
	return nil
}
