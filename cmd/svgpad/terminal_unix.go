//go:build !windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func getTerminalCellSize() (int, int, error) {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		f.Fd(),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return 0, 0, errno
	}
	if ws.Col == 0 || ws.Row == 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
		return 0, 0, syscall.EINVAL
	}
	return int(ws.Xpixel / ws.Col), int(ws.Ypixel / ws.Row), nil
}
