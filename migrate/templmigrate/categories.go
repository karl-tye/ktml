package templmigrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
)

// Categories assigns report-relative file paths to migration categories, in
// migration order, and lists excluded paths with a reason.
type Categories struct {
	// Order lists every category name in migration order.
	Order []string `json:"order"`
	// Files maps a category name to path patterns. A pattern is a
	// path.Match pattern, or a directory prefix ending in "/**".
	Files map[string][]string `json:"files"`
	// Default is the category of a file no pattern matches.
	Default string `json:"default"`
	// Exclude maps a path pattern to the reason the file is excluded.
	Exclude map[string]string `json:"exclude"`
}

// LoadCategories reads a categories JSON file.
func LoadCategories(file string) (*Categories, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var categories Categories
	if err := json.Unmarshal(data, &categories); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	known := make(map[string]bool)
	for _, name := range categories.Order {
		known[name] = true
	}
	for name := range categories.Files {
		if !known[name] {
			return nil, fmt.Errorf("%s: category %q is not listed in order", file, name)
		}
	}
	if categories.Default != "" && !known[categories.Default] {
		return nil, fmt.Errorf("%s: default category %q is not listed in order", file, categories.Default)
	}
	return &categories, nil
}

// Assign returns the category of a file and its exclusion reason, if any.
// The first category in Order with a matching pattern wins.
func (c *Categories) Assign(rel string) (category, excluded string) {
	for pattern, reason := range c.Exclude {
		if matchPattern(pattern, rel) {
			excluded = reason
		}
	}
	for _, name := range c.Order {
		for _, pattern := range c.Files[name] {
			if matchPattern(pattern, rel) {
				return name, excluded
			}
		}
	}
	if c.Default != "" {
		return c.Default, excluded
	}
	return "uncategorized", excluded
}

func matchPattern(pattern, rel string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "/**"); ok {
		return strings.HasPrefix(rel, prefix+"/")
	}
	matched, err := path.Match(pattern, rel)
	return err == nil && matched
}
