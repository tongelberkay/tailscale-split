package proc

import (
	"reflect"
	"testing"
)

func TestFilter(t *testing.T) {
	all := []string{
		`C:\Program Files\WindowsApps\OpenAI.ChatGPT\app\ChatGPT.exe`,
		`C:\Users\b\.codex\bin\codex.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Windows\explorer.exe`,
	}
	cases := map[string][]string{
		"":             {all[0], all[1], all[3], all[2]}, // sorted by file name: chatgpt, codex, explorer, msedge
		"CODEX":        {all[1]},
		"openai":       {all[0]},         // matches the path, not the name
		"program exe":  {all[0], all[2]}, // every term must match
		"windows edge": nil,
	}
	for q, want := range cases {
		if got := Filter(all, q); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q, want %q", q, got, want)
		}
	}
}
