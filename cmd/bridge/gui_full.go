//go:build gui

package main

import (
	"bridge/pkg/ui"
	"log"
)

func StartGUI() bool {
	log.Println("Starting ScoreScrape Bridge in GUI mode...")
	app := ui.NewApp()
	app.Run()
	return true
}
