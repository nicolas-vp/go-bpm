package utils

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// GoImportPath возвращает import path пакета в папке dir по ближайшему go.mod выше неё
func GoImportPath(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	modDir, err := ModuleRoot(absDir)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(filepath.Join(modDir, "go.mod"))
	if err != nil {
		return "", err
	}
	modulePath := modfile.ModulePath(content)
	if modulePath == "" {
		return "", fmt.Errorf("no module path in %s", filepath.Join(modDir, "go.mod"))
	}
	rel, err := filepath.Rel(modDir, absDir)
	if err != nil {
		return "", err
	}
	return path.Join(modulePath, filepath.ToSlash(rel)), nil
}

// ModuleRoot возвращает абсолютный путь папки с ближайшим go.mod, начиная с dir и выше
func ModuleRoot(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for modDir := absDir; ; modDir = filepath.Dir(modDir) {
		_, err := os.Stat(filepath.Join(modDir, "go.mod"))
		if err == nil {
			return modDir, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(modDir) == modDir {
			return "", fmt.Errorf("go.mod not found for %s", absDir)
		}
	}
}
