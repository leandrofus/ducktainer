//go:build !windows

package agent

import "os"

// swapBinary puts the new binary in place of the running one.
func swapBinary(tmp, exe string) error { return os.Rename(tmp, exe) }

// cleanOldBinary removes what a Windows update leaves behind (nothing here).
func cleanOldBinary() {}
