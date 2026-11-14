package tailwind

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DynamicCode is the diagnostic code of class construction that cannot be
// discovered statically.
const DynamicCode = "tailwind-dynamic-class"

// Message explains a dynamic class construction. text is its source.
func (d Dynamic) Message(text string) string {
	if d.Partial {
		return "class " + text + " is built from {" + d.Path + "}, so Tailwind cannot find it; write whole class names, for example in the presenter, or list them with @source inline(...)"
	}
	return "classes from {" + d.Path + "} are found by Tailwind only where they are written whole, such as string literals in Go, or listed with @source inline(...)"
}

// File is the extraction of one KTML source file.
type File struct {
	Path       string
	Source     []byte
	Extraction Extraction
}

// SkipDir reports whether a directory below a walk root is skipped. Like
// the go command and the compiler's workspace index, KTML skips hidden,
// vendor, testdata, and node_modules directories.
func SkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules"
}

// ExtractFiles extracts the classes of every .ktml file under roots, in
// lexical path order. A root may be a file.
func ExtractFiles(roots []string) ([]File, error) {
	var files []File
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path != root && SkipDir(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".ktml") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files = append(files, File{Path: path, Source: source, Extraction: Extract(source)})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}
