package main

import (
	"fmt"
	"os"

	root "github.com/bilirec/bilirec"
	"github.com/bilirec/bilirec/internal/bootstrap"
	"github.com/bilirec/bilirec/internal/modules/configfile"
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
	bootstrap.NewApp(store).Run()
}
