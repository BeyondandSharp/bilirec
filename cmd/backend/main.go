package main

import (
	"fmt"
	"os"

	root "github.com/bilirec/bilirec"
	"github.com/bilirec/bilirec/internal/bootstrap"
	"github.com/bilirec/bilirec/internal/modules/configfile"
	"github.com/bilirec/bilirec/internal/services/restart"
	"github.com/bilirec/bilirec/pkg/logger"
)

func main() {
	// The unified config center must run before logger.Init so that config.json
	// and the .env chain can seed logging/tuning keys (docs/CONFIG-V2.md §1).
	store, err := configfile.LoadAndApply()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configfile: failed to load configuration: %v\n", err)
	}

	logger.Init(logger.Options{})
	defer logger.Sync()

	root.InitDotEnv()
	root.InitSwaggerDocs()

	logger.L().Info("Starting bilirec backend...")
	restart.Enable()
	app := bootstrap.NewApp(store)
	restart.Watch(nil)
	app.Run()
	if restart.Requested() {
		logger.Sync()
		if err := restart.Exec(); err != nil {
			logger.L().Errorf("进程重启失败，退回退出进程：%v", err)
			os.Exit(0)
		}
	}
}
