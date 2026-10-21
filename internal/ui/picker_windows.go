package ui

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"tailsplit/internal/proc"
)

// A small native window: a search box over a multi-select list of running
// executables. Search matches every typed word against name and full path.

var (
	gdi32               = windows.NewLazySystemDLL("gdi32.dll")
	getStockObject      = gdi32.NewProc("GetStockObject")
	registerClassExW    = user32.NewProc("RegisterClassExW")
	createWindowExW     = user32.NewProc("CreateWindowExW")
	defWindowProcW      = user32.NewProc("DefWindowProcW")
	destroyWindow       = user32.NewProc("DestroyWindow")
	getMessageW         = user32.NewProc("GetMessageW")
	translateMessage    = user32.NewProc("TranslateMessage")
	dispatchMessageW    = user32.NewProc("DispatchMessageW")
	isDialogMessageW    = user32.NewProc("IsDialogMessageW")
	postQuitMessage     = user32.NewProc("PostQuitMessage")
	sendMessageW        = user32.NewProc("SendMessageW")
	moveWindow          = user32.NewProc("MoveWindow")
	showWindow          = user32.NewProc("ShowWindow")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	setFocus            = user32.NewProc("SetFocus")
	getClientRect       = user32.NewProc("GetClientRect")
	loadCursorW         = user32.NewProc("LoadCursorW")
	getModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	getDpiForWindow     = user32.NewProc("GetDpiForWindow")
	getWindowTextLenW   = user32.NewProc("GetWindowTextLengthW")
	getWindowTextW      = user32.NewProc("GetWindowTextW")
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	wsChild            = 0x40000000
	wsTabStop          = 0x00010000
	wsVScroll          = 0x00200000
	wsBorder           = 0x00800000
	wsExClientEdge     = 0x00000200
	esAutoHScroll      = 0x0080
	lbsNotify          = 0x0001
	lbsExtendedSel     = 0x0800
	lbsNoIntegral      = 0x0100
	bsDefPushButton    = 0x0001

	wmDestroy    = 0x0002
	wmSize       = 0x0005
	wmClose      = 0x0010
	wmSetFont    = 0x0030
	wmCommand    = 0x0111
	enChange     = 0x0300
	lbnDblClk    = 2
	lbAddString  = 0x0180
	lbReset      = 0x0184
	lbGetSelCnt  = 0x0190
	lbGetSelItms = 0x0191
	lbSetSel     = 0x0185
	cwUseDefault = 0x80000000
	swShow       = 5
	idcArrow     = 32512
	defaultFont  = 17
	colorWindow  = 5

	idSearch = 101
	idList   = 102
	idAdd    = 1 // IDOK, so Enter adds
	idCancel = 2 // IDCANCEL, so Esc closes the window
)

type wndClassEx struct {
	size, style                uint32
	wndProc                    uintptr
	clsExtra, wndExtra         int32
	instance, icon, cursor, bg uintptr
	menuName, className        *uint16
	iconSm                     uintptr
}

type msg struct {
	hwnd, message, wParam, lParam uintptr
	time                          uint32
	pt                            [2]int32
	private                       uint32
}

type picker struct {
	hwnd, search, list, add, cancel uintptr
	all, shown                      []string
	result                          []string
}

var (
	pickerMu     sync.Mutex // one picker at a time
	pickerActive *picker
	classOnce    sync.Once
	wndProcCB    = syscall.NewCallback(pickerProc)
)

func call(p *windows.LazyProc, a ...uintptr) uintptr {
	r, _, _ := p.Call(a...)
	return r
}

// PickRunning shows running executables and returns the ones the user chose
// (nil if cancelled). exclude lists paths already selected.
func PickRunning(exclude []string) []string {
	if !pickerMu.TryLock() {
		return nil
	}
	defer pickerMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	skip := map[string]bool{}
	for _, e := range exclude {
		skip[lower(e)] = true
	}
	var all []string
	for _, p := range proc.Running() {
		if !skip[lower(p)] {
			all = append(all, p)
		}
	}
	p := &picker{all: all}
	pickerActive = p

	inst := call(getModuleHandleW, 0)
	cls := ptr("TailSplitPicker")
	classOnce.Do(func() {
		wc := wndClassEx{wndProc: wndProcCB, instance: inst, className: cls,
			cursor: call(loadCursorW, 0, idcArrow), bg: colorWindow + 1}
		wc.size = uint32(unsafe.Sizeof(wc))
		call(registerClassExW, uintptr(unsafe.Pointer(&wc)))
	})
	p.hwnd = call(createWindowExW, 0, uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(ptr("Add running apps to TailSplit"))), wsOverlappedWindow,
		cwUseDefault, cwUseDefault, 760, 520, 0, 0, inst, 0)
	if p.hwnd == 0 {
		return nil
	}
	child := func(class, text string, style, ex uintptr, id int) uintptr {
		h := call(createWindowExW, ex, uintptr(unsafe.Pointer(ptr(class))), uintptr(unsafe.Pointer(ptr(text))),
			wsChild|wsVisible|style, 0, 0, 10, 10, p.hwnd, uintptr(id), inst, 0)
		call(sendMessageW, h, wmSetFont, call(getStockObject, defaultFont), 1)
		return h
	}
	p.search = child("EDIT", "", wsTabStop|esAutoHScroll|wsBorder, wsExClientEdge, idSearch)
	p.list = child("LISTBOX", "", wsTabStop|wsVScroll|lbsNotify|lbsExtendedSel|lbsNoIntegral, wsExClientEdge, idList)
	p.add = child("BUTTON", "Add selected", wsTabStop|bsDefPushButton, 0, idAdd)
	p.cancel = child("BUTTON", "Cancel", wsTabStop, 0, idCancel)
	p.refill()
	p.layout()
	call(showWindow, p.hwnd, swShow)
	call(setForegroundWindow, p.hwnd)
	call(setFocus, p.search)

	var m msg
	for {
		r := call(getMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			break
		}
		if call(isDialogMessageW, p.hwnd, uintptr(unsafe.Pointer(&m))) != 0 {
			continue // Tab, Enter and Esc handling
		}
		call(translateMessage, uintptr(unsafe.Pointer(&m)))
		call(dispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}
	pickerActive = nil
	return p.result
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func (p *picker) refill() {
	n := call(getWindowTextLenW, p.search)
	buf := make([]uint16, n+1)
	call(getWindowTextW, p.search, uintptr(unsafe.Pointer(&buf[0])), n+1)
	p.shown = proc.Filter(p.all, windows.UTF16ToString(buf))
	call(sendMessageW, p.list, lbReset, 0, 0)
	for _, s := range p.shown {
		line := proc.Name(s) + "      " + s
		call(sendMessageW, p.list, lbAddString, 0, uintptr(unsafe.Pointer(ptr(line))))
	}
	if len(p.shown) == 1 {
		call(sendMessageW, p.list, lbSetSel, 1, 0)
	}
}

func (p *picker) selected() []string {
	n := int(call(sendMessageW, p.list, lbGetSelCnt, 0, 0))
	if n <= 0 {
		return nil
	}
	idx := make([]int32, n)
	call(sendMessageW, p.list, lbGetSelItms, uintptr(n), uintptr(unsafe.Pointer(&idx[0])))
	var out []string
	for _, i := range idx {
		if int(i) < len(p.shown) {
			out = append(out, p.shown[i])
		}
	}
	return out
}

func (p *picker) layout() {
	var rc [4]int32
	call(getClientRect, p.hwnd, uintptr(unsafe.Pointer(&rc)))
	scale := func(v int32) int32 {
		dpi := int32(call(getDpiForWindow, p.hwnd))
		if dpi == 0 {
			dpi = 96
		}
		return v * dpi / 96
	}
	w, h := rc[2], rc[3]
	m, eh, bh, bw := scale(10), scale(24), scale(28), scale(110)
	mv := func(hwnd uintptr, x, y, cx, cy int32) {
		call(moveWindow, hwnd, uintptr(x), uintptr(y), uintptr(cx), uintptr(cy), 1)
	}
	mv(p.search, m, m, w-2*m, eh)
	mv(p.list, m, 2*m+eh, w-2*m, h-4*m-eh-bh)
	mv(p.cancel, w-m-bw, h-m-bh, bw, bh)
	mv(p.add, w-2*m-2*bw, h-m-bh, bw, bh)
}

func pickerProc(hwnd, message, wParam, lParam uintptr) uintptr {
	p := pickerActive
	if p == nil || p.hwnd != hwnd {
		return call(defWindowProcW, hwnd, message, wParam, lParam)
	}
	switch message {
	case wmSize:
		p.layout()
		return 0
	case wmCommand:
		id, code := wParam&0xffff, (wParam>>16)&0xffff
		switch {
		case id == idSearch && code == enChange:
			p.refill()
		case id == idAdd, id == idList && code == lbnDblClk:
			if sel := p.selected(); len(sel) > 0 {
				p.result = sel
				call(destroyWindow, hwnd)
			}
		case id == idCancel:
			call(destroyWindow, hwnd)
		}
		return 0
	case wmClose:
		call(destroyWindow, hwnd)
		return 0
	case wmDestroy:
		call(postQuitMessage, 0)
		return 0
	}
	return call(defWindowProcW, hwnd, message, wParam, lParam)
}
