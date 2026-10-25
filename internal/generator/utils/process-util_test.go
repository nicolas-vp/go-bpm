package utils

import "testing"

func TestToCamelCase(t *testing.T) {
	tests := map[string]string{
		"Gateway_1nptppd": "gateway1nptppd",
		"MySimpleMethod":  "mySimpleMethod",
		"HTTPCheck":       "httpCheck",
		"ID":              "id",
		"1abc":            "element1abc",
	}
	for in, want := range tests {
		if got := ToCamelCase(in); got != want {
			t.Errorf("ToCamelCase(%q) = %q, want %q", in, got, want)
		}
	}
}
