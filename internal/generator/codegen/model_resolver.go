package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/ast/astutil"

	"github.com/nicolas-vp/go-bpm/internal/generator/utils"
)

// Модель данных жёсткая: любая переменная, которую используют условия, скрипты и решения DMN,
// должна быть полем DataModel. Переменная a - поле с тегом json:"a" (без тега - поле с именем a),
// путь client.account.number - поля вложенных структур пакета model: data.Client.Account.Number.
// Генератор читает рукописный пакет model и не правит его: недостающие поля собираются в modelGaps,
// и генерация останавливается с подсказкой, что добавить

// dataModelType - структура модели данных в пакете modelDir
const dataModelType = "DataModel"

// dataParam - параметр сгенерированных функций с моделью данных
const dataParam = "data"

// modelPackage - структуры рукописного пакета model по имени
type modelPackage struct {
	structs map[string]*ast.StructType
}

// loadModel читает структуры пакета outputPath/model
func loadModel(outputPath string) (*modelPackage, error) {
	dir := filepath.Join(outputPath, modelDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	m := &modelPackage{structs: make(map[string]*ast.StructType)}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("модель данных: %w", err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if spec, ok := node.(*ast.TypeSpec); ok {
				if st, ok := spec.Type.(*ast.StructType); ok {
					m.structs[spec.Name.Name] = st
				}
			}
			return true
		})
	}
	if m.structs[dataModelType] == nil {
		return nil, fmt.Errorf("в %s нет структуры %s", dir, dataModelType)
	}
	return m, nil
}

// modelField - поле структуры модели
type modelField struct {
	Name string
	Type ast.Expr
}

// field ищет поле структуры structName по ключу JSON, как encoding/json при записи: тег json,
// без тега - имя поля. Поля встроенных структур без тега поднимаются; поле верхнего уровня важнее
func (m *modelPackage) field(structName, key string) (modelField, bool) {
	return m.findField(structName, key, make(map[string]bool))
}

func (m *modelPackage) findField(structName, key string, seen map[string]bool) (modelField, bool) {
	st := m.structs[structName]
	if st == nil || seen[structName] {
		return modelField{}, false
	}
	seen[structName] = true
	var embedded []string
	for _, field := range st.Fields.List {
		jsonKey := ""
		if field.Tag != nil {
			tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
			jsonKey, _, _ = strings.Cut(tag.Get("json"), ",")
		}
		if jsonKey == "-" {
			continue
		}
		if len(field.Names) == 0 {
			name, _ := m.structName(field.Type)
			if jsonKey == "" {
				embedded = append(embedded, name)
			} else if jsonKey == key && ast.IsExported(name) {
				return modelField{Name: name, Type: field.Type}, true
			}
			continue
		}
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			if fieldKey := cmpOr(jsonKey, name.Name); fieldKey == key {
				return modelField{Name: name.Name, Type: field.Type}, true
			}
		}
	}
	for _, name := range embedded {
		if field, ok := m.findField(name, key, seen); ok {
			return field, true
		}
	}
	return modelField{}, false
}

// structName - имя структуры модели, если тип - она или указатель на неё
func (m *modelPackage) structName(t ast.Expr) (string, bool) {
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	ident, ok := t.(*ast.Ident)
	if !ok || m.structs[ident.Name] == nil {
		return "", false
	}
	return ident.Name, true
}

// modelGap - поля key нет в структуре Struct; nested - путь идёт дальше, значит поле должно быть структурой
type modelGap struct {
	Struct string
	Key    string
	nested bool
}

// modelPath - путь переменной, переведённый в Go: data.Client.Account.Number
type modelPath struct {
	expr ast.Expr
	// leaf - тип последнего поля модели в пути
	leaf ast.Expr
}

func (p modelPath) String() string {
	return exprString(p.expr)
}

// resolvePath переводит путь переменной (client, account, number) в поля модели.
// Когда путь доходит до поля, тип которого не структура модели (time.Time, map), остаток пути
// пишется как есть: это поля или методы этого типа, их проверит сборка
func (m *modelPackage) resolvePath(segments []string) (modelPath, *modelGap) {
	path := modelPath{expr: ast.NewIdent(dataParam)}
	current := dataModelType
	for i, segment := range segments {
		if current == "" {
			path.expr = &ast.SelectorExpr{X: path.expr, Sel: ast.NewIdent(segment)}
			continue
		}
		field, ok := m.field(current, segment)
		if !ok {
			return modelPath{}, &modelGap{Struct: current, Key: segment, nested: i < len(segments)-1}
		}
		path.expr = &ast.SelectorExpr{X: path.expr, Sel: ast.NewIdent(field.Name)}
		path.leaf = field.Type
		current, _ = m.structName(field.Type)
	}
	return path, nil
}

// splitPath делит путь переменной по точкам: "client.account.number"; сегмент FEEL может содержать пробелы
func splitPath(text string) ([]string, bool) {
	segments := strings.Split(strings.TrimSpace(text), ".")
	for i, segment := range segments {
		segment = strings.TrimSpace(segment)
		if !isPlainVariable(segment) && !spacedName.MatchString(segment) {
			return nil, false
		}
		segments[i] = segment
	}
	return segments, true
}

// modelGaps - поля, которых не хватает в модели, по структурам, и где они используются
type modelGaps struct {
	fields map[string]map[string]string
	owners []string
}

func newModelGaps() *modelGaps {
	return &modelGaps{fields: make(map[string]map[string]string)}
}

// add запоминает, что owner нужно поле gap; goType - тип поля для подсказки
func (g *modelGaps) add(owner string, gap modelGap, goType string) {
	if gap.nested {
		goType = utils.ToPascalCase(gap.Key)
	}
	if g.fields[gap.Struct] == nil {
		g.fields[gap.Struct] = make(map[string]string)
	}
	if existing, ok := g.fields[gap.Struct][gap.Key]; !ok || existing == "any" {
		g.fields[gap.Struct][gap.Key] = goType
	}
	for _, o := range g.owners {
		if o == owner {
			return
		}
	}
	g.owners = append(g.owners, owner)
}

// merge добавляет недостающие поля other
func (g *modelGaps) merge(other *modelGaps) {
	for structName, fields := range other.fields {
		for key, goType := range fields {
			for _, owner := range other.owners {
				g.add(owner, modelGap{Struct: structName, Key: key}, goType)
			}
		}
	}
}

func (g *modelGaps) len() int {
	return len(g.owners)
}

// err - ошибка со списком полей, которые нужно добавить в модель; nil, если всего хватает
func (g *modelGaps) err() error {
	if len(g.owners) == 0 {
		return nil
	}
	var out strings.Builder
	out.WriteString("переменных нет в модели данных " + modelDir + ", добавьте поля:")
	structs := make([]string, 0, len(g.fields))
	for name := range g.fields {
		structs = append(structs, name)
	}
	sort.Strings(structs)
	for _, name := range structs {
		fmt.Fprintf(&out, "\n\t%s.%s:", modelDir, name)
		keys := make([]string, 0, len(g.fields[name]))
		for key := range g.fields[name] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			goType := g.fields[name][key]
			fmt.Fprintf(&out, "\n\t\t%s %s `json:%q`", utils.ToPascalCase(key), goType, key)
			if _, isStruct := g.fields[utils.ToPascalCase(key)]; isStruct || goType == utils.ToPascalCase(key) {
				out.WriteString(" // структура модели")
			}
		}
	}
	out.WriteString("\nиспользуются в:")
	for _, owner := range g.owners {
		out.WriteString("\n\t" + owner)
	}
	return fmt.Errorf("%s", out.String())
}

// localVar - имя в выражении, которое не переменная процесса: вход решения DMN
type localVar struct {
	kind valueKind
	// name - на что заменить имя в Go
	name string
}

// exprContext - перевод выражений процесса (FEEL или Go) на модель данных
type exprContext struct {
	model *modelPackage
	gaps  *modelGaps
	// owner - где используется выражение, для подсказки о недостающих полях
	owner  string
	locals map[string]localVar
}

// rewrite переводит выражение в Go на модели данных: каждая переменная или путь (a, client.account)
// становится полем data. Переменная, которой нет в модели, попадает в gaps с типом по месту
// использования; want - ожидаемый тип всего выражения (kindBool для условий).
// Выражения на FEEL (and, or, not(), одиночное =) переводятся в Go
func (c exprContext) rewrite(expression string, want valueKind) (string, error) {
	expr, err := parser.ParseExpr(expression)
	if err != nil {
		var feelErr error
		if expr, feelErr = parser.ParseExpr(feelToGo(expression)); feelErr != nil {
			return "", fmt.Errorf("выражение %q не разбирается как Go: %w", expression, err)
		}
	}

	// пути переменных заменяются заглушками, чтобы вывести их тип по месту использования
	paths := make(map[string][]string)
	kinds := make(map[string]valueKind)
	for name, local := range c.locals {
		kinds[name] = local.kind
	}
	expr = astutil.Apply(expr, func(cursor *astutil.Cursor) bool {
		switch cursor.Name() {
		case "Sel", "Key":
			return false
		}
		node, ok := cursor.Node().(ast.Expr)
		if !ok {
			return true
		}
		segments := chainSegments(node)
		if segments == nil {
			return true
		}
		root := segments[0]
		if _, isLocal := c.locals[root]; isLocal || isConstName(root) {
			return false
		}
		if call, isCall := cursor.Parent().(*ast.CallExpr); isCall && call.Fun == node && len(segments) == 1 {
			// вызов функции: len(x), min(a, b)
			return true
		}
		placeholder := fmt.Sprintf("__path%d__", len(paths))
		paths[placeholder] = segments
		kinds[placeholder] = kindUnknown
		cursor.Replace(ast.NewIdent(placeholder))
		return false
	}, nil).(ast.Expr)

	(&kindInferer{kinds: kinds}).visit(expr, want)

	expr = astutil.Apply(expr, func(cursor *astutil.Cursor) bool {
		ident, ok := cursor.Node().(*ast.Ident)
		if !ok {
			return true
		}
		if local, isLocal := c.locals[ident.Name]; isLocal && cursor.Name() != "Sel" {
			cursor.Replace(ast.NewIdent(local.name))
			return true
		}
		segments, isPath := paths[ident.Name]
		if !isPath {
			return true
		}
		path, gap := c.model.resolvePath(segments)
		if gap != nil {
			c.gaps.add(c.owner, *gap, kindGoType(kinds[ident.Name]))
			cursor.Replace(ast.NewIdent("nil"))
			return true
		}
		cursor.Replace(path.expr)
		return true
	}, nil).(ast.Expr)
	return exprString(expr), nil
}

// expressionKind - тип выражения по литералам и операторам: name + "!" - string, a > 1 - bool
func expressionKind(expression string) valueKind {
	expr, err := parser.ParseExpr(expression)
	if err != nil {
		if expr, err = parser.ParseExpr(feelToGo(expression)); err != nil {
			return kindUnknown
		}
	}
	return (&kindInferer{kinds: map[string]valueKind{}}).kindOf(expr)
}

// chainSegments - путь a.b.c, если выражение - только имена через точку; иначе nil
func chainSegments(expr ast.Expr) []string {
	switch e := expr.(type) {
	case *ast.Ident:
		return []string{e.Name}
	case *ast.SelectorExpr:
		if prefix := chainSegments(e.X); prefix != nil {
			return append(prefix, e.Sel.Name)
		}
	}
	return nil
}

// isConstName - встроенные константы Go, это не переменные процесса
func isConstName(name string) bool {
	switch name {
	case "true", "false", "nil", "iota":
		return true
	}
	return false
}

// kindGoType - тип Go для подсказки и результата решения
func kindGoType(kind valueKind) string {
	switch kind {
	case kindInt:
		return "int"
	case kindFloat:
		return "float64"
	case kindString:
		return "string"
	case kindBool:
		return "bool"
	}
	return "any"
}

// leafKind - тип значения поля модели, если он простой: int, float64, string, bool и указатели на них
func leafKind(t ast.Expr) valueKind {
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	ident, ok := t.(*ast.Ident)
	if !ok {
		return kindUnknown
	}
	switch ident.Name {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return kindInt
	case "float32", "float64":
		return kindFloat
	case "string":
		return kindString
	case "bool":
		return kindBool
	}
	return kindUnknown
}

// isNillable - значение поля можно сравнить с nil: указатель, map, срез, интерфейс
func isNillable(t ast.Expr) bool {
	switch e := t.(type) {
	case *ast.StarExpr, *ast.MapType, *ast.InterfaceType, *ast.FuncType, *ast.ChanType:
		return true
	case *ast.ArrayType:
		return e.Len == nil
	case *ast.Ident:
		return e.Name == "any" || e.Name == "error"
	}
	return false
}

func exprString(node ast.Node) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), node); err != nil {
		panic(err)
	}
	return buf.String()
}

func cmpOr(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
