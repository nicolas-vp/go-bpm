package codegen

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nicolas-vp/go-bpm/internal/generator/templates"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

const (
	// modelDir - пакет с моделью данных DataModel, общей для методов и обработчиков всех процессов
	modelDir = "model"
	// servicesDir - пакет с внешними сервисами Services для методов и обработчиков всех процессов
	servicesDir = "services"
)

// reservedDirs - папки в корне вывода, которые не являются пакетами процессов
var reservedDirs = map[string]string{
	handlersDir: "обработчиками jobType",
	modelDir:    "моделью данных",
	servicesDir: "внешними сервисами",
}

// dataImports - пути пакетов модели данных и сервисов, их импортируют заглушки методов и обработчиков,
// а сервисы - ещё flow-gen.go, queue-gen.go и bpm-gen.go
type dataImports struct {
	DataModelImport string
	ServicesImport  string
}

// prepareDataFiles создаёт outputPath/model/data_model.go с пустой DataModel и outputPath/services/services.go
// с пустой Services. Обе структуры дописываются руками, поэтому существующие файлы не перезаписываются
func prepareDataFiles(outputPath string) (dataImports, error) {
	var imports dataImports
	files := []struct {
		dir, file, content string
		importPath         *string
	}{
		{modelDir, "data_model.go", templates.DataModelTemplate, &imports.DataModelImport},
		{servicesDir, "services.go", templates.ServicesTemplate, &imports.ServicesImport},
	}
	for _, f := range files {
		dir := filepath.Join(outputPath, f.dir)
		importPath, err := utils.GoImportPath(dir)
		if err != nil {
			return imports, err
		}
		*f.importPath = importPath

		if err := os.MkdirAll(dir, 0755); err != nil {
			return imports, err
		}
		// Пакет считается написанным руками, если в нём есть хоть один файл: DataModel могли перенести в другой
		if hasGo, err := hasGoFiles(dir); err != nil {
			return imports, err
		} else if hasGo {
			continue
		}
		if err := utils.SaveGoFile(filepath.Join(dir, f.file), []byte(f.content)); err != nil {
			return imports, fmt.Errorf("%s: %w", f.dir, err)
		}
	}
	return imports, nil
}

func hasGoFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".go" {
			return true, nil
		}
	}
	return false, nil
}
