package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testModelSource - модель для тестов: вложенные структуры, указатели, встроенная структура и срезы
const testModelSource = "package model\n\nimport \"time\"\n\n" +
	"type DataModel struct {\n" +
	"\tA int `json:\"a\"`\n" +
	"\tB float64 `json:\"b\"`\n" +
	"\tName string `json:\"name\"`\n" +
	"\tApproved bool `json:\"approved\"`\n" +
	"\tAmount float64 `json:\"amount\"`\n" +
	"\tCategory string `json:\"customer category\"`\n" +
	"\tAge int `json:\"age\"`\n" +
	"\tVip bool `json:\"vip\"`\n" +
	"\tLimit *int `json:\"limit\"`\n" +
	"\tClient *Client `json:\"client\"`\n" +
	"\tCreated time.Time `json:\"created\"`\n" +
	"\tDiscount Discount `json:\"discount\"`\n" +
	"\tTags []string `json:\"tags\"`\n" +
	"\tLines []Line `json:\"lines\"`\n" +
	"\tScore int `json:\"score\"`\n" +
	"\tReason *string `json:\"reason\"`\n" +
	"\tHidden string `json:\"-\"`\n" +
	"\tNoTag string\n" +
	"\tAudit\n" +
	"}\n\n" +
	"type Audit struct {\n\tAuthor string `json:\"author\"`\n\tA string `json:\"a\"`\n}\n\n" +
	"type Client struct {\n\tAccount Account `json:\"account\"`\n}\n\n" +
	"type Account struct {\n\tNumber string `json:\"number\"`\n}\n\n" +
	"type Discount struct {\n\tValue float64 `json:\"discount\"`\n\tReason *string `json:\"reason\"`\n}\n\n" +
	"type Line struct {\n\tDiscount float64 `json:\"discount\"`\n\tReason string `json:\"reason\"`\n}\n"

// loadTestModel пишет пакет model во временную папку вывода и читает его
func loadTestModel(t *testing.T, source string) *modelPackage {
	t.Helper()
	outputPath := t.TempDir()
	dir := filepath.Join(outputPath, modelDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data_model.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := loadModel(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestResolvePath(t *testing.T) {
	m := loadTestModel(t, testModelSource)
	tests := map[string]string{
		"a":                     "data.A",
		"customer category":     "data.Category",
		"client.account.number": "data.Client.Account.Number",
		// поле встроенной структуры поднимается, но поле верхнего уровня важнее
		"author": "data.Author",
		// без тега - имя поля
		"NoTag": "data.NoTag",
		// time.Time не структура модели: дальше путь как есть
		"created.Year": "data.Created.Year",
	}
	for text, want := range tests {
		segments, ok := splitPath(text)
		if !ok {
			t.Errorf("%q: не путь", text)
			continue
		}
		path, gap := m.resolvePath(segments)
		if gap != nil || path.String() != want {
			t.Errorf("%q = %q, gap %+v; want %q", text, path.String(), gap, want)
		}
	}
	for text, want := range map[string]modelGap{
		"hidden":           {Struct: "DataModel", Key: "hidden"},
		"Hidden":           {Struct: "DataModel", Key: "Hidden"},
		"client.card.id":   {Struct: "Client", Key: "card", nested: true},
		"client.account.x": {Struct: "Account", Key: "x"},
	} {
		segments, _ := splitPath(text)
		if _, gap := m.resolvePath(segments); gap == nil || *gap != want {
			t.Errorf("%q: gap %+v, want %+v", text, gap, want)
		}
	}
}

func TestRewrite(t *testing.T) {
	m := loadTestModel(t, testModelSource)
	tests := map[string]string{
		"a == 55":                       "data.A == 55",
		`name = "x" and approved`:       `data.Name == "x" && data.Approved`,
		"not(approved) or amount > 1.5": "!(data.Approved) || data.Amount > 1.5",
		"len(name) > 0":                 "len(data.Name) > 0",
		`client.account.number != ""`:   `data.Client.Account.Number != ""`,
		"created.Year() > 2000":         "data.Created.Year() > 2000",
	}
	for source, want := range tests {
		gaps := newModelGaps()
		got, err := exprContext{model: m, gaps: gaps, owner: "test"}.rewrite(source, kindBool)
		if err != nil || gaps.len() > 0 {
			t.Errorf("%q: gaps %v, err %v", source, gaps.err(), err)
			continue
		}
		if got != want {
			t.Errorf("%q = %q, want %q", source, got, want)
		}
	}

	gaps := newModelGaps()
	ctx := exprContext{model: m, gaps: gaps, owner: "процесс P: условие G"}
	if _, err := ctx.rewrite(`x > 1.5 && y == "s" && client.card.id == 1 && flag`, kindBool); err != nil {
		t.Fatal(err)
	}
	if _, err := ctx.rewrite(`a == 1`, kindBool); err != nil {
		t.Fatal(err)
	}
	ctx.owner = "решение D"
	if _, err := ctx.rewrite(`x > 2`, kindBool); err != nil {
		t.Fatal(err)
	}
	err := gaps.err()
	if err == nil {
		t.Fatal("нет ошибки о недостающих полях")
	}
	for _, part := range []string{
		"model.DataModel:\n\t\tFlag bool `json:\"flag\"`\n\t\tX float64 `json:\"x\"`\n\t\tY string `json:\"y\"`",
		"model.Client:\n\t\tCard Card `json:\"card\"` // структура модели",
		"используются в:\n\tпроцесс P: условие G\n\tрешение D",
	} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("в ошибке нет %q:\n%v", part, err)
		}
	}

	if _, err := (exprContext{model: m, gaps: newModelGaps()}).rewrite("a ==", kindBool); err == nil {
		t.Error("выражение не разбирается: нет ошибки")
	}
}

func TestLoadModelWithoutDataModel(t *testing.T) {
	outputPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outputPath, modelDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputPath, modelDir, "x.go"), []byte("package model\n\ntype Other struct{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadModel(outputPath); err == nil {
		t.Error("нет ошибки")
	}
}
