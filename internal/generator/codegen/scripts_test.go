package codegen

import (
	"strings"
	"testing"
)

func TestScriptCode(t *testing.T) {
	m := loadTestModel(t, testModelSource)
	tests := []struct {
		expression, resultVariable, want string
	}{
		{"a + 1", "score", "data.Score = data.A + 1"},
		{`name + "!"`, "client.account.number", `data.Client.Account.Number = data.Name + "!"`},
		// код Go: поля модели переводятся, пакеты и локальные переменные - нет
		{`fmt.Println("Hey", name)`, "", `fmt.Println("Hey", data.Name)`},
		{"x := a * 2\nscore = x\nclient.account.number = fmt.Sprint(x)", "",
			"x := data.A * 2\ndata.Score = x\ndata.Client.Account.Number = fmt.Sprint(x)"},
		{"", "score", ""},
	}
	for _, test := range tests {
		gaps := newModelGaps()
		got, err := scriptCode(exprContext{model: m, gaps: gaps, owner: "test"}, test.expression, test.resultVariable)
		if err != nil || gaps.len() > 0 {
			t.Errorf("%q: %v, gaps %v", test.expression, err, gaps.err())
			continue
		}
		if got != test.want {
			t.Errorf("%q:\n%s\nwant\n%s", test.expression, got, test.want)
		}
	}

	gaps := newModelGaps()
	ctx := exprContext{model: m, gaps: gaps, owner: "процесс P: скрипт S"}
	if _, err := scriptCode(ctx, "total * 2", "result"); err != nil {
		t.Fatal(err)
	}
	if err := gaps.err(); err == nil || !strings.Contains(err.Error(), "Total int `json:\"total\"`") ||
		!strings.Contains(err.Error(), "Result int `json:\"result\"`") {
		t.Errorf("gaps: %v", err)
	}
}
