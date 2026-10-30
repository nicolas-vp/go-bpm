package codegen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
	"github.com/nicolas-vp/go-bpm/internal/generator/templates"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

const (
	flowFileName  = "flow-gen.go"
	queueFileName = "queue-gen.go"
)

// GenerateQueue генерирует код для каждого процесса в отдельный пакет outputPath/<имя процесса>:
// flow-gen.go, условия cond_<id>.go и методы method_<id>.go task и scriptTask.
// Всё лежит в одном пакете, поэтому flow-gen.go ссылается на условия и методы без импортов
// и не зависит от имени целевого проекта.
// Очереди service task общие для всех процессов (один jobType может быть в нескольких процессах),
// поэтому они в outputPath/queue-gen.go, а их обработчики - в пакете outputPath/handlers.
// Методы, скрипты, обработчики, условия и решения DMN работают с моделью данных outputPath/model (DataModel):
// любая переменная, которую они используют, должна быть её полем (см. model_resolver.go)
// и внешними сервисами outputPath/services (Services), они тоже общие для всех процессов.
// Сервисы передаются при старте: Processes, Queues и Deployment процессов - функции от них.
// Путь модуля есть только в корневых bpm-gen.go и queue-gen.go,
// поэтому definitions должны содержать все процессы сразу.
// decisions - решения DMN-файлов по id, их вызывают задачи бизнес-правил
func GenerateQueue(definitions []bpmn.Definitions, decisions map[string]Decision, bpmnPath, outputPath string) error {
	var bpmProcesses []bpmProcess
	// handlers/, model/ и services/ - не пакеты процессов, их не должен удалить removeStaleProcessDirs
	processDirs := make(map[string]bool)
	for dir := range reservedDirs {
		processDirs[dir] = true
	}
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return err
	}
	imports, err := prepareDataFiles(outputPath)
	if err != nil {
		return err
	}
	dataModel, err := loadModel(outputPath)
	if err != nil {
		return err
	}
	gaps := newModelGaps()
	var allQueues []model.Queue
	for _, definition := range definitions {
		for _, process := range definition.Processes {
			var processes []model.DeploymentProcess

			expressions, err := ParseFromProcess(&process, &allQueues, &definition)
			if err != nil {
				return fmt.Errorf("process %s: %w", process.Id, err)
			}
			processes = append(processes, model.DeploymentProcess{
				ProcessID:    process.Id,
				StartElement: process.GetStartingElement(),
				Expressions:  expressions,
			})

			for _, subProcess := range process.SubProcessList {
				subProcess.Id = process.Id + "." + subProcess.Id
				expressions, err := ParseFromProcess(&subProcess, &allQueues, &definition)
				if err != nil {
					return fmt.Errorf("process %s: %w", subProcess.Id, err)
				}
				processes = append(processes, model.DeploymentProcess{
					ProcessID:    subProcess.Id,
					StartElement: subProcess.GetStartingElement(),
					Expressions:  expressions,
				})
			}

			processDir := utils.ProcessDirName(&process)
			if owner, ok := reservedDirs[processDir]; ok {
				return fmt.Errorf("process %s: папка %s занята %s, переименуйте процесс", process.Id, processDir, owner)
			}
			processPath := filepath.Join(outputPath, processDir)
			processDirs[processDir] = true
			pkg := packageName(processDir)
			processImport, err := utils.GoImportPath(processPath)
			if err != nil {
				return err
			}
			if err := checkFuncNames(processes, decisionFuncNames(&process)); err != nil {
				return fmt.Errorf("process %s: %w", process.Id, err)
			}
			if err := os.MkdirAll(processPath, 0755); err != nil {
				return err
			}
			// Удаляем всё сгенерированное раньше, чтобы не остались условия и скрипты, которых больше нет в процессе
			if err := removeGeneratedFiles(processPath); err != nil {
				return err
			}
			if err := prepareConditionFiles(processes, process.Id, processPath, pkg, imports, dataModel, gaps); err != nil {
				return fmt.Errorf("process %s: %w", process.Id, err)
			}
			if err := prepareLoopFiles(processes, process.Id, processPath, pkg, imports, dataModel, gaps); err != nil {
				return fmt.Errorf("process %s: %w", process.Id, err)
			}
			if err := prepareMethodFiles(&process, processPath, pkg, imports, dataModel, gaps); err != nil {
				return err
			}
			if err := prepareDecisionFiles(&process, decisions, processPath, pkg, imports, dataModel, gaps); err != nil {
				return fmt.Errorf("process %s: %w", process.Id, err)
			}
			prepareFileProcesses(deploymentFile{ServicesImport: imports.ServicesImport, Package: pkg, Processes: processes}, processPath)

			bpmProcesses = append(bpmProcesses, bpmProcess{
				ProcessId: process.Id,
				Alias:     pkg,
				Import:    processImport,
				BpmnFile:  definition.Path,
			})
		}
	}

	// недостающие поля модели собраны со всех процессов, чтобы показать их одной ошибкой
	if err := gaps.err(); err != nil {
		return err
	}

	queues := mergeQueues(allQueues)
	// Пакеты удалённых из BPMN процессов иначе остались бы в выводе
	if err := removeStaleProcessDirs(outputPath, processDirs); err != nil {
		return err
	}
	if err := prepareHandlerFiles(queues, outputPath, imports); err != nil {
		return err
	}
	if err := prepareQueuesFile(queues, outputPath, imports); err != nil {
		return err
	}
	bpmnImport, err := prepareBpmnFile(definitions, decisions, bpmnPath)
	if err != nil {
		return err
	}
	return prepareBpmFile(bpmProcesses, decisions, bpmnImport, outputPath, imports)
}

type deploymentFile struct {
	ServicesImport string
	Package        string
	Processes      []model.DeploymentProcess
}

// HasTimers - есть ли в процессах таймеры: тогда flow-gen.go импортирует time
func (f deploymentFile) HasTimers() bool {
	for _, process := range f.Processes {
		for _, expression := range process.Expressions {
			if expression.Timer != 0 {
				return true
			}
			for _, boundary := range expression.Boundaries {
				if boundary.Timer != 0 {
					return true
				}
			}
		}
	}
	return false
}

// reservedNames - имена, которые уже заняты в пакете процесса сгенерированным кодом:
// функция Deployment, её параметр svc и пакеты services и model
var reservedNames = map[string]bool{"Deployment": true, "svc": true, "services": true, "model": true}

// mergeQueues объединяет service task всех процессов по jobType с максимальным Retries,
// проставляет имя обработчика и сортирует по jobType
func mergeQueues(queues []model.Queue) []model.Queue {
	byType := make(map[string]model.Queue)
	for _, queue := range queues {
		if existing, ok := byType[queue.TypeID]; ok {
			queue.Retries = max(queue.Retries, existing.Retries)
		}
		queue.HandlerFunc = utils.ToPascalCase(queue.TypeID)
		byType[queue.TypeID] = queue
	}
	result := make([]model.Queue, 0, len(byType))
	for _, queue := range byType {
		result = append(result, queue)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TypeID < result[j].TypeID })
	return result
}

// checkFuncNames проверяет, что функции условий, условий завершения циклов, методов и решений (decisions: имя - решение) не совпадают
// друг с другом и с reservedNames. Все они лежат в одном пакете, а имена получаются из id элементов и решений
func checkFuncNames(processes []model.DeploymentProcess, decisions map[string]string) error {
	owners := make(map[string]string)
	for name, owner := range decisions {
		if reservedNames[name] {
			return fmt.Errorf("%s: имя функции %s занято сгенерированным кодом, переименуйте id решения", owner, name)
		}
		owners[name] = owner
	}
	for _, process := range processes {
		for _, expression := range process.Expressions {
			names := []string{expression.ConditionFunc, expression.MethodFunc}
			if expression.Loop != nil {
				names = append(names, expression.Loop.ConditionFunc)
			}
			for _, name := range names {
				if name == "" {
					continue
				}
				if reservedNames[name] {
					return fmt.Errorf("element %s: имя функции %s занято сгенерированным кодом, переименуйте id элемента", expression.ElementId, name)
				}
				if owner, ok := owners[name]; ok && owner != expression.ElementId {
					return fmt.Errorf("elements %s и %s дают одно имя функции %s, переименуйте id одного из них", owner, expression.ElementId, name)
				}
				owners[name] = expression.ElementId
			}
		}
	}
	return nil
}

func prepareFileProcesses(deployment deploymentFile, filePath string) {
	// Создаем функцию join для использования в шаблоне
	funcMap := template.FuncMap{
		"join": func(strs []string, sep string) string {
			result := ""
			for i, s := range strs {
				if i > 0 {
					result += sep
				}
				result += "\"" + s + "\""
			}
			return result
		},
		"activateType": bpm.ActivateType.String,
		"completeType": bpm.CompleteType.String,
		"boundaryType": bpm.BoundaryType.String,
		"duration":     utils.DurationLiteral,
		// quotes - строки списком Go-литералов через запятую, с экранированием (значения из BPMN)
		"quotes": func(strs []string) string {
			quoted := make([]string, len(strs))
			for i, s := range strs {
				quoted[i] = strconv.Quote(s)
			}
			return strings.Join(quoted, ", ")
		},
	}

	t, err := template.New("deployment").Funcs(funcMap).Parse(templates.ProcessTemplate)
	if err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, deployment)
	if err != nil {
		panic(err)
	}
	err = utils.SaveGoFile(filepath.Join(filePath, flowFileName), buf.Bytes())
	if err != nil {
		panic(err)
	}
}

type queuesFile struct {
	ServicesImport string
	Package        string
	HandlersImport string
	Queues         []model.Queue
}

// prepareQueuesFile генерирует outputPath/queue-gen.go: очереди всех процессов со ссылками на обработчики
func prepareQueuesFile(queues []model.Queue, outputPath string, imports dataImports) error {
	t, err := template.New("queue").Parse(templates.QueuesTemplate)
	if err != nil {
		return err
	}
	handlersImport, err := utils.GoImportPath(filepath.Join(outputPath, handlersDir))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, queuesFile{
		ServicesImport: imports.ServicesImport,
		Package:        packageName(filepath.Base(outputPath)),
		HandlersImport: handlersImport,
		Queues:         queues,
	})
	if err != nil {
		return err
	}
	return utils.SaveGoFile(filepath.Join(outputPath, queueFileName), buf.Bytes())
}
