package root

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/bilirec/bilirec/pkg/logger"
)

var log = logger.Named("root")

//go:embed .env
var dotEnvFile string

// InitDotEnv generates the embedded .env template next to the executable for
// native installs when it is missing. Loading .env / .env.local happens in
// internal/modules/configfile before logger.Init (docs/CONFIG-V2.md §1);
// container images never auto-write .env.
func InitDotEnv() {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		log.Debug("在 Docker 中运行，跳过 .env 模板生成")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		log.Warnf("无法确定可执行文件路径：%v", err)
		exe = "."
	}
	dotEnvPath := filepath.Join(filepath.Dir(exe), ".env")
	if _, err := os.Stat(dotEnvPath); !os.IsNotExist(err) {
		return
	}
	if err := os.WriteFile(dotEnvPath, []byte(dotEnvFile), 0o644); err != nil {
		log.Warnf("写入 .env 文件失败：%v，如果你使用的是二进制版本，请手动创建 .env 文件", err)
		return
	}
	log.Info("已使用默认值生成 .env 文件；修改 .env 后请重启")
}
