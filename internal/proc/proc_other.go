//go:build !windows

package proc

// Lookup is only implemented on Windows; elsewhere every flow is unidentified
// and therefore goes direct.
func Lookup(Flow) string { return "" }
