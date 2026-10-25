package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/dmn"
	"github.com/nicolas-vp/go-bpm/internal/generator/templates"
	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

// Решение DMN переводится в Go-функцию пакета процесса на модели данных: входы таблицы - поля DataModel
// (вход a - data.A, вход client.age - data.Client.Age), вычисляются один раз; правила становятся
// цепочкой if в порядке таблицы. Ячейки правил - unary tests FEEL:
//   - пусто или "-" - любое значение;
//   - значение ("foo", 5, true, переменная) - равенство;
//   - сравнение (< 5, >= limit), интервал ([1..10], ]1..10[, (1..10]);
//   - несколько тестов через запятую - любой из них, not(...) - отрицание;
//   - null - поля нет (nil), только у входа - поля-указателя, map, среза или any;
//   - выражение с ? (? > a + b), где ? - значение входа.
//
// Выходы - выражения FEEL, тип выхода - typeRef (без него - по литералам в ячейках).
// Переменные в ячейках и выходах - тоже поля модели.

// Decision - решение DMN и файл, в котором оно описано
type Decision struct {
	dmn.Decision
	Path string
}

// CollectDecisions собирает решения всех DMN-файлов по id; id решения должен быть уникален во всех файлах
func CollectDecisions(definitions []dmn.Definitions) (map[string]Decision, error) {
	decisions := make(map[string]Decision)
	for _, definition := range definitions {
		for _, decision := range definition.Decisions {
			if existing, found := decisions[decision.Id]; found {
				return nil, fmt.Errorf("решение %s есть в %s и %s, id решений должны быть уникальны", decision.Id, existing.Path, definition.Path)
			}
			decisions[decision.Id] = Decision{Decision: decision, Path: definition.Path}
		}
	}
	return decisions, nil
}

// decisionFilePrefix - решения лежат в пакете процесса файлами decision_<id решения>.go
const decisionFilePrefix = "decision_"

// decisionFuncName - имя функции решения в пакете процесса
func decisionFuncName(decisionId string) string {
	return "decide" + utils.ToPascalCase(decisionId)
}

type decisionFile struct {
	DataModelImport string
	Package         string
	FuncName        string
	DecisionId      string
	Name            string
	Path            string
	HitPolicy       string
	// Result - что возвращает функция, для комментария
	Result string
	// ResultType - тип результата; ResultStruct - объявление структуры выходов, если их несколько
	ResultType   string
	ResultStruct string
	Inputs       []decisionInput
	// Prologue и Epilogue - код до и после правил: накопление результата и его возврат
	Prologue string
	Rules    []decisionRule
	Epilogue string

	shape   decisionShape
	outputs []decisionOutput
}

// decisionShape - что возвращает решение
type decisionShape int

const (
	// shapeFirst - выходы первого подошедшего правила
	shapeFirst decisionShape = iota
	// shapeList - список выходов всех подошедших правил
	shapeList
	// shapeCount - число подошедших правил
	shapeCount
	// shapeAggregate - сумма, минимум или максимум выхода подошедших правил
	shapeAggregate
)

type decisionOutput struct {
	// Key - имя выхода, по нему ищется поле результата в модели
	Key string
	// Field - поле структуры выходов, если выходов несколько
	Field  string
	GoType string
	kind   valueKind
}

type decisionInput struct {
	// Name - локальная переменная со значением входа
	Name       string
	Label      string
	Expression string
	kind       valueKind
	// nilCheck - проверка null у входа - поля, которое может быть nil
	nilCheck string
	used     bool
	// gaps - недостающие поля выражения входа; в подсказку попадают, только если вход проверяет хоть одно правило
	gaps *modelGaps
}

type decisionRule struct {
	Comment   string
	Condition string
	Statement string
}

const (
	hitUnique      = "UNIQUE"
	hitFirst       = "FIRST"
	hitAny         = "ANY"
	hitCollect     = "COLLECT"
	hitRuleOrder   = "RULE ORDER"
	hitPriority    = "PRIORITY"
	hitOutputOrder = "OUTPUT ORDER"
)

// compileDecision переводит таблицу решения в описание файла для DecisionTemplate.
// Недостающие в модели поля попадают в gaps
func compileDecision(decision Decision, m *modelPackage, gaps *modelGaps) (*decisionFile, error) {
	if decision.DecisionTable == nil {
		if decision.LiteralExpression != nil {
			return nil, fmt.Errorf("решение выражением (literal expression) не поддерживается, только таблица")
		}
		return nil, fmt.Errorf("у решения нет таблицы правил")
	}
	for _, requirement := range decision.InformationRequirements {
		if requirement.RequiredDecision != nil {
			return nil, fmt.Errorf("зависимость от другого решения (%s) не поддерживается", strings.TrimPrefix(requirement.RequiredDecision.Href, "#"))
		}
	}
	table := decision.DecisionTable
	if len(table.Outputs) == 0 {
		return nil, fmt.Errorf("у таблицы нет выходов")
	}

	file := &decisionFile{
		FuncName:   decisionFuncName(decision.Id),
		DecisionId: decision.Id,
		Name:       oneLine(decision.Name),
		Path:       decision.Path,
		HitPolicy:  hitPolicy(table),
	}
	ctx := exprContext{model: m, gaps: gaps, owner: "решение " + decision.Id + " (" + decision.Path + ")"}
	inputs, err := compileInputs(ctx, table)
	if err != nil {
		return nil, err
	}
	if err := file.compileOutputs(table); err != nil {
		return nil, err
	}

	accumulator, err := newAccumulator(file, table)
	if err != nil {
		return nil, err
	}
	for _, rule := range table.Rules {
		if len(rule.InputEntries) != len(inputs) || len(rule.OutputEntries) != len(table.Outputs) {
			return nil, fmt.Errorf("правило %s: ячеек %d/%d, а входов и выходов %d/%d", rule.Id,
				len(rule.InputEntries), len(rule.OutputEntries), len(inputs), len(table.Outputs))
		}
		var conditions, sources []string
		for i, entry := range rule.InputEntries {
			condition, err := compileUnaryTests(ctx, entry.Text, &inputs[i])
			if err != nil {
				return nil, fmt.Errorf("правило %s, вход %s: %w", rule.Id, inputLabel(table.Inputs[i]), err)
			}
			if condition != "" {
				conditions = append(conditions, condition)
				inputs[i].used = true
			}
			sources = append(sources, orDash(entry.Text))
		}
		var values, outputs []string
		for i, entry := range rule.OutputEntries {
			value, err := compileOutput(ctx, entry.Text, file.outputs[i].kind)
			if err != nil {
				return nil, fmt.Errorf("правило %s, выход %s: %w", rule.Id, outputLabel(table.Outputs[i]), err)
			}
			values = append(values, value)
			outputs = append(outputs, orDash(entry.Text))
		}

		comment := rule.Id
		if description := oneLine(rule.Description); description != "" {
			comment += " - " + description
		}
		comment += ": " + strings.Join(sources, " | ") + " => " + strings.Join(outputs, " | ")
		file.Rules = append(file.Rules, decisionRule{
			Comment:   comment,
			Condition: strings.Join(conditions, " && "),
			Statement: accumulator.statement(file, rule.Id, values),
		})
		// правило без условий срабатывает всегда: в режиме первого совпадения дальше код недостижим
		if file.shape == shapeFirst && len(conditions) == 0 {
			file.Epilogue = ""
			break
		}
	}
	for _, input := range inputs {
		// вход, который не проверяет ни одно правило, не объявляем: Go не даёт неиспользуемых переменных
		if input.used {
			file.Inputs = append(file.Inputs, input)
			gaps.merge(input.gaps)
		}
	}
	return file, nil
}

// compileOutputs - типы выходов: typeRef, без него - по литералам в ячейках. Несколько выходов
// возвращаются структурой с полем на каждый выход
func (file *decisionFile) compileOutputs(table *dmn.DecisionTable) error {
	fields := make(map[string]string)
	for i, output := range table.Outputs {
		kind := kindFromTypeRef(output.TypeRef)
		if kind == kindUnknown {
			var texts []string
			for _, rule := range table.Rules {
				if i < len(rule.OutputEntries) {
					texts = append(texts, rule.OutputEntries[i].Text)
				}
			}
			kind = inferKind(texts)
		}
		out := decisionOutput{Key: output.Name, GoType: kindGoType(kind), kind: kind}
		if len(table.Outputs) > 1 {
			if output.Name == "" {
				return fmt.Errorf("у таблицы несколько выходов, у выхода %s нужно имя (name)", output.Id)
			}
			out.Field = utils.ToPascalCase(output.Name)
			if !goIdentifier.MatchString(out.Field) {
				return fmt.Errorf("выход %q: имя не переводится в поле Go", output.Name)
			}
			if other, exists := fields[out.Field]; exists {
				return fmt.Errorf("выходы %q и %q дают одно поле %s", other, output.Name, out.Field)
			}
			fields[out.Field] = output.Name
		}
		file.outputs = append(file.outputs, out)
	}
	return nil
}

// elementType - тип одного результата правила: тип выхода или структура выходов
func (file *decisionFile) elementType() string {
	if len(file.outputs) == 1 {
		return file.outputs[0].GoType
	}
	return file.FuncName + "Result"
}

// zeroValue - нулевое значение типа результата правила
func (file *decisionFile) zeroValue() string {
	if len(file.outputs) > 1 {
		return file.elementType() + "{}"
	}
	return kindZero(file.outputs[0].kind)
}

func cmpKind(kinds ...valueKind) valueKind {
	for _, kind := range kinds {
		if kind != kindUnknown {
			return kind
		}
	}
	return kindUnknown
}

func kindZero(kind valueKind) string {
	switch kind {
	case kindInt, kindFloat:
		return "0"
	case kindString:
		return `""`
	case kindBool:
		return "false"
	}
	return "nil"
}

func hitPolicy(table *dmn.DecisionTable) string {
	policy := strings.ToUpper(strings.Join(strings.Fields(table.HitPolicy), " "))
	if policy == "" {
		policy = hitUnique
	}
	if policy == hitCollect && table.Aggregation != "" {
		policy += " " + strings.ToUpper(table.Aggregation)
	}
	return policy
}

// accumulator - как правило записывает результат, зависит от hit policy
type accumulator struct{}

func newAccumulator(file *decisionFile, table *dmn.DecisionTable) (*accumulator, error) {
	single := len(file.outputs) == 1
	what := "выходы " + outputNames(table.Outputs)
	if single {
		what = "значение выхода " + outputLabel(table.Outputs[0])
	} else {
		var fields []string
		for _, output := range file.outputs {
			fields = append(fields, fmt.Sprintf("\t%s %s `json:%q`", output.Field, output.GoType, output.Key))
		}
		file.ResultStruct = fmt.Sprintf("// %s - выходы решения %s\ntype %s struct {\n%s\n}",
			file.elementType(), file.DecisionId, file.elementType(), strings.Join(fields, "\n"))
	}
	element := file.elementType()
	policy := strings.ToUpper(strings.Join(strings.Fields(table.HitPolicy), " "))
	switch policy {
	case "", hitUnique, hitFirst, hitAny:
		// UNIQUE и ANY не проверяют пересечение правил: берётся первое подошедшее, как в FIRST
		file.shape = shapeFirst
		file.ResultType = element
		file.Result = what + " первого подошедшего правила; если ни одно не подошло - нулевое значение и пустой список правил"
		file.Epilogue = "return " + file.zeroValue() + ", nil"
		return &accumulator{}, nil
	case hitRuleOrder, hitCollect:
	case hitPriority, hitOutputOrder:
		return nil, fmt.Errorf("hit policy %s не поддерживается: нужен порядок значений выхода", policy)
	default:
		return nil, fmt.Errorf("неизвестная hit policy %q", table.HitPolicy)
	}

	aggregation := strings.ToUpper(table.Aggregation)
	if policy == hitRuleOrder || aggregation == "" {
		file.shape = shapeList
		file.ResultType = "[]" + element
		file.Result = "список: " + what + " каждого подошедшего правила по порядку; пустой, если ни одно не подошло"
		file.Prologue = "result := []" + element + "{}\nvar rules []string"
		file.Epilogue = "return result, rules"
		return &accumulator{}, nil
	}
	if !single {
		return nil, fmt.Errorf("COLLECT %s работает только с одним выходом", aggregation)
	}
	if aggregation == "COUNT" {
		file.shape = shapeCount
		file.ResultType = "int"
		file.Result = "число подошедших правил"
		file.Prologue = "count := 0\nvar rules []string"
		file.Epilogue = "return count, rules"
		return &accumulator{}, nil
	}
	operation, found := map[string]string{
		"SUM": "result += value",
		"MIN": "result = min(result, value)",
		"MAX": "result = max(result, value)",
	}[aggregation]
	if !found {
		return nil, fmt.Errorf("неизвестная агрегация COLLECT %q", table.Aggregation)
	}
	output := &file.outputs[0]
	switch output.kind {
	case kindInt:
	case kindFloat, kindUnknown:
		output.kind, output.GoType = kindFloat, kindGoType(kindFloat)
	default:
		return nil, fmt.Errorf("COLLECT %s: выход %s должен быть числом", aggregation, outputLabel(table.Outputs[0]))
	}
	file.shape = shapeAggregate
	file.ResultType = output.GoType
	file.Result = strings.ToLower(aggregation) + " значений выхода " + outputLabel(table.Outputs[0]) +
		" подошедших правил; если ни одно не подошло - 0 и пустой список правил"
	file.Prologue = "var values []" + output.GoType + "\nvar rules []string"
	file.Epilogue = "if len(values) == 0 {\nreturn 0, nil\n}\nresult := values[0]\nfor _, value := range values[1:] {\n" + operation + "\n}\nreturn result, rules"
	return &accumulator{}, nil
}

// statement - код правила ruleId, которое подошло: результат и id правила для истории
func (a *accumulator) statement(file *decisionFile, ruleId string, values []string) string {
	value := values[0]
	if len(file.outputs) > 1 {
		var fields []string
		for i, output := range file.outputs {
			fields = append(fields, output.Field+": "+values[i])
		}
		value = file.elementType() + "{" + strings.Join(fields, ", ") + "}"
	}
	rule := strconv.Quote(ruleId)
	if file.shape == shapeFirst {
		return "return " + value + ", []string{" + rule + "}"
	}
	statement := "rules = append(rules, " + rule + ")\n"
	switch file.shape {
	case shapeCount:
		return statement + "count++"
	case shapeAggregate:
		return statement + "values = append(values, " + value + ")"
	default:
		return statement + "result = append(result, " + value + ")"
	}
}

var (
	goIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	spacedName   = regexp.MustCompile(`^[\pL_][\pL\pN_]*(?: [\pL\pN_]+)+$`)
)

// compileInputs - локальные переменные входов. Вход - путь переменной (a, client.age, customer category) -
// читается из поля модели, локальная переменная называется по последнему сегменту, иначе inputN.
// Тип входа - typeRef, без него - тип поля, без него - по литералам в ячейках
func compileInputs(ctx exprContext, table *dmn.DecisionTable) ([]decisionInput, error) {
	inputs := make([]decisionInput, len(table.Inputs))
	names := make(map[string]bool)
	for i, input := range table.Inputs {
		text := strings.TrimSpace(input.InputExpression.Text)
		if text == "" {
			return nil, fmt.Errorf("вход %s: нет выражения", inputLabel(input))
		}
		decisionIn := decisionInput{Label: oneLine(input.Label), kind: kindFromTypeRef(input.InputExpression.TypeRef), gaps: newModelGaps()}
		inputCtx := ctx
		inputCtx.gaps = decisionIn.gaps
		segments, isPath := splitPath(text)
		var leaf ast.Expr
		if isPath {
			path, gap := ctx.model.resolvePath(segments)
			if gap != nil {
				kind := decisionIn.kind
				if kind == kindUnknown {
					kind = inferKind(columnTexts(table, i))
				}
				decisionIn.gaps.add(ctx.owner, *gap, kindGoType(kind))
				decisionIn.Expression = "nil"
				if decisionIn.kind == kindUnknown {
					// файл не запишется, тип нужен только чтобы дособрать недостающие поля из ячеек
					decisionIn.kind = cmpKind(kind, kindString)
				}
			} else {
				leaf = path.leaf
				decisionIn.Expression = path.String()
				if isNillable(leaf) {
					decisionIn.nilCheck = path.String() + " == nil"
				}
				if _, isPointer := leaf.(*ast.StarExpr); isPointer {
					// у указателя на простой тип проверяется null, а правила сравнивают значение
					if leafKind(leaf) != kindUnknown {
						decisionIn.Expression = "bpm.Deref(" + path.String() + ")"
					}
				}
			}
			if last := segments[len(segments)-1]; isPlainVariable(last) && !names[last] && !isReservedLocal(last) {
				decisionIn.Name = last
			}
		}
		if decisionIn.kind == kindUnknown && leaf != nil {
			decisionIn.kind = leafKind(leaf)
		}
		if decisionIn.kind == kindUnknown {
			if decisionIn.kind = inferKind(columnTexts(table, i)); decisionIn.kind == kindUnknown {
				return nil, fmt.Errorf("вход %s: тип не выводится из правил, укажите typeRef (string, number, integer, boolean)", inputLabel(input))
			}
		}
		if !isPath {
			expression, err := inputCtx.rewrite(text, decisionIn.kind)
			if err != nil {
				return nil, fmt.Errorf("вход %s: %w", inputLabel(input), err)
			}
			decisionIn.Expression = expression
		}
		if decisionIn.Name == "" {
			decisionIn.Name = "input" + strconv.Itoa(i+1)
			for names[decisionIn.Name] {
				decisionIn.Name += "_"
			}
		}
		names[decisionIn.Name] = true
		inputs[i] = decisionIn
	}
	return inputs, nil
}

// columnTexts - ячейки столбца входа i во всех правилах
func columnTexts(table *dmn.DecisionTable, i int) []string {
	var texts []string
	for _, rule := range table.Rules {
		if i < len(rule.InputEntries) {
			texts = append(texts, rule.InputEntries[i].Text)
		}
	}
	return texts
}

func isPlainVariable(text string) bool {
	return goIdentifier.MatchString(text) && !token.IsKeyword(text) && !isPredeclared(text) && text != "null"
}

// isReservedLocal - имя нельзя дать локальной переменной: оно занято кодом функции, пакетом или встроено в Go
func isReservedLocal(name string) bool {
	switch name {
	case "result", "values", "value", "count", "rules", "ctx", dataParam, "bpm", modelDir:
		return true
	}
	return types.Universe.Lookup(name) != nil || strings.HasPrefix(name, "input")
}

func kindFromTypeRef(typeRef string) valueKind {
	switch strings.ToLower(typeRef) {
	case "string":
		return kindString
	case "number", "double":
		return kindFloat
	case "integer", "long":
		return kindInt
	case "boolean":
		return kindBool
	}
	return kindUnknown
}

var (
	stringLiteral = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	boolLiteral   = regexp.MustCompile(`\b(?:true|false)\b`)
	floatLiteral  = regexp.MustCompile(`(?:^|[^\w.])\d+\.\d+`)
	intLiteral    = regexp.MustCompile(`(?:^|[^\w.])\d+`)
)

// inferKind - тип по литералам в ячейках: строка, логическое, дробное или целое число
func inferKind(texts []string) valueKind {
	text := strings.Join(texts, "\n")
	switch {
	case stringLiteral.MatchString(text):
		return kindString
	case boolLiteral.MatchString(text):
		return kindBool
	case floatLiteral.MatchString(text):
		return kindFloat
	case intLiteral.MatchString(text):
		return kindInt
	}
	return kindUnknown
}

const inputPlaceholder = "__input__"

var (
	rangeTest      = regexp.MustCompile(`^([\[(\]])\s*(.+?)\s*\.\.\s*(.+?)\s*([\])\[])$`)
	comparisonTest = regexp.MustCompile(`^(<=|>=|!=|<|>|=)\s*(.+)$`)
)

// compileUnaryTests переводит ячейку входа в условие Go; "" - ячейка подходит к любому значению
func compileUnaryTests(ctx exprContext, text string, input *decisionInput) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "-" {
		return "", nil
	}
	if inner, ok := notCall(text); ok {
		condition, err := compileUnaryTests(ctx, inner, input)
		if err != nil {
			return "", err
		}
		if condition == "" {
			return "", fmt.Errorf("not(%s) не подходит ни к одному значению", inner)
		}
		return "!(" + condition + ")", nil
	}
	var conditions []string
	for _, test := range splitTests(text) {
		condition, err := compileUnaryTest(ctx, strings.TrimSpace(test), input)
		if err != nil {
			return "", err
		}
		conditions = append(conditions, condition)
	}
	if len(conditions) == 1 {
		return conditions[0], nil
	}
	return "(" + strings.Join(conditions, " || ") + ")", nil
}

func compileUnaryTest(ctx exprContext, test string, input *decisionInput) (string, error) {
	switch {
	case test == "":
		return "", fmt.Errorf("пустой тест в списке через запятую")
	case test == "null":
		if input.nilCheck == "" {
			return "", fmt.Errorf("null проверяется только у входа - поля модели, которое может быть nil: указатель (*int), map, срез или any")
		}
		return input.nilCheck, nil
	case strings.ContainsRune(stripStrings(test), '?'):
		return compileInputExpression(ctx, test, input)
	}
	if match := rangeTest.FindStringSubmatch(test); match != nil {
		low, err := compileValue(ctx, match[2], input.kind)
		if err != nil {
			return "", err
		}
		high, err := compileValue(ctx, match[3], input.kind)
		if err != nil {
			return "", err
		}
		lowOp, highOp := ">", "<"
		if match[1] == "[" {
			lowOp = ">="
		}
		if match[4] == "]" {
			highOp = "<="
		}
		return input.Name + " " + lowOp + " " + low + " && " + input.Name + " " + highOp + " " + high, nil
	}
	op := "=="
	if match := comparisonTest.FindStringSubmatch(test); match != nil {
		if op = match[1]; op == "=" {
			op = "=="
		}
		test = match[2]
	}
	value, err := compileValue(ctx, test, input.kind)
	if err != nil {
		return "", err
	}
	if input.kind == kindBool && (value == "true" || value == "false") && (op == "==" || op == "!=") {
		if (value == "true") == (op == "==") {
			return input.Name, nil
		}
		return "!" + input.Name, nil
	}
	return input.Name + " " + op + " " + value, nil
}

// compileInputExpression - тест-выражение, где ? - значение входа: "? > 5", "? < limit * 2"
func compileInputExpression(ctx exprContext, test string, input *decisionInput) (string, error) {
	var out strings.Builder
	quote := rune(0)
	for _, r := range test {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"':
			quote = r
		case r == '?':
			out.WriteString(inputPlaceholder)
			continue
		}
		out.WriteRune(r)
	}
	ctx.locals = map[string]localVar{inputPlaceholder: {kind: input.kind, name: input.Name}}
	return ctx.rewrite(out.String(), kindBool)
}

// notCall - "not(тесты)" целиком: возвращает тесты внутри
func notCall(text string) (string, bool) {
	rest, found := strings.CutPrefix(text, "not")
	rest = strings.TrimSpace(rest)
	if !found || !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
		return "", false
	}
	// скобка в конце должна закрывать первую, иначе это "not(a), not(b)"
	depth, quote := 0, rune(0)
	runes := []rune(rest)
	for i, r := range runes {
		switch {
		case quote != 0:
			if r == '\\' {
				continue
			}
			if r == quote {
				quote = 0
			}
		case r == '"':
			quote = r
		case r == '(':
			depth++
		case r == ')':
			depth--
			if depth == 0 && i != len(runes)-1 {
				return "", false
			}
		}
	}
	return string(runes[1 : len(runes)-1]), true
}

// splitTests делит ячейку по запятым верхнего уровня: вне строк и скобок вызовов функций.
// Скобки интервалов не считаются: в интервале запятых нет, а ]1..5[ скобки не парные
func splitTests(text string) []string {
	var tests []string
	runes := []rune(text)
	depth, quote, start := 0, rune(0), 0
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			if r == '\\' {
				i++
			} else if r == quote {
				quote = 0
			}
		case r == '"':
			quote = r
		case r == '(' && isIdentRune(prevNonSpace(runes, i)):
			depth++
		case r == ')' && depth > 0:
			depth--
		case r == ',' && depth == 0:
			tests = append(tests, string(runes[start:i]))
			start = i + 1
		}
	}
	return append(tests, string(runes[start:]))
}

// stripStrings убирает строковые литералы, чтобы искать операторы только в коде
func stripStrings(text string) string {
	return stringLiteral.ReplaceAllString(text, `""`)
}

// compileOutput - значение выхода правила; пустая ячейка - нулевое значение типа выхода
func compileOutput(ctx exprContext, text string, kind valueKind) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return kindZero(kind), nil
	}
	return compileValue(ctx, text, kind)
}

// compileValue переводит значение FEEL в выражение Go: переменные процесса - поля модели,
// тип выводится по месту использования (для подсказки о недостающих полях)
func compileValue(ctx exprContext, text string, kind valueKind) (string, error) {
	text = strings.TrimSpace(text)
	if text == "null" {
		return "nil", nil
	}
	return ctx.rewrite(text, kind)
}

func inputLabel(input dmn.Input) string {
	if label := oneLine(input.Label); label != "" {
		return fmt.Sprintf("%q", label)
	}
	return input.Id
}

func outputLabel(output dmn.Output) string {
	if output.Name != "" {
		return output.Name
	}
	if label := oneLine(output.Label); label != "" {
		return fmt.Sprintf("%q", label)
	}
	return output.Id
}

func outputNames[T dmn.Output | decisionOutput](outputs []T) string {
	var names []string
	for _, output := range outputs {
		switch o := any(output).(type) {
		case dmn.Output:
			names = append(names, o.Name)
		case decisionOutput:
			names = append(names, o.Key)
		}
	}
	return strings.Join(names, ", ")
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func orDash(text string) string {
	if text = oneLine(text); text == "" {
		return "-"
	}
	return text
}

type businessRuleMethodFile struct {
	dataImports
	Package        string
	FuncName       string
	ElementId      string
	Name           string
	DecisionId     string
	DecisionFunc   string
	ResultVariable string
	// Guard - результат записывается, только если сработало хоть одно правило
	Guard bool
	// Assign - код записи результата в поле модели resultVariable
	Assign string
}

// prepareDecisionFiles генерирует для задач бизнес-правил процесса (и его подпроцессов)
// method_<id задачи>.go и decision_<id решения>.go - по одному на решение, даже если его вызывают несколько задач.
// Недостающие в модели поля попадают в gaps, тогда файлы не пишутся
func prepareDecisionFiles(process *bpmn.Process, decisions map[string]Decision, processPath string, pkg string,
	imports dataImports, m *modelPackage, gaps *modelGaps) error {
	methodTemplate, err := template.New("business_rule_method").Parse(templates.BusinessRuleMethodTemplate)
	if err != nil {
		return err
	}
	decisionTemplate, err := template.New("decision").Parse(templates.DecisionTemplate)
	if err != nil {
		return err
	}

	compiled := make(map[string]*decisionFile)
	for _, task := range process.GetBusinessRuleTasks() {
		called := task.GetCalledDecision()
		decision, found := decisions[called.DecisionId]
		if !found {
			return fmt.Errorf("business rule task %s: решение %s не найдено в DMN-файлах", task.Id, called.DecisionId)
		}

		file, done := compiled[decision.Id]
		if !done {
			gapsBefore := gaps.len()
			file, err = compileDecision(decision, m, gaps)
			if err != nil {
				return fmt.Errorf("решение %s (%s): %w", decision.Id, decision.Path, err)
			}
			compiled[decision.Id] = file
			if gaps.len() == gapsBefore {
				file.Package = pkg
				file.DataModelImport = imports.DataModelImport
				var buf bytes.Buffer
				if err := decisionTemplate.Execute(&buf, file); err != nil {
					return err
				}
				path := filepath.Join(processPath, decisionFilePrefix+utils.ToSnakeCase(decision.Id)+".go")
				if err := utils.SaveGoFile(path, buf.Bytes()); err != nil {
					return fmt.Errorf("решение %s: %w", decision.Id, err)
				}
			}
		}

		path := methodFilePath(processPath, task.Id)
		// Сгенерированные файлы уже удалены, значит существующий файл написан руками
		if exists, err := fileExists(path); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("business rule task %s: %s написан руками, удалите его, чтобы сгенерировать вызов решения", task.Id, path)
		}
		ctx := exprContext{model: m, gaps: gaps, owner: "процесс " + process.Id + ": бизнес-правило " + task.Id}
		gapsBefore := gaps.len()
		assign, err := resultAssign(ctx, file, called.ResultVariable)
		if err != nil {
			return fmt.Errorf("business rule task %s: %w", task.Id, err)
		}
		if gaps.len() > gapsBefore {
			continue
		}
		var buf bytes.Buffer
		err = methodTemplate.Execute(&buf, businessRuleMethodFile{
			dataImports:    imports,
			Package:        pkg,
			FuncName:       utils.ToPascalCase(task.Id),
			ElementId:      task.Id,
			Name:           oneLine(task.Name),
			DecisionId:     decision.Id,
			DecisionFunc:   decisionFuncName(decision.Id),
			ResultVariable: called.ResultVariable,
			Guard:          file.shape == shapeFirst || file.shape == shapeAggregate,
			Assign:         assign,
		})
		if err != nil {
			return err
		}
		if err := utils.SaveGoFile(path, buf.Bytes()); err != nil {
			return fmt.Errorf("business rule task %s: %w", task.Id, err)
		}
	}
	return nil
}

// resultAssign - код записи результата решения в поле модели resultVariable:
//   - один выход - значение в поле (в поле-указатель - адрес), список - срез значений;
//   - несколько выходов - поля структуры модели resultVariable с именами выходов;
//   - список с несколькими выходами - поле-срез структур модели, у структуры поля с именами выходов
func resultAssign(ctx exprContext, file *decisionFile, resultVariable string) (string, error) {
	segments, ok := splitPath(resultVariable)
	if !ok {
		return "", fmt.Errorf("resultVariable %q - не имя переменной", resultVariable)
	}
	single := len(file.outputs) == 1
	element := file.outputs[0].GoType
	if !single {
		element = utils.ToPascalCase(segments[len(segments)-1])
	}
	hint := element
	switch file.shape {
	case shapeList:
		hint = "[]" + element
	case shapeCount:
		hint = "int"
	}
	path, gap := ctx.model.resolvePath(segments)
	if gap != nil {
		if !single && !gap.nested && file.shape == shapeFirst {
			// поле - новая структура модели с полями выходов
			gap.nested = true
		}
		ctx.gaps.add(ctx.owner, *gap, hint)
		return "", nil
	}
	target := path.String()
	if single {
		if _, isPointer := path.leaf.(*ast.StarExpr); isPointer && file.shape != shapeList {
			return target + " = &result", nil
		}
		return target + " = result", nil
	}

	outputsHint := "с полями " + outputNames(file.outputs)
	if file.shape == shapeFirst {
		structName, isStruct := ctx.model.structName(path.leaf)
		if !isStruct {
			return "", fmt.Errorf("результат решения %s - выходы %s: поле %s должно быть структурой модели %s",
				file.DecisionId, outputNames(file.outputs), resultVariable, outputsHint)
		}
		var lines []string
		if _, isPointer := path.leaf.(*ast.StarExpr); isPointer {
			lines = append(lines, "if "+target+" == nil {\n"+target+" = &"+modelDir+"."+structName+"{}\n}")
		}
		for _, output := range file.outputs {
			field, found := ctx.model.field(structName, output.Key)
			if !found {
				ctx.gaps.add(ctx.owner, modelGap{Struct: structName, Key: output.Key}, output.GoType)
				continue
			}
			lines = append(lines, target+"."+field.Name+" = "+addressIfPointer(field.Type, "result."+output.Field))
		}
		return strings.Join(lines, "\n"), nil
	}

	array, isSlice := path.leaf.(*ast.ArrayType)
	var structName string
	if isSlice && array.Len == nil {
		structName, isSlice = ctx.model.structName(array.Elt)
	}
	if !isSlice {
		return "", fmt.Errorf("результат решения %s - список выходов %s: поле %s должно быть срезом структур модели %s",
			file.DecisionId, outputNames(file.outputs), resultVariable, outputsHint)
	}
	var fields []string
	for _, output := range file.outputs {
		field, found := ctx.model.field(structName, output.Key)
		if !found {
			ctx.gaps.add(ctx.owner, modelGap{Struct: structName, Key: output.Key}, output.GoType)
			continue
		}
		fields = append(fields, field.Name+": "+addressIfPointer(field.Type, "item."+output.Field))
	}
	element = modelDir + "." + structName
	literal := element + "{" + strings.Join(fields, ", ") + "}"
	if _, isPointer := array.Elt.(*ast.StarExpr); isPointer {
		element, literal = "*"+element, "&"+literal
	}
	return target + " = []" + element + "{}\nfor _, item := range result {\n" +
		target + " = append(" + target + ", " + literal + ")\n}", nil
}

// addressIfPointer - value или его адрес, если поле - указатель
func addressIfPointer(fieldType ast.Expr, value string) string {
	if _, isPointer := fieldType.(*ast.StarExpr); isPointer {
		return "&" + value
	}
	return value
}

// decisionFuncNames - функции решений, которые вызывают задачи процесса, по имени функции
func decisionFuncNames(process *bpmn.Process) map[string]string {
	names := make(map[string]string)
	for _, task := range process.GetBusinessRuleTasks() {
		id := task.GetCalledDecision().DecisionId
		names[decisionFuncName(id)] = "решение " + id
	}
	return names
}
