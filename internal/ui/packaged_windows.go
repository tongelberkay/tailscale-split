package ui

import (
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"

	"tailsplit/internal/policy"
)

var (
	getPackagesByPackageFamily = kernel32.NewProc("GetPackagesByPackageFamily")
	getPackagePathByFullName   = kernel32.NewProc("GetPackagePathByFullName")
)

// currentPackagePath maps a path inside a Store app's package to the same
// file in the version installed now; Store updates move the package folder.
// Other paths, and packages that can't be found, are returned unchanged.
func currentPackagePath(exe string) string {
	family, rel, ok := policy.PackagedApp(exe)
	if !ok {
		return exe
	}
	if _, err := os.Stat(exe); err == nil {
		return exe
	}
	for _, full := range packagesInFamily(family) {
		if dir := packagePath(full); dir != "" {
			p := filepath.Join(dir, rel)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return exe
}

func packagesInFamily(family string) []string {
	f, err := windows.UTF16PtrFromString(family)
	if err != nil {
		return nil
	}
	var count, size uint32
	r, _, _ := getPackagesByPackageFamily.Call(uintptr(unsafe.Pointer(f)),
		uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&size)), 0)
	if r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) || count == 0 {
		return nil
	}
	names := make([]*uint16, count)
	buf := make([]uint16, size)
	r, _, _ = getPackagesByPackageFamily.Call(uintptr(unsafe.Pointer(f)),
		uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&names[0])),
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return nil
	}
	out := make([]string, 0, count)
	for _, n := range names[:count] {
		out = append(out, windows.UTF16PtrToString(n))
	}
	return out
}

func packagePath(fullName string) string {
	f, err := windows.UTF16PtrFromString(fullName)
	if err != nil {
		return ""
	}
	var size uint32
	getPackagePathByFullName.Call(uintptr(unsafe.Pointer(f)), uintptr(unsafe.Pointer(&size)), 0)
	if size == 0 {
		return ""
	}
	buf := make([]uint16, size)
	if r, _, _ := getPackagePathByFullName.Call(uintptr(unsafe.Pointer(f)),
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0]))); r != 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}
