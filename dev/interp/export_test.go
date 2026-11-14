package interp

import "path/filepath"

// Reuses reports whether p shares the compiled components of file with old,
// which Swap does for every Document that did not change.
func (p *Program) Reuses(old *Program, file string) bool {
	path, err := filepath.Abs(file)
	if err != nil {
		return false
	}
	current, previous := p.files[path], old.files[path]
	if len(current) == 0 || len(current) != len(previous) {
		return false
	}
	for index := range current {
		if current[index] != previous[index] {
			return false
		}
	}
	return true
}
