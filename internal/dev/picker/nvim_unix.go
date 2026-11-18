//go:build unix

package picker

import (
	"os"
	"syscall"
)

// processAlive reports whether a process of this user runs with pid.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

// ownedByMe reports whether this user owns a file.
func ownedByMe(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}

// privateDirectory reports whether dir is a directory that this user owns
// and nobody else can write.
func privateDirectory(dir string) bool {
	info, err := os.Lstat(dir)
	return err == nil && info.IsDir() && info.Mode().Perm()&0o022 == 0 && ownedByMe(info)
}
