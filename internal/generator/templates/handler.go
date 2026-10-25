package templates

// HandlerTemplate - заглушка обработчика jobType; генерируется один раз, дальше файл правится руками
const HandlerTemplate = `package handlers

import (
	"context"

	"` + ModelImport + `"
	"{{ .ServicesImport }}"
	"{{ .DataModelImport }}"
)

// {{ .FuncName }} - обработчик service task с jobType "{{ .JobType }}" во всех процессах, где он встречается
// (job.ProcessName, job.ElementId). Вызывается асинхронно из очереди задач и при ошибке повторяется,
// поэтому должен быть идемпотентным (ключ - job.InstanceUid + job.ElementId)
func {{ .FuncName }}(ctx context.Context, svc *services.Services, job bpm.Job, data *model.DataModel) error {
	// TODO: реализовать обработчик
	return nil
}
`
