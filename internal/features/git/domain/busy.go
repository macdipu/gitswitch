package domain

import "fmt"

// SmartSwitch is the operation a repo is in the middle of while a smart
// switch has clashing files to fix (see refs.Clash).
const SmartSwitch = "smart switch"

// Busy explains why a repo can't be changed while an operation is in
// progress, e.g. "rebase".
func Busy(op string) error {
	if op == SmartSwitch {
		return fmt.Errorf("a smart switch left files with conflicts in this repo; fix them and run `gitswitch resolve --done`, or go back with `gitswitch resolve --abort` (the apps have buttons for both)")
	}
	return fmt.Errorf("git is in the middle of a %s in this repo; finish or abort it first", op)
}
