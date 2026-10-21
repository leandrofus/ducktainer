//go:build windows

package agent

import "os"

// swapBinary puts the new binary in place of the running one. Windows will
// not overwrite or delete a running executable, but it does let it be renamed:
// the old file is moved aside (and removed by cleanOldBinary on the next start).
func swapBinary(tmp, exe string) error {
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe) // put the working binary back
		return err
	}
	return nil
}

// cleanOldBinary removes the executable a previous update moved aside.
func cleanOldBinary() {
	if exe, err := selfPath(); err == nil {
		os.Remove(exe + ".old")
	}
}
