//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package buildstate

import (
	"os"

	"golang.org/x/sys/unix"
)

func openDirectoryNoFollow(anchor *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(anchor.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
