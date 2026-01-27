package bridge

import (
	_ "embed"
	"os"
	"runtime"
	"strings"
)

//go:embed VERSION
var embeddedVersion string

var Version string // this is set via ldflags

func GetVersion() string {
	if Version != "" {
		return Version
	}
	if v := strings.TrimSpace(embeddedVersion); v != "" {
		return v
	}
	return "dev"
}

func GetAppType() string {
	if os.Getenv("BRIDGE_GUI") == "false" {
		return "docker"
	}
	if strings.Contains(os.Args[0], ".app/Contents/MacOS/") || runtime.GOOS == "darwin" {
		return "macos"
	}
	return "windows"
}
