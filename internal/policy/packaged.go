package policy

import "strings"

// PackagedApp splits a path inside a Microsoft Store (MSIX) package, such as
// C:\Program Files\WindowsApps\Claude_3.6556.2.0_x64__pzs8sxrjxfjjc\app\claude.exe,
// into the package family ("Claude_pzs8sxrjxfjjc") and the path inside the
// package ("app\claude.exe"). The folder name changes with every update; the
// family does not.
func PackagedApp(path string) (family, rel string, ok bool) {
	p := strings.ReplaceAll(path, "/", `\`)
	i := strings.Index(strings.ToLower(p), `\windowsapps\`)
	if i < 0 {
		return "", "", false
	}
	folder, rel, found := strings.Cut(p[i+len(`\windowsapps\`):], `\`)
	if !found || rel == "" {
		return "", "", false
	}
	// Package full name: Name_Version_Architecture_ResourceId_PublisherId.
	// Names can't contain "_", so the split is unambiguous.
	parts := strings.Split(folder, "_")
	if len(parts) != 5 || parts[0] == "" || parts[4] == "" {
		return "", "", false
	}
	return parts[0] + "_" + parts[4], rel, true
}

// key identifies an executable across Store app updates.
func key(path string) string {
	if family, rel, ok := PackagedApp(path); ok {
		return strings.ToLower("pkg:" + family + `\` + rel)
	}
	return strings.ToLower(path)
}
