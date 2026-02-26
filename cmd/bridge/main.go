package main

import (
	"log"
	"os"
)

func main() {
	gui := os.Getenv("BRIDGE_GUI")
	id := os.Getenv("BRIDGE_ID")

	if gui == "true" || (gui == "" && id == "") {
		if !StartGUI() {
			log.Fatal("GUI not supported in this build")
		}
		return
	}

	if id != "" && gui == "false" {
		runCLI()
		return
	}

	log.Fatal("Invalid config: set BRIDGE_ID and BRIDGE_GUI=false for CLI mode")
}
