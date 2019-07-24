package version

import (
	"fmt"
	"io"
	"runtime"

	sdkVersion "github.com/operator-framework/operator-sdk/version"

	"github.com/go-logr/logr"
)

var (
	// Version binary version
	Version = "0.0.1"
	// BuildTime binary build time
	BuildTime = ""
	// Commit current git commit
	Commit = ""
	// Tag possible git Tag on current commit
	Tag = ""
)

// PrintVersionWriter print versions information in to writer interface
func PrintVersionWriter(writer io.Writer) {
	fmt.Fprintf(writer, "Version:\n")
	for _, val := range printVersionSlice() {
		fmt.Fprintf(writer, "- %s\n", val)
	}
}

// PrintVersionLogs print versions information in logs
func PrintVersionLogs(logger logr.Logger) {
	for _, val := range printVersionSlice() {
		logger.Info(val)
	}
}

func printVersionSlice() []string {
	output := []string{
		fmt.Sprintf("Version: %v", Version),
		fmt.Sprintf("Build time: %v", BuildTime),
		fmt.Sprintf("Git tag: %v", Tag),
		fmt.Sprintf("Git Commit: %v", Commit),
		fmt.Sprintf("Go Version: %s", runtime.Version()),
		fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH),
		fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version),
	}
	return output
}
