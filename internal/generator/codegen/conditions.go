package codegen

import (
	"bytes"
	"fmt"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"unicode"

	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/templates"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

// conditionFilePrefix - условия лежат в пакете процесса файлами cond_<id элемента>.go
const conditionFilePrefix = "cond_"

type conditionFile struct {
	DataModelImport string
	Package         string
	FuncName        string
	ElementId       string
	Branches        []conditionFileBranch
	DefaultFlow     string
}

type conditionFileBranch struct {
	FlowId     string
	Expression string
	// Source - условие как оно записано в процессе, если Expression от него отличается
	Source string
}

// prepareConditionFiles генерирует processPath/cond_<id элемента>.go для каждого условного перехода.
// Условия работают с моделью данных: переменные процесса - поля DataModel; недостающие поля попадают в gaps,
// и файл условия не пишется. Условия, которых больше нет в процессе, уже удалены вместе
// с остальными сгенерированными файлами
func prepareConditionFiles(processes []model.DeploymentProcess, processId, processPath, pkg string,
	imports dataImports, m *modelPackage, gaps *modelGaps) error {
	t, err := template.New("condition").Parse(templates.ConditionTemplate)
	if err != nil {
		return err
	}

	for _, process := range processes {
		for _, expression := range process.Expressions {
			if expression.Condition == nil {
				continue
			}
			ctx := exprContext{model: m, gaps: gaps, owner: "процесс " + processId + ": условие " + expression.ElementId}
			gapsBefore := gaps.len()
			var branches []conditionFileBranch
			for _, branch := range expression.Condition.Branches {
				source := strings.TrimSpace(branch.Expression)
				code, err := ctx.rewrite(source, kindBool)
				if err != nil {
					return fmt.Errorf("condition %s, flow %s: %w", expression.ElementId, branch.FlowId, err)
				}
				fileBranch := conditionFileBranch{FlowId: branch.FlowId, Expression: code}
				if code != source {
					fileBranch.Source = source
				}
				branches = append(branches, fileBranch)
			}
			if gaps.len() > gapsBefore {
				continue
			}

			var buf bytes.Buffer
			err = t.Execute(&buf, conditionFile{
				DataModelImport: imports.DataModelImport,
				Package:         pkg,
				FuncName:        expression.ConditionFunc,
				ElementId:       expression.ElementId,
				Branches:        branches,
				DefaultFlow:     expression.Condition.DefaultFlow,
			})
			if err != nil {
				return err
			}

			path := filepath.Join(processPath, conditionFilePrefix+utils.ToSnakeCase(expression.ElementId)+".go")
			if err := utils.SaveGoFile(path, buf.Bytes()); err != nil {
				return fmt.Errorf("condition %s: %w", expression.ElementId, err)
			}
		}
	}
	return nil
}

var (
	// "x := ...", "x, y := ...", "var x ..."
	shortVarDecl = regexp.MustCompile(`([A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*)\s*:=`)
	varDecl      = regexp.MustCompile(`\bvar\s+([A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*)`)
)

// localDeclarations - переменные, которые объявляются в самом коде скрипта, а не берутся из процесса
func localDeclarations(code string) map[string]bool {
	locals := make(map[string]bool)
	for _, re := range []*regexp.Regexp{shortVarDecl, varDecl} {
		for _, match := range re.FindAllStringSubmatch(code, -1) {
			for _, name := range strings.Split(match[1], ",") {
				locals[strings.TrimSpace(name)] = true
			}
		}
	}
	return locals
}

func isIdentRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// isPredeclared - ключевые слова, true/false/nil и параметры сгенерированных функций
func isPredeclared(name string) bool {
	switch name {
	case "true", "false", "nil", "ctx", "svc", dataParam:
		return true
	}
	return token.IsKeyword(name)
}

func prevNonSpace(runes []rune, i int) rune {
	for i--; i >= 0; i-- {
		if !unicode.IsSpace(runes[i]) {
			return runes[i]
		}
	}
	return 0
}

func nextNonSpace(runes []rune, i int) rune {
	for ; i < len(runes); i++ {
		if !unicode.IsSpace(runes[i]) {
			return runes[i]
		}
	}
	return 0
}
