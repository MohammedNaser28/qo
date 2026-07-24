package main

import (
	"os"

	"github.com/ahmedYasserM/qo/cmd"
	"github.com/ahmedYasserM/qo/pkg/logger"
	"github.com/ahmedYasserM/qo/pkg/sandbox"
)

func main() {

	if len(os.Args) > 0 && os.Args[0] == "init" {
		if len(os.Args) > 1 {
			if err := sandbox.StartSandBox(os.Args[1], 0); err != nil {
				logger.Error(err)
				os.Exit(1)
			} else {
				os.Exit(0)
			}
		}
	}

	if err := cmd.Execute(); err != nil {
		logger.Error(err)
		os.Exit(1)
	}

}
