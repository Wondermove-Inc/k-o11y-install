package main

import (
	"os"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
