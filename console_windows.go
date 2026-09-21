//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// setupConsole stellt die Konsole auf UTF-8 und setzt einen sprechenden Fenstertitel.
func setupConsole() {
	k := syscall.NewLazyDLL("kernel32.dll")
	_, _, _ = k.NewProc("SetConsoleOutputCP").Call(65001)
	if t, err := syscall.UTF16PtrFromString("Gartenabrechnung - dieses Fenster offen lassen"); err == nil {
		_, _, _ = k.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(t)))
	}
}
