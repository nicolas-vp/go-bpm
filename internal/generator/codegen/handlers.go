package codegen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/templates"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

// handlersDir - пакет обработчиков jobType в корне вывода: jobType может быть в нескольких процессах,
// поэтому обработчик общий и не лежит в пакете процесса
const handlersDir = "handlers"

type handlerFile struct {
	dataImports
	JobType  string
	FuncName string
}

// handlerFilePath - outputPath/handlers/<jobType>.go
func handlerFilePath(outputPath string, queue model.Queue) string {
	return filepath.Join(outputPath, handlersDir, utils.ToSnakeCase(queue.HandlerFunc)+".go")
}

// prepareHandlerFiles создаёт заглушку outputPath/handlers/<jobType>.go для каждого jobType всех процессов,
// queue-gen.go ссылается на неё. Заглушки на модели данных и клиентах (imports) пишутся руками:
// существующие файлы не перезаписываются и не удаляются
func prepareHandlerFiles(queues []model.Queue, outputPath string, imports dataImports) error {
	t, err := template.New("handler").Parse(templates.HandlerTemplate)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(outputPath, handlersDir), 0755); err != nil {
		return err
	}

	owners := make(map[string]string)
	for _, queue := range queues {
		if other, ok := owners[queue.HandlerFunc]; ok {
			return fmt.Errorf("jobType %q и %q дают одно имя обработчика %s, переименуйте один из них", other, queue.TypeID, queue.HandlerFunc)
		}
		owners[queue.HandlerFunc] = queue.TypeID

		path := handlerFilePath(outputPath, queue)
		if exists, err := fileExists(path); err != nil {
			return err
		} else if exists {
			continue
		}

		var buf bytes.Buffer
		if err := t.Execute(&buf, handlerFile{dataImports: imports, JobType: queue.TypeID, FuncName: queue.HandlerFunc}); err != nil {
			return err
		}
		if err := utils.SaveGoFile(path, buf.Bytes()); err != nil {
			return fmt.Errorf("handler %s: %w", queue.TypeID, err)
		}
	}
	return nil
}
