package engine

import (
	"encoding/xml"
	"fmt"
	"slices"
	"strings"

	"github.com/nicolas-vp/go-bpm/bpm"
)

// ProcessDefinition - процесс, который можно запустить: корневой процесс из Processes сгенерированного кода
type ProcessDefinition struct {
	// Id - id процесса в BPMN, по нему процесс запускается
	Id string `json:"id"`
	// Name - название процесса в BPMN, пусто, если не задано
	Name string `json:"name,omitempty"`
	// BPMN - исходный BPMN-файл; только у Process, список его не возвращает
	BPMN string `json:"bpmn,omitempty"`
}

// bpmnProcesses - id и названия процессов BPMN-файла: файл может описывать несколько процессов (collaboration)
type bpmnProcesses struct {
	Processes []struct {
		Id   string `xml:"id,attr"`
		Name string `xml:"name,attr"`
	} `xml:"process"`
}

// catalog собирает определения процессов, отсортированные по id. Название берётся из BPMN; битый BPMN - ошибка:
// его же не нарисует страница истории
func catalog(processes map[string]bpm.Process) ([]ProcessDefinition, error) {
	definitions := make([]ProcessDefinition, 0, len(processes))
	for id, process := range processes {
		definition := ProcessDefinition{Id: id, BPMN: process.BPMN}
		if process.BPMN != "" {
			var file bpmnProcesses
			if err := xml.Unmarshal([]byte(process.BPMN), &file); err != nil {
				return nil, fmt.Errorf("process %q: parse BPMN: %w", id, err)
			}
			for _, p := range file.Processes {
				if p.Id == id {
					definition.Name = strings.TrimSpace(p.Name)
				}
			}
		}
		definitions = append(definitions, definition)
	}
	slices.SortFunc(definitions, func(a, b ProcessDefinition) int { return strings.Compare(a.Id, b.Id) })
	return definitions, nil
}

// Processes возвращает процессы движка без BPMN, по id
func (s *Engine) Processes() []ProcessDefinition {
	list := make([]ProcessDefinition, len(s.definitions))
	for i, definition := range s.definitions {
		definition.BPMN = ""
		list[i] = definition
	}
	return list
}

// Process возвращает процесс с BPMN; ErrProcessNotFound, если его нет
func (s *Engine) Process(id string) (*ProcessDefinition, error) {
	for _, definition := range s.definitions {
		if definition.Id == id {
			return &definition, nil
		}
	}
	return nil, fmt.Errorf("process %q: %w", id, ErrProcessNotFound)
}
