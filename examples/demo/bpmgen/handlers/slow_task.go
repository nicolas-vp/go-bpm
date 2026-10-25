package handlers

import (
	"context"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/model"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
)

// SlowTask - обработчик service task с jobType "slowTask" во всех процессах, где он встречается
// (job.ProcessName, job.ElementId). Вызывается асинхронно из очереди задач и при ошибке повторяется,
// поэтому должен быть идемпотентным (ключ - job.InstanceUid + job.ElementId)
func SlowTask(ctx context.Context, svc *services.Services, job bpm.Job, data *model.DataModel) error {
	// TODO: реализовать обработчик
	return nil
}
