package codegen

import (
	"fmt"
	"os"
	"path/filepath"
)

// removeStaleProcessDirs удаляет сгенерированные файлы из пакетов процессов, которых больше нет в BPMN.
// Пустая папка удаляется; если в ней остались рукописные файлы, она остаётся, а в stderr пишется предупреждение
func removeStaleProcessDirs(outputPath string, processDirs map[string]bool) error {
	entries, err := os.ReadDir(outputPath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || processDirs[entry.Name()] {
			continue
		}
		dir := filepath.Join(outputPath, entry.Name())
		if err := removeGeneratedFiles(dir); err != nil {
			return err
		}
		left, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		if len(left) == 0 {
			if err := os.Remove(dir); err != nil {
				return err
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "go-bpm-gen: процесса для %s больше нет, но в папке остались рукописные файлы — удалите её вручную\n", dir)
	}
	return nil
}
