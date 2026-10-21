package logfile

import "testing"

func TestRedact(t *testing.T) {
	in := "visit https://login.tailscale.com/a/abc123 key nodekey:deadbeef and tskey-auth-XYZ"
	want := "visit [sign-in link hidden] key [key hidden] and [key hidden]"
	if got := Redact(in); got != want {
		t.Fatalf("got %q", got)
	}
}
