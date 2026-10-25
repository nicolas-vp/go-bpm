package templates

// MethodTemplate - заглушка метода; генерируется один раз, дальше файл правится руками
const MethodTemplate = `package {{ .Package }}

import (
	"context"

	"{{ .ServicesImport }}"
	"{{ .DataModelImport }}"
)

// {{ .FuncName }} - метод задачи {{ .ElementId }}{{ if .Name }} «{{ .Name }}»{{ end }}
func {{ .FuncName }}(ctx context.Context, svc *services.Services, data *model.DataModel) error {
	// TODO: реализовать метод
	return nil
}
`
