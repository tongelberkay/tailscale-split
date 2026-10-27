package policy

import "testing"

func TestPackagedApp(t *testing.T) {
	cases := []struct {
		path, family, rel string
		ok                bool
	}{
		{`C:\Program Files\WindowsApps\Claude_3.6556.2.0_x64__pzs8sxrjxfjjc\app\claude.exe`, "Claude_pzs8sxrjxfjjc", `app\claude.exe`, true},
		{`C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.21.3231.0_x64__8wekyb3d8bbwe\wt.exe`, "Microsoft.WindowsTerminal_8wekyb3d8bbwe", `wt.exe`, true},
		{`C:\Program Files\WindowsApps\Claude_3.6556.2.0_x64__pzs8sxrjxfjjc`, "", "", false},
		{`C:\Program Files\WindowsApps\NotAPackage\x.exe`, "", "", false},
		{`C:\Apps\Browser.exe`, "", "", false},
	}
	for _, c := range cases {
		f, r, ok := PackagedApp(c.path)
		if f != c.family || r != c.rel || ok != c.ok {
			t.Errorf("PackagedApp(%q) = %q, %q, %v", c.path, f, r, ok)
		}
	}
}

func TestSelectedAcrossStoreUpdate(t *testing.T) {
	p := New([]string{`C:\Program Files\WindowsApps\Claude_3.1623.0.0_x64__pzs8sxrjxfjjc\app\Claude.exe`})
	if !p.Selected([]string{`C:\Program Files\WindowsApps\Claude_3.6556.2.0_x64__pzs8sxrjxfjjc\app\claude.exe`}) {
		t.Error("updated Store app not matched")
	}
	if p.Selected([]string{`C:\Program Files\WindowsApps\Other_1.0.0.0_x64__pzs8sxrjxfjjc\app\Claude.exe`}) {
		t.Error("different package matched")
	}
	if p.Selected([]string{`C:\Program Files\WindowsApps\Claude_3.6556.2.0_x64__pzs8sxrjxfjjc\app\helper.exe`}) {
		t.Error("different exe in the same package matched")
	}
}
