package codegen

import (
	"bytes"
	"go/token"
	"path/filepath"
	"sort"
	"text/template"
	"unicode"

	"github.com/nicolas-vp/go-bpm/internal/generator/templates"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

const bpmFileName = "bpm-gen.go"

type bpmFile struct {
	ServicesImport string
	Package        string
	Processes      []bpmProcess
	// BpmnAlias и BpmnImport - пакет process/bpmn-gen.go с BPMN-файлами
	BpmnAlias  string
	BpmnImport string
	Decisions  []bpmDecision
}

// bpmDecision - решение DMN и путь его файла в папке process
type bpmDecision struct {
	Id   string
	Path string
}

type bpmProcess struct {
	ProcessId string
	Alias     string
	Import    string
	// BpmnFile - путь BPMN-файла процесса в папке process
	BpmnFile string
}

// prepareBpmFile генерирует outputPath/bpm-gen.go со ссылками на пакеты всех процессов
func prepareBpmFile(processes []bpmProcess, decisions map[string]Decision, bpmnImport, outputPath string, imports dataImports) error {
	t, err := template.New("bpm").Parse(templates.BpmTemplate)
	if err != nil {
		return err
	}

	sort.Slice(processes, func(i, j int) bool {
		return processes[i].ProcessId < processes[j].ProcessId
	})

	var bpmDecisions []bpmDecision
	for id, decision := range decisions {
		bpmDecisions = append(bpmDecisions, bpmDecision{Id: id, Path: decision.Path})
	}
	sort.Slice(bpmDecisions, func(i, j int) bool { return bpmDecisions[i].Id < bpmDecisions[j].Id })

	var buf bytes.Buffer
	err = t.Execute(&buf, bpmFile{
		ServicesImport: imports.ServicesImport,
		Decisions:      bpmDecisions,
		Package:        packageName(filepath.Base(outputPath)),
		Processes:      processes,
		BpmnAlias:      bpmnAlias,
		BpmnImport:     bpmnImport,
	})
	if err != nil {
		return err
	}
	return utils.SaveGoFile(filepath.Join(outputPath, bpmFileName), buf.Bytes())
}

// packageName делает из имени папки допустимое имя пакета или алиаса импорта: "bpm-gen" -> "bpm_gen"
func packageName(dir string) string {
	name := utils.ToSnakeCase(dir)
	if name == "" || unicode.IsDigit(rune(name[0])) || token.IsKeyword(name) {
		name = "process_" + name
	}
	return name
}
