//go:build gui

package main

import "bridge/pkg/ui"

func StartGUI() bool {
	ui.NewApp().Run()
	return true
}
