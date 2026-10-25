package codegen

import (
	"bytes"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"text/template"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/dmn"
	pgparser "github.com/nicolas-vp/go-bpm/internal/generator/parser"
	"github.com/nicolas-vp/go-bpm/internal/generator/templates"
)

func TestCompileUnaryTests(t *testing.T) {
	ctx := exprContext{model: loadTestModel(t, testModelSource), gaps: newModelGaps(), owner: "test"}
	str := &decisionInput{Name: "name", kind: kindString}
	num := &decisionInput{Name: "amount", kind: kindFloat}
	integer := &decisionInput{Name: "input1", kind: kindInt}
	flag := &decisionInput{Name: "vip", kind: kindBool}
	limit := &decisionInput{Name: "limit", kind: kindInt, nilCheck: "data.Limit == nil"}
	tests := []struct {
		input *decisionInput
		text  string
		want  string
	}{
		{str, "", ""},
		{str, " - ", ""},
		{str, `"foo"`, `name == "foo"`},
		{str, `"foo", "bar"`, `(name == "foo" || name == "bar")`},
		{str, `"a,b", "c"`, `(name == "a,b" || name == "c")`},
		{str, `not("foo", "bar")`, `!((name == "foo" || name == "bar"))`},
		{str, `client.account.number`, `name == data.Client.Account.Number`},
		{limit, `null`, `data.Limit == nil`},
		{limit, `> 5`, `limit > 5`},
		{num, `< 10`, `amount < 10`},
		{num, `>= 1.5`, `amount >= 1.5`},
		{num, `[1..10]`, `amount >= 1 && amount <= 10`},
		{num, `]1..10[`, `amount > 1 && amount < 10`},
		{num, `(1.5..2.5]`, `amount > 1.5 && amount <= 2.5`},
		{num, `[1..5], > 100`, `(amount >= 1 && amount <= 5 || amount > 100)`},
		{num, `< b * 2`, `amount < data.B * 2`},
		{num, `? > b + 1`, `amount > data.B+1`},
		{integer, `5`, `input1 == 5`},
		{flag, `true`, `vip`},
		{flag, `false`, `!vip`},
	}
	for _, test := range tests {
		got, err := compileUnaryTests(ctx, test.text, test.input)
		if err != nil {
			t.Errorf("%q: %v", test.text, err)
			continue
		}
		if got != test.want {
			t.Errorf("%q = %q, want %q", test.text, got, test.want)
		}
	}
	if err := ctx.gaps.err(); err != nil {
		t.Error(err)
	}

	if _, err := compileUnaryTests(ctx, "null", integer); err == nil {
		t.Error("null у входа, который не может быть nil, должен давать ошибку")
	}
	if _, err := compileUnaryTests(ctx, `"a",`, str); err == nil {
		t.Error("пустой тест после запятой должен давать ошибку")
	}
	// переменная ячейки, которой нет в модели, - в подсказку с типом входа
	if _, err := compileUnaryTests(ctx, `< max`, num); err != nil {
		t.Fatal(err)
	}
	if err := ctx.gaps.err(); err == nil || !strings.Contains(err.Error(), "Max float64 `json:\"max\"`") {
		t.Errorf("gaps: %v", err)
	}
}

const decisionsDmn = `<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="d">
  <decision id="Discount" name="Скидка">
    <decisionTable id="t1" hitPolicy="FIRST">
      <input label="Сумма"><inputExpression typeRef="number"><text>amount</text></inputExpression></input>
      <input label="Категория"><inputExpression><text>customer category</text></inputExpression></input>
      <input label="Не проверяется"><inputExpression typeRef="string"><text>unused</text></inputExpression></input>
      <output name="discount" typeRef="number" /><output name="reason" typeRef="string" />
      <rule id="r1"><description>крупная
        покупка</description>
        <inputEntry><text>&gt;= 1000</text></inputEntry><inputEntry><text>"gold"</text></inputEntry><inputEntry><text></text></inputEntry>
        <outputEntry><text>amount * 0.1</text></outputEntry><outputEntry><text>"gold"</text></outputEntry>
      </rule>
      <rule id="r2">
        <inputEntry><text>-</text></inputEntry><inputEntry><text></text></inputEntry><inputEntry><text></text></inputEntry>
        <outputEntry><text>0</text></outputEntry><outputEntry><text></text></outputEntry>
      </rule>
      <rule id="r3">
        <inputEntry><text>1</text></inputEntry><inputEntry><text></text></inputEntry><inputEntry><text></text></inputEntry>
        <outputEntry><text>1</text></outputEntry><outputEntry><text></text></outputEntry>
      </rule>
    </decisionTable>
  </decision>
  <decision id="Tags">
    <decisionTable id="t2" hitPolicy="COLLECT">
      <input><inputExpression typeRef="integer"><text>age</text></inputExpression></input>
      <output name="tag" />
      <rule id="r1"><inputEntry><text>&lt; 18</text></inputEntry><outputEntry><text>"minor"</text></outputEntry></rule>
      <rule id="r2"><inputEntry><text></text></inputEntry><outputEntry><text>"person"</text></outputEntry></rule>
    </decisionTable>
  </decision>
  <decision id="Score">
    <decisionTable id="t3" hitPolicy="COLLECT" aggregation="SUM">
      <input><inputExpression><text>age + 1</text></inputExpression></input>
      <output name="score" typeRef="integer" />
      <rule id="r1"><inputEntry><text>&gt; 18</text></inputEntry><outputEntry><text>10</text></outputEntry></rule>
      <rule id="r2"><inputEntry><text>&gt; 60</text></inputEntry><outputEntry><text>5</text></outputEntry></rule>
    </decisionTable>
  </decision>
  <decision id="Offers">
    <decisionTable id="t6" hitPolicy="COLLECT">
      <input><inputExpression typeRef="boolean"><text>vip</text></inputExpression></input>
      <output name="discount" typeRef="number" /><output name="reason" typeRef="string" />
      <rule id="r1"><inputEntry><text>true</text></inputEntry><outputEntry><text>0.1</text></outputEntry><outputEntry><text>"vip"</text></outputEntry></rule>
    </decisionTable>
  </decision>
  <decision id="Limit">
    <decisionTable id="t5">
      <input><inputExpression><text>limit</text></inputExpression></input>
      <output name="reason" typeRef="string" />
      <rule id="r1"><inputEntry><text>null</text></inputEntry><outputEntry><text>"нет лимита"</text></outputEntry></rule>
      <rule id="r2"><inputEntry><text>&gt; 10</text></inputEntry><outputEntry><text>"большой"</text></outputEntry></rule>
    </decisionTable>
  </decision>
  <decision id="Count">
    <decisionTable id="t4" hitPolicy="COLLECT" aggregation="COUNT">
      <input><inputExpression typeRef="boolean"><text>vip</text></inputExpression></input>
      <output name="n" />
      <rule id="r1"><inputEntry><text>true</text></inputEntry><outputEntry><text>1</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

// generateDecision компилирует решение, рендерит файл и проверяет, что он разбирается как Go
func generateDecision(t *testing.T, decisions map[string]Decision, id string) string {
	t.Helper()
	gaps := newModelGaps()
	file, err := compileDecision(decisions[id], loadTestModel(t, testModelSource), gaps)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	if err := gaps.err(); err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	file.Package = "p"
	file.DataModelImport = "example.com/svc/gen/model"
	var buf bytes.Buffer
	if err := template.Must(template.New("decision").Parse(templates.DecisionTemplate)).Execute(&buf, file); err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), id+".go", buf.Bytes(), 0); err != nil {
		t.Fatalf("%s: %v\n%s", id, err, buf.String())
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return string(formatted)
}

func parseDecisions(t *testing.T, source string) map[string]Decision {
	t.Helper()
	definitions, err := pgparser.ParseDmn([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	definitions.Path = "rules.dmn"
	decisions, err := CollectDecisions([]dmn.Definitions{*definitions})
	if err != nil {
		t.Fatal(err)
	}
	return decisions
}

func TestCompileDecisions(t *testing.T) {
	decisions := parseDecisions(t, decisionsDmn)

	code := generateDecision(t, decisions, "Discount")
	for _, want := range []string{
		"type decideDiscountResult struct {\n\tDiscount float64 `json:\"discount\"`\n\tReason   string  `json:\"reason\"`\n}",
		`func decideDiscount(data *model.DataModel) (decideDiscountResult, []string) {`,
		`amount := data.Amount   // Сумма`,
		// имя с пробелами без typeRef: тип из поля модели, локальная переменная inputN
		`input2 := data.Category`,
		`// r1 - крупная покупка: >= 1000 | "gold" | - => amount * 0.1 | "gold"`,
		`if amount >= 1000 && input2 == "gold" {`,
		`return decideDiscountResult{Discount: data.Amount * 0.1, Reason: "gold"}, []string{"r1"}`,
		// пустая ячейка - нулевое значение типа выхода
		`return decideDiscountResult{Discount: 0, Reason: ""}, []string{"r2"}`,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("Discount: нет %q в\n%s", want, code)
		}
	}
	// вход, который не проверяет ни одно правило, не объявляется; правила после безусловного не генерируются
	for _, unwanted := range []string{"unused", "r3", "return decideDiscountResult{}, nil"} {
		if strings.Contains(code, unwanted) {
			t.Errorf("Discount: лишнее %q в\n%s", unwanted, code)
		}
	}

	code = generateDecision(t, decisions, "Tags")
	for _, want := range []string{"func decideTags(data *model.DataModel) ([]string, []string) {", "age := data.Age", "result := []string{}",
		"if age < 18 {\n\t\trules = append(rules, \"r1\")\n\t\tresult = append(result, \"minor\")",
		"\trules = append(rules, \"r2\")\n\tresult = append(result, \"person\")\n", "return result, rules"} {
		if !strings.Contains(code, want) {
			t.Errorf("Tags: нет %q в\n%s", want, code)
		}
	}
	code = generateDecision(t, decisions, "Score")
	for _, want := range []string{"hit policy COLLECT SUM", "(int, []string)", "input1 := data.Age + 1", "if input1 > 18 {", "var values []int",
		"values = append(values, 10)", "return 0, nil", "result += value", "return result, rules"} {
		if !strings.Contains(code, want) {
			t.Errorf("Score: нет %q в\n%s", want, code)
		}
	}
	code = generateDecision(t, decisions, "Count")
	for _, want := range []string{"(int, []string)", "if vip {\n\t\trules = append(rules, \"r1\")\n\t\tcount++", "return count, rules"} {
		if !strings.Contains(code, want) {
			t.Errorf("Count: нет %q в\n%s", want, code)
		}
	}
	code = generateDecision(t, decisions, "Limit")
	for _, want := range []string{"limit := bpm.Deref(data.Limit)", "if data.Limit == nil {", "if limit > 10 {"} {
		if !strings.Contains(code, want) {
			t.Errorf("Limit: нет %q в\n%s", want, code)
		}
	}
}

func TestResultAssign(t *testing.T) {
	m := loadTestModel(t, testModelSource)
	decisions := parseDecisions(t, decisionsDmn)
	compile := func(id string) *decisionFile {
		file, err := compileDecision(decisions[id], m, newModelGaps())
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	tests := []struct {
		decision, resultVariable, want string
	}{
		{"Discount", "discount", "data.Discount.Value = result.Discount\ndata.Discount.Reason = &result.Reason"},
		{"Offers", "lines", "data.Lines = []model.Line{}\nfor _, item := range result {\ndata.Lines = append(data.Lines, model.Line{Discount: item.Discount, Reason: item.Reason})\n}"},
		{"Tags", "tags", "data.Tags = result"},
		{"Score", "score", "data.Score = result"},
		{"Limit", "reason", "data.Reason = &result"},
		{"Tags", "client.account.number", "data.Client.Account.Number = result"},
	}
	for _, test := range tests {
		gaps := newModelGaps()
		got, err := resultAssign(exprContext{model: m, gaps: gaps, owner: "test"}, compile(test.decision), test.resultVariable)
		if err != nil || gaps.len() > 0 {
			t.Errorf("%s -> %s: %v, gaps %v", test.decision, test.resultVariable, err, gaps.err())
			continue
		}
		if got != test.want {
			t.Errorf("%s -> %s:\n%s\nwant\n%s", test.decision, test.resultVariable, got, test.want)
		}
	}

	// несколько выходов, а поле не структура и не срез структур
	for decision, resultVariable := range map[string]string{"Discount": "a", "Offers": "tags"} {
		if _, err := resultAssign(exprContext{model: m, gaps: newModelGaps()}, compile(decision), resultVariable); err == nil {
			t.Errorf("%s -> %s: нет ошибки", decision, resultVariable)
		}
	}
	// поля нет: подсказка с типом результата
	for decision, want := range map[string]string{
		"Tags":     "Out []string `json:\"out\"`",
		"Count":    "Out int `json:\"out\"`",
		"Discount": "Out Out `json:\"out\"` // структура модели",
	} {
		gaps := newModelGaps()
		if _, err := resultAssign(exprContext{model: m, gaps: gaps, owner: "test"}, compile(decision), "out"); err != nil {
			t.Fatal(err)
		}
		if err := gaps.err(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", decision, err, want)
		}
	}
}

func TestDecisionErrors(t *testing.T) {
	table := func(attrs, input, rule string) string {
		return `<definitions><decision id="D"><decisionTable ` + attrs + `>` + input +
			`<output name="x" typeRef="string" />` + rule + `</decisionTable></decision></definitions>`
	}
	typed := `<input><inputExpression typeRef="string"><text>a</text></inputExpression></input>`
	rule := `<rule id="r"><inputEntry><text>"x"</text></inputEntry><outputEntry><text>"y"</text></outputEntry></rule>`
	tests := map[string]string{
		"priority":      table(`hitPolicy="PRIORITY"`, typed, rule),
		"no type":       table(``, `<input><inputExpression><text>a + 1</text></inputExpression></input>`, `<rule id="r"><inputEntry><text>-</text></inputEntry><outputEntry><text>"y"</text></outputEntry></rule>`),
		"cells":         table(``, typed, `<rule id="r"><outputEntry><text>"y"</text></outputEntry></rule>`),
		"sum of string": table(`hitPolicy="COLLECT" aggregation="SUM"`, typed, rule),
		"literal":       `<definitions><decision id="D"><literalExpression><text>1</text></literalExpression></decision></definitions>`,
		"required": `<definitions><decision id="D"><informationRequirement><requiredDecision href="#Other" /></informationRequirement>` +
			`<decisionTable>` + typed + `<output name="x" typeRef="string" />` + rule + `</decisionTable></decision></definitions>`,
	}
	for name, source := range tests {
		if _, err := compileDecision(parseDecisions(t, source)["D"], loadTestModel(t, testModelSource), newModelGaps()); err == nil {
			t.Errorf("%s: нет ошибки", name)
		}
	}

	a := dmn.Definitions{Path: "a.dmn", Decisions: []dmn.Decision{{Id: "D"}}}
	b := dmn.Definitions{Path: "b.dmn", Decisions: []dmn.Decision{{Id: "D"}}}
	if _, err := CollectDecisions([]dmn.Definitions{a, b}); err == nil {
		t.Error("одинаковый id решения в двух файлах должен давать ошибку")
	}
}

func TestBusinessRuleTask(t *testing.T) {
	process := func(extension string) string {
		return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:process id="Rules">
    <bpmn:startEvent id="start"><bpmn:outgoing>f1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:businessRuleTask id="rule">
      <bpmn:extensionElements>` + extension + `</bpmn:extensionElements>
      <bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing>
    </bpmn:businessRuleTask>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="rule" />
    <bpmn:sequenceFlow id="f2" sourceRef="rule" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`
	}
	expressions, err := parseEvents(t, process(`<zeebe:calledDecision decisionId="D" resultVariable="r" />`))
	if err != nil {
		t.Fatal(err)
	}
	if rule := expressions["rule"]; rule.ActivateType != bpm.ActivateTypeMethod || rule.MethodFunc != "Rule" {
		t.Errorf("rule = %+v, want Method Rule", rule)
	}
	for name, extension := range map[string]string{
		"no result":   `<zeebe:calledDecision decisionId="D" />`,
		"no decision": ``,
		"job worker":  `<zeebe:taskDefinition type="rules" />`,
	} {
		if _, err := parseEvents(t, process(extension)); err == nil {
			t.Errorf("%s: нет ошибки", name)
		}
	}
}
