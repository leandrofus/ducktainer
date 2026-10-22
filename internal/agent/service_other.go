//go:build !windows

package agent

import "errors"

func installWindowsService(Config) error { return errors.New("Windows services only exist on Windows") }
