package main

import (
	"log"
	"os"
)

func main() {
	guiEnv := os.Getenv("BRIDGE_GUI")
	bridgeID := os.Getenv("BRIDGE_ID")

	// 1. Explicit GUI request
	if guiEnv == "true" {
		startOrDie()
		return
	}

	// 2. Explicit CLI request
	if bridgeID != "" {
		runCLI()
		return
	}

	// 3. Default: Try GUI first, then fallback to CLI
	if !StartGUI() {
		runCLI()
	}
}

func startOrDie() {
	if !StartGUI() {
		log.Fatal("GUI mode requested but not supported in this build")
	}
}
