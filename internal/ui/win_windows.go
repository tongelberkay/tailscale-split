// Package ui is the tray icon and the few native dialogs TailSplit needs.
package ui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	comdlg32         = windows.NewLazySystemDLL("comdlg32.dll")
	getOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	user32           = windows.NewLazySystemDLL("user32.dll")
	messageBoxW      = user32.NewProc("MessageBoxW")
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	attachConsole    = kernel32.NewProc("AttachConsole")
)

// IsElevated reports whether this process runs as administrator.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RelaunchElevated starts this executable again through the UAC prompt.
func RelaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	params := make([]string, len(args))
	for i, a := range args {
		params[i] = syscall.EscapeArg(a)
	}
	return windows.ShellExecute(0, ptr("runas"), ptr(exe), ptr(strings.Join(params, " ")), ptr(cwd), windows.SW_NORMAL)
}

// Open opens a URL or file with its default handler.
func Open(target string) error {
	return windows.ShellExecute(0, ptr("open"), ptr(target), nil, nil, windows.SW_NORMAL)
}

func ptr(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

const (
	mbOK          = 0x0
	mbIconError   = 0x10
	mbIconInfo    = 0x40
	mbSetForegrnd = 0x10000
)

func Error(title, text string) { messageBox(title, text, mbIconError) }
func Info(title, text string)  { messageBox(title, text, mbIconInfo) }

func messageBox(title, text string, icon uintptr) {
	messageBoxW.Call(0, uintptr(unsafe.Pointer(ptr(text))), uintptr(unsafe.Pointer(ptr(title))),
		mbOK|icon|mbSetForegrnd)
}

// AttachParentConsole lets CLI subcommands print when the exe was built as a
// GUI program and started from a terminal.
func AttachParentConsole() {
	const attachParentProcess = ^uintptr(0)
	if r, _, _ := attachConsole.Call(attachParentProcess); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}

// SingleInstance returns false if another TailSplit already runs.
func SingleInstance() (bool, func()) {
	h, err := windows.CreateMutex(nil, false, ptr(`Local\TailSplit-7a115b17`))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(h)
		return false, func() {}
	}
	if err != nil {
		return true, func() {} // can't tell; don't block the user
	}
	return true, func() { windows.CloseHandle(h) }
}

type openFileName struct {
	structSize    uint32
	owner         uintptr
	instance      uintptr
	filter        *uint16
	customFilter  *uint16
	maxCustFilter uint32
	filterIndex   uint32
	file          *uint16
	maxFile       uint32
	fileTitle     *uint16
	maxFileTitle  uint32
	initialDir    *uint16
	title         *uint16
	flags         uint32
	fileOffset    uint16
	fileExtension uint16
	defExt        *uint16
	custData      uintptr
	hook          uintptr
	templateName  *uint16
	reserved      uintptr
	reservedDword uint32
	flagsEx       uint32
}

const (
	ofnFileMustExist = 0x1000
	ofnPathMustExist = 0x800
	ofnNoChangeDir   = 0x8
	ofnExplorer      = 0x80000
	ofnDontAddRecent = 0x2000000
	ofnAllowMulti    = 0x200
)

// PickExecutables shows the standard Open dialog for .exe files. It returns
// nil if the user cancelled.
func PickExecutables() []string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	buf := make([]uint16, 32<<10)
	filter, _ := windows.UTF16FromString("Programs (*.exe)\x00*.exe\x00")
	filter = append(filter, 0)
	dir := ptr(os.Getenv("ProgramFiles"))
	ofn := openFileName{
		filter:     &filter[0],
		file:       &buf[0],
		maxFile:    uint32(len(buf)),
		initialDir: dir,
		title:      ptr("Choose apps that should use the exit node"),
		flags:      ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir | ofnExplorer | ofnDontAddRecent | ofnAllowMulti,
	}
	ofn.structSize = uint32(unsafe.Sizeof(ofn))
	if r, _, _ := getOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn))); r == 0 {
		return nil
	}
	// Multi-select returns "dir\0file1\0file2\0\0"; single returns "path\0\0".
	var parts []string
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i == start {
			break
		}
		parts = append(parts, windows.UTF16ToString(buf[start:i]))
		start = i + 1
	}
	if len(parts) <= 1 {
		return parts
	}
	out := make([]string, 0, len(parts)-1)
	for _, f := range parts[1:] {
		out = append(out, filepath.Join(parts[0], f))
	}
	return out
}
