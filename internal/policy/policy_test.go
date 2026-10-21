package policy

import "testing"

func TestSelected(t *testing.T) {
	p := New([]string{`C:\Apps\Browser.exe`})
	sel, other := `c:\apps\browser.EXE`, `C:\Other\x.exe`
	cases := []struct {
		exe  []string
		want bool
	}{
		{[]string{sel}, true},
		{[]string{other}, false},
		{nil, false}, // unidentified process
		{[]string{`C:\WebView\msedgewebview2.exe`, sel}, true}, // child of a selected app
		{[]string{sel, `C:\Windows\explorer.exe`}, true},
		{[]string{other, `C:\Windows\explorer.exe`}, false},
	}
	for _, c := range cases {
		if got := p.Selected(c.exe); got != c.want {
			t.Errorf("Selected(%q) = %v, want %v", c.exe, got, c.want)
		}
	}
}
