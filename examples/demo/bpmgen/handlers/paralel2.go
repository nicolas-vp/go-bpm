package handlers

import (
	"context"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/model"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
)

// Paralel2 - обработчик service task с jobType "paralel2" во всех процессах, где он встречается
// (job.ProcessName, job.ElementId). Вызывается асинхронно из очереди задач и при ошибке повторяется,
// поэтому должен быть идемпотентным (ключ - job.InstanceUid + job.ElementId)
func Paralel2(ctx context.Context, svc *services.Services, job bpm.Job, data *model.DataModel) error {
	// TODO: реализовать обработчик
	return nil
}
