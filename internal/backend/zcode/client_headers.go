package zcode

import (
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// zcodeOSVersion mirrors Node's os.release(), which the open-source client
// sends as X-Os-Version. It is detected once so all requests from one proxy
// process present the same host identity.
var zcodeOSVersion = detectZCodeOSVersion()

func detectZCodeOSVersion() string {
	if runtime.GOOS == "windows" {
		return runtime.GOOS
	}
	output, err := exec.Command("uname", "-r").Output()
	if err == nil {
		if version := strings.TrimSpace(string(output)); version != "" {
			return version
		}
	}
	return runtime.GOOS
}

func zcodeOSCategory() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

func zcodeClientTimezone() string {
	name := time.Now().Location().String()
	if name == "" || name == "Local" {
		return "UTC"
	}
	return name
}
