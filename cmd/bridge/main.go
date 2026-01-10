package main

import (
	"log"
	"os"
)

func main() {
	guiEnv := os.Getenv("BRIDGE_GUI")
	bridgeID := os.Getenv("BRIDGE_ID")

	// 1. Explicit GUI request or no environment variables (default to GUI)
	if guiEnv == "true" || (guiEnv == "" && bridgeID == "") {
		startOrDie()
		return
	}

	// 2. Docker CLI mode (when BRIDGE_ID is set and GUI is disabled)
	if bridgeID != "" && guiEnv == "false" {
		runCLI()
		return
	}

	// 3. Invalid configuration
	log.Fatal("Invalid configuration: Use GUI mode or Docker with BRIDGE_ID and BRIDGE_GUI=false")
}

func startOrDie() {
	if !StartGUI() {
		log.Fatal("GUI mode requested but not supported in this build")
	}
}
