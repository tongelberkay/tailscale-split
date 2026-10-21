package proc

import (
	"path/filepath"
	"sort"
	"strings"
)

// Filter keeps the paths matching every whitespace-separated term of query,
// compared case-insensitively against the file name and the full path.
// Results are sorted by file name, then path.
func Filter(paths []string, query string) []string {
	terms := strings.Fields(strings.ToLower(query))
	var out []string
	for _, p := range paths {
		hay := strings.ToLower(p)
		ok := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(base(out[i])), strings.ToLower(base(out[j]))
		if a != b {
			return a < b
		}
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// base handles Windows separators on any OS, so tests behave the same.
func base(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return filepath.Base(p)
}

// Name is the executable's file name.
func Name(p string) string { return base(p) }
