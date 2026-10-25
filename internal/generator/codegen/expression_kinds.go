package codegen

import (
	"go/ast"
	"go/token"
	"strings"
	"unicode"
)

// valueKind - тип значения переменной процесса или выражения
type valueKind int

const (
	kindUnknown valueKind = iota
	kindInt
	kindFloat
	kindString
	kindBool
)

// kindInferer выводит тип переменных выражения по месту использования: "a == 55" - int,
// "name == \"x\"" - string, "approved && a > 1.5" - bool и float64. kinds - переменные выражения
// с уже известным типом (kindUnknown - вывести); найденные типы записываются туда же
type kindInferer struct {
	kinds map[string]valueKind
	// known - имена, тип которых задан заранее и не выводится
	known map[string]bool
}

// visit проходит выражение; want - ожидаемый тип всего выражения (kindBool для условий)
func (t *kindInferer) visit(expr ast.Expr, want valueKind) {
	if t.known == nil {
		t.known = make(map[string]bool)
		for name, kind := range t.kinds {
			if kind != kindUnknown {
				t.known[name] = true
			}
		}
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if kind, ok := t.kinds[e.Name]; ok && kind == kindUnknown && want != kindUnknown {
			t.kinds[e.Name] = want
		}
	case *ast.ParenExpr:
		t.visit(e.X, want)
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			want = kindBool
		}
		t.visit(e.X, want)
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LAND, token.LOR:
			want = kindBool
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			want = t.operandsKind(e)
		default:
			// Арифметика берёт тип снаружи (a + 1 > 2.5 - float), а если его нет - от операндов
			if kind := t.operandsKind(e); want == kindUnknown || kind != kindInt && kind != kindUnknown {
				want = kind
			}
		}
		t.visit(e.X, want)
		t.visit(e.Y, want)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			t.visit(arg, kindUnknown)
		}
	}
}

// operandsKind - тип операндов бинарного выражения; int и float вместе дают float
func (t *kindInferer) operandsKind(e *ast.BinaryExpr) valueKind {
	x, y := t.kindOf(e.X), t.kindOf(e.Y)
	switch {
	case x == kindUnknown:
		return y
	case y == kindUnknown:
		return x
	case x == kindFloat && y == kindInt || x == kindInt && y == kindFloat:
		return kindFloat
	}
	return x
}

// kindOf - тип выражения, если он понятен без выводимых переменных
func (t *kindInferer) kindOf(expr ast.Expr) valueKind {
	switch e := expr.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT:
			return kindInt
		case token.FLOAT:
			return kindFloat
		case token.STRING, token.CHAR:
			return kindString
		}
	case *ast.Ident:
		if t.known[e.Name] {
			return t.kinds[e.Name]
		}
		if e.Name == "true" || e.Name == "false" {
			return kindBool
		}
	case *ast.ParenExpr:
		return t.kindOf(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			return kindBool
		}
		return t.kindOf(e.X)
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LAND, token.LOR, token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			return kindBool
		}
		return t.operandsKind(e)
	}
	return kindUnknown
}

// feelToGo переводит операторы FEEL в Go вне строковых литералов: and -> &&, or -> ||, not( -> !(, = -> ==
func feelToGo(expression string) string {
	var out strings.Builder
	runes := []rune(expression)
	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case r == '"' || r == '`':
			j := i + 1
			for j < len(runes) && runes[j] != r {
				if runes[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(runes))
			out.WriteString(string(runes[i:j]))
			i = j
		case unicode.IsLetter(r) || r == '_':
			j := i
			for j < len(runes) && isIdentRune(runes[j]) {
				j++
			}
			switch word := string(runes[i:j]); {
			case word == "and" && prevNonSpace(runes, i) != '.':
				out.WriteString("&&")
			case word == "or" && prevNonSpace(runes, i) != '.':
				out.WriteString("||")
			case word == "not" && nextNonSpace(runes, j) == '(':
				out.WriteString("!")
			default:
				out.WriteString(word)
			}
			i = j
		case r == '=' && !strings.ContainsRune("=!<>:", prevRune(runes, i)) && nextRune(runes, i) != '=':
			out.WriteString("==")
			i++
		default:
			out.WriteRune(r)
			i++
		}
	}
	return out.String()
}

func prevRune(runes []rune, i int) rune {
	if i > 0 {
		return runes[i-1]
	}
	return 0
}

func nextRune(runes []rune, i int) rune {
	if i+1 < len(runes) {
		return runes[i+1]
	}
	return 0
}
