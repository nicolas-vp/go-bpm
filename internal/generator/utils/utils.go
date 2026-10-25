package utils

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/tools/imports"
)

func SaveGoFile(path string, content []byte) error {
	formattedContent, err := imports.Process("", content, nil)
	if err != nil {
		_, _ = fmt.Println(string(content))
		return fmt.Errorf("format: %v", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directories: %v", err)
	}

	if err := os.WriteFile(path, formattedContent, 0600); err != nil {
		return fmt.Errorf("write file: %v", err)
	}

	return nil
}
