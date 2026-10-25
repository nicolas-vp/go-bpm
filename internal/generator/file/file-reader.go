package file

import (
	"os"
	"path/filepath"
)

func GetFiles(fromPath string) (map[string][]string, error) {
	folders := make(map[string][]string)

	err := filepath.Walk(fromPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Ключ — путь папки относительно корня ("." для файлов в самом корне)
		dir, err := filepath.Rel(fromPath, filepath.Dir(path))
		if err != nil {
			return err
		}
		folders[dir] = append(folders[dir], filepath.Base(path))
		return nil
	})

	return folders, err
}

func ReadFile(fromPath string, subFolder string, file string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(fromPath, subFolder, file))
	return data, err
}
