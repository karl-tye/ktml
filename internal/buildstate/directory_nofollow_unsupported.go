//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package buildstate

import (
	"fmt"
	"os"
	"runtime"
)

func openDirectoryNoFollow(_ *os.File, _ string) (*os.File, error) {
	return nil, fmt.Errorf("secure no-follow directory acquisition is unsupported on %s", runtime.GOOS)
}
