package config

import (
	"reflect"
	"testing"
)

func TestNormalizeApps(t *testing.T) {
	got, err := NormalizeApps([]string{` "C:\Program Files\App\app.EXE" `, `c:/program files/app/APP.exe`, `\\server\share\tool.exe`})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`C:\Program Files\App\app.EXE`, `\\server\share\tool.exe`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, bad := range []string{`app.exe`, `C:\tools\app.exe.txt`, `C:\tools`, `C:app.exe`, ``} {
		if _, err := NormalizeApps([]string{bad}); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestValidateExitNode(t *testing.T) {
	for in, want := range map[string]string{
		"100.101.102.103":           "100.101.102.103",
		"fd7a:115c:a1e0::1":         "fd7a:115c:a1e0::1",
		" home-server ":             "home-server",
		"home-server.tail1.ts.net.": "home-server.tail1.ts.net",
	} {
		got, err := ValidateExitNode(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "https://x", "host:443", "a b"} {
		if _, err := ValidateExitNode(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	s, _ := Settings{ExitNode: "exit", Apps: []string{`C:\a\b.exe`}}.Normalized()
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if s.Mode != ModeProxy || s.ProxyPort != DefaultProxyPort {
		t.Fatalf("defaults: %+v", s)
	}
	if _, err := (Settings{Mode: "vpn"}).Normalized(); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if empty, err := Load(t.TempDir()); err != nil || len(empty.Apps) != 0 {
		t.Fatalf("missing file: %+v, %v", empty, err)
	}
}
