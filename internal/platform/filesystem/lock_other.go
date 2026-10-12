//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package filesystem

import "os"

// No file locks here: prompts are not serialized.
func tryLock(*os.File) bool { return true }

func unlockFile(*os.File) {}
