package bridge

import (
	"os"
	"runtime"
	"strings"
)

var Version string // set via ldflags in release builds

func GetVersion() string {
	if v := strings.TrimSpace(Version); v != "" {
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
