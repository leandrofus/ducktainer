//go:build windows

package agent

import "errors"

var execSelf = func(string) error { return errors.New("self-update is not supported on Windows") }
