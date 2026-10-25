package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckBuild собирает сгенерированный код в модуле движка. Так при генерации видны ошибки,
// которые иначе всплывут только в движке: опечатки в скриптах и условиях процесса,
// рукописные методы со старой сигнатурой, расхождение с контрактом bpm
func CheckBuild(outputPath string) error {
	cmd := exec.Command(goBinary(), "build", "./...")
	cmd.Dir = outputPath
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("сгенерированный код в %s не собирается: %w\n%s", outputPath, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// goBinary - go из GOROOT, если он задан. Иначе первый go из PATH может оказаться другой версии,
// чем GOROOT (например, IDE задаёт GOROOT своего SDK, а в PATH - системный go),
// и сборка падает с "compile: version ... does not match go tool version ..."
func goBinary() string {
	if goroot := os.Getenv("GOROOT"); goroot != "" {
		path := filepath.Join(goroot, "bin", "go")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "go"
}
