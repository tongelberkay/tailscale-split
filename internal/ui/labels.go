package ui

import (
	"path/filepath"
	"strings"
)

// Labels names each app by its file name, adding as many parent folders as
// it takes to tell apart apps that share a name.
func Labels(paths []string) []string {
	split := make([][]string, len(paths))
	for i, p := range paths {
		split[i] = strings.FieldsFunc(p, func(r rune) bool { return r == '\\' || r == '/' })
	}
	label := func(i, depth int) string {
		parts := split[i]
		if depth > len(parts) {
			depth = len(parts)
		}
		s := filepath.Join(parts[len(parts)-depth:]...)
		if depth > 1 && depth < len(parts) {
			s = `...\` + s
		}
		return strings.ReplaceAll(s, "/", `\`)
	}
	out := make([]string, len(paths))
	for i := range paths {
		for depth := 1; ; depth++ {
			out[i] = label(i, depth)
			if depth >= len(split[i]) {
				break
			}
			clash := false
			for j := range paths {
				if j != i && strings.EqualFold(label(j, depth), out[i]) {
					clash = true
					break
				}
			}
			if !clash {
				break
			}
		}
	}
	return out
}
