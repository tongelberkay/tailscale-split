// Package policy decides which processes may use the exit node proxy.
package policy

// Policy is immutable; build a new one when the app list changes.
type Policy struct {
	apps map[string]bool // lower-cased executable paths
}

func New(apps []string) *Policy {
	p := &Policy{apps: make(map[string]bool, len(apps))}
	for _, a := range apps {
		p.apps[key(a)] = true
	}
	return p
}

// Selected reports whether the process or one of its ancestors is selected.
// owner is the process's executable path followed by its ancestors' paths;
// an unidentified owner (nil) is not selected.
func (p *Policy) Selected(owner []string) bool {
	for _, path := range owner {
		if p.apps[key(path)] {
			return true
		}
	}
	return false
}
