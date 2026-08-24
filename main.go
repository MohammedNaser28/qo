package main

import (
	"os"
	"path/filepath"

	"github.com/ahmedYasserM/qo/cmd"
	"github.com/ahmedYasserM/qo/pkg/logger"
	"github.com/ahmedYasserM/qo/pkg/sandbox"
)

func main() {
	switch filepath.Base(os.Args[0]) {
	case "qo-check":
		os.Exit(sandbox.RunCheckClient(os.Args[1:]))
	case "qo-setup":
		os.Exit(sandbox.RunSetupClient(os.Args[1:]))
	case "qo-reset":
		os.Exit(sandbox.RunResetClient(os.Args[1:]))
	}

	if len(os.Args) > 1 && os.Args[1] == "init" {
		if len(os.Args) > 2 {
			if err := sandbox.StartSandBox(os.Args[2], 0); err != nil {
				logger.Error(err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}

	if err := cmd.Execute(); err != nil {
		logger.Error(err)
		os.Exit(1)
	}
}
