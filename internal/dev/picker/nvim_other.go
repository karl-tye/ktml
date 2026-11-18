//go:build !unix

package picker

import "os"

// processAlive cannot tell without unix signals; the server call fails for
// an instance that has exited.
func processAlive(int) bool { return true }

func ownedByMe(os.FileInfo) bool { return true }

func privateDirectory(dir string) bool {
	info, err := os.Lstat(dir)
	return err == nil && info.IsDir()
}
