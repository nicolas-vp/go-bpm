package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicolas-vp/go-bpm/internal/generator/codegen"

	"github.com/nicolas-vp/go-bpm/internal/generator/file"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/dmn"
	"github.com/nicolas-vp/go-bpm/internal/generator/parser"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

// Пути по умолчанию считаются от корня модуля (папки с go.mod), а не от текущей папки,
// поэтому без флагов генератор работает из любой папки проекта, в том числе из go:generate,
// который запускает команду в папке пакета
const (
	defaultProcessPath = "process"
	defaultOutputPath  = "internal/bpmgen"
	bpmnExt            = ".bpmn"
	dmnExt             = ".dmn"
)

func main() {
	processPath := flag.String("process", defaultProcessPath, "папка с BPMN-файлами; по умолчанию <корень модуля>/"+defaultProcessPath)
	outputPath := flag.String("output", defaultOutputPath, "папка для сгенерированного кода внутри Go-модуля; по умолчанию <корень модуля>/"+defaultOutputPath)
	check := flag.Bool("check", true, "после генерации собрать код в модуле (go build)")
	flag.Parse()

	if err := resolveDefaults(processPath, outputPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("go-bpm-gen: %s -> %s\n", *processPath, *outputPath)

	deploymentFolders, err := file.GetFiles(*processPath)
	if err != nil {
		panic(err)
	}

	// Генерируем все процессы за один вызов, чтобы bpm-gen.go содержал их все.
	// Читаем только *.bpmn и *.dmn: в папке лежит и сгенерированный process/bpmn-gen.go
	var allDefinitions []bpmn.Definitions
	var allDecisions []dmn.Definitions
	for deploymentFolder, files := range deploymentFolders {
		for _, processFile := range files {
			ext := strings.ToLower(filepath.Ext(processFile))
			if ext != bpmnExt && ext != dmnExt {
				continue
			}
			fileContent, err := file.ReadFile(*processPath, deploymentFolder, processFile)
			if err != nil {
				panic(err)
			}
			path := filepath.ToSlash(filepath.Join(deploymentFolder, processFile))
			if ext == dmnExt {
				decisions, err := parser.ParseDmn(fileContent)
				if err != nil {
					panic(fmt.Errorf("%s: %w", path, err))
				}
				decisions.Path = path
				allDecisions = append(allDecisions, *decisions)
				continue
			}
			definitions, err := parser.ParseBpm(fileContent)
			if err != nil {
				panic(fmt.Errorf("%s: %w", path, err))
			}
			definitions.Path = path
			allDefinitions = append(allDefinitions, *definitions)
		}
	}

	decisions, err := codegen.CollectDecisions(allDecisions)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	err = codegen.GenerateQueue(allDefinitions, decisions, *processPath, *outputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *check {
		if err := codegen.CheckBuild(*outputPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

// resolveDefaults переводит незаданные флаги в пути от корня модуля; заданные явно остаются от текущей папки
func resolveDefaults(processPath, outputPath *string) error {
	set := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["process"] && set["output"] {
		return nil
	}

	root, err := utils.ModuleRoot(".")
	if err != nil {
		return fmt.Errorf("пути по умолчанию считаются от go.mod, запустите генератор внутри Go-модуля или задайте -process и -output: %w", err)
	}
	if !set["process"] {
		*processPath = filepath.Join(root, defaultProcessPath)
	}
	if !set["output"] {
		*outputPath = filepath.Join(root, defaultOutputPath)
	}
	return nil
}
