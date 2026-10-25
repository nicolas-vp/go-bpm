package utils

import (
	"strings"
	"unicode"

	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

// ProcessDirName возвращает имя папки для сгенерированного кода процесса:
// name процесса в snake_case, а если в нём нет латиницы (например "Пустой процесс 2") — id процесса
func ProcessDirName(process *bpmn.Process) string {
	if dir := ToSnakeCase(process.Name); strings.IndexFunc(dir, isASCIILower) >= 0 {
		return dir
	}
	return ToSnakeCase(process.Id)
}

// ToSnakeCase оставляет только латиницу и цифры, остальное заменяет на "_"
// и разбивает camelCase: "MySimpleMethod" -> "my_simple_method", "HTTPServer" -> "http_server"
func ToSnakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	pendingSep := false
	for i, r := range runes {
		if !isASCIIAlnum(r) {
			pendingSep = true
			continue
		}
		if isASCIIUpper(r) && i > 0 {
			prev := runes[i-1]
			nextIsLower := i+1 < len(runes) && isASCIILower(runes[i+1])
			if isASCIILower(prev) || unicode.IsDigit(prev) || (isASCIIUpper(prev) && nextIsLower) {
				pendingSep = true
			}
		}
		if pendingSep && b.Len() > 0 {
			b.WriteByte('_')
		}
		pendingSep = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func isASCIIUpper(r rune) bool {
	return r >= 'A' && r <= 'Z'
}

func isASCIILower(r rune) bool {
	return r >= 'a' && r <= 'z'
}

func isASCIIAlnum(r rune) bool {
	return isASCIIUpper(r) || isASCIILower(r) || (r >= '0' && r <= '9')
}

// ToPascalCase делает из id экспортируемый Go-идентификатор: "Gateway_1nptppd" -> "Gateway1nptppd"
func ToPascalCase(s string) string {
	var b strings.Builder
	upperNext := true
	for _, r := range s {
		if isASCIIAlnum(r) {
			if upperNext {
				r = unicode.ToUpper(r)
			}
			upperNext = false
			b.WriteRune(r)
		} else {
			upperNext = true
		}
	}
	name := b.String()
	if name == "" || unicode.IsDigit(rune(name[0])) {
		name = "Element" + name
	}
	return name
}

// ToCamelCase - ToPascalCase с маленькой первой буквой, для неэкспортируемых имён:
// "Gateway_1nptppd" -> "gateway1nptppd", "HTTPCheck" -> "httpCheck"
func ToCamelCase(s string) string {
	runes := []rune(ToPascalCase(s))
	for i := 0; i < len(runes) && isASCIIUpper(runes[i]); i++ {
		// в аббревиатуре последняя заглавная - начало следующего слова: HTTPCheck -> httpCheck
		if i > 0 && i+1 < len(runes) && isASCIILower(runes[i+1]) {
			break
		}
		runes[i] = runes[i] - 'A' + 'a'
	}
	return string(runes)
}
