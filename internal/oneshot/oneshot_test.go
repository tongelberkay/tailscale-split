package oneshot

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]struct {
		in   []string
		want Args
	}{
		"user's form":  {[]string{`codex.exe`, "--exitnode", "home"}, Args{App: "codex.exe", ExitNode: "home"}},
		"flag first":   {[]string{"--exit-node=home", `C:\A\b.exe`}, Args{App: `C:\A\b.exe`, ExitNode: "home"}},
		"short":        {[]string{"b.exe", "-e", "100.64.0.1"}, Args{App: "b.exe", ExitNode: "100.64.0.1"}},
		"no exit node": {[]string{"b.exe"}, Args{App: "b.exe"}},
		"app args":     {[]string{"b.exe", "-e", "x", "--", "--foo", "bar"}, Args{App: "b.exe", ExitNode: "x", AppArgs: []string{"--foo", "bar"}}},
	}
	for name, c := range cases {
		got, err := Parse(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, %v", name, got, err)
		}
	}
	for _, bad := range [][]string{{"b.exe", "--exitnode"}, {"b.exe", "--bogus"}, {"a.exe", "b.exe"}, {"--exitnode", "x"}} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
	if !LooksLikeApp("Codex.EXE") || !LooksLikeApp(`C:\x\y`) || LooksLikeApp("launch") {
		t.Error("LooksLikeApp")
	}
}
