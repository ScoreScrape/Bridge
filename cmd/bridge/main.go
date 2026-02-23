package main

import (
	"bufio"
	"log"
	"os"
	"strings"
)

func main() {
	loadEnv(".env")

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

func loadEnv(path string) {
	for _, p := range []string{path, "../" + path, "../../" + path} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if i := strings.Index(line, "="); i > 0 {
				key := strings.TrimSpace(line[:i])
				val := strings.TrimSpace(line[i+1:])
				if key != "" && os.Getenv(key) == "" {
					os.Setenv(key, val)
				}
			}
		}
		return
	}
}
