package codegen

import (
	"bytes"
	"path/filepath"
	"slices"
	"text/template"

	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
	"github.com/nicolas-vp/go-bpm/internal/generator/templates"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

const (
	bpmnFileName = "bpmn-gen.go"
	// bpmnAlias - алиас пакета папки process в bpm-gen.go
	bpmnAlias = "bpmn_files"
)

type bpmnFile struct {
	Package  string
	Files    []string
	DmnFiles []string
}

// prepareBpmnFile генерирует processPath/bpmn-gen.go: map BPMN и DMN по пути файла, вшитые через go:embed.
// Возвращает путь импорта пакета папки process для bpm-gen.go
func prepareBpmnFile(definitions []bpmn.Definitions, decisions map[string]Decision, processPath string) (string, error) {
	importPath, err := utils.GoImportPath(processPath)
	if err != nil {
		return "", err
	}
	var files []string
	for _, definition := range definitions {
		files = append(files, definition.Path)
	}
	slices.Sort(files)
	var dmnFiles []string
	for _, decision := range decisions {
		dmnFiles = append(dmnFiles, decision.Path)
	}
	slices.Sort(dmnFiles)
	dmnFiles = slices.Compact(dmnFiles)

	t, err := template.New("bpmn").Parse(templates.BpmnTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, bpmnFile{Package: packageName(filepath.Base(processPath)), Files: files, DmnFiles: dmnFiles}); err != nil {
		return "", err
	}
	if err := utils.SaveGoFile(filepath.Join(processPath, bpmnFileName), buf.Bytes()); err != nil {
		return "", err
	}
	return importPath, nil
}
