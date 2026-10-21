package ui

import (
	"reflect"
	"testing"
)

func TestLabels(t *testing.T) {
	got := Labels([]string{
		`C:\Program Files\WindowsApps\Claude_3.1623.0_x64\app\Claude.exe`,
		`C:\Windows\System32\curl.exe`,
		`C:\Tools\curl.exe`,
		`C:\a\app\x.exe`,
		`C:\b\app\x.exe`,
	})
	want := []string{`Claude.exe`, `...\System32\curl.exe`, `...\Tools\curl.exe`, `...\a\app\x.exe`, `...\b\app\x.exe`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
