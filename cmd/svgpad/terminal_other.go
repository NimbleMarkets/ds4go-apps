//go:build windows

package main

import "errors"

func getTerminalCellSize() (int, int, error) {
	return 0, 0, errors.New("not supported on windows")
}
