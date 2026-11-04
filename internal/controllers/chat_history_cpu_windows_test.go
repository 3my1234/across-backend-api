package controllers

import (
	"syscall"
	"unsafe"
)

// Kernel and user CPU of the measured PostgreSQL backend processes only.
// This excludes the generator, background writer, checkpointer and autovacuum.
func historyBackendCPU(pids []uint32) (float64, error) {
	var sum float64
	for _, pid := range pids {
		handle, err := syscall.OpenProcess(0x1000, false, pid)
		if err != nil {
			return 0, err
		}
		var created, exited, kernel, user syscall.Filetime
		ok, _, callErr := syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessTimes").Call(uintptr(handle), uintptr(unsafe.Pointer(&created)), uintptr(unsafe.Pointer(&exited)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
		syscall.CloseHandle(handle)
		if ok == 0 {
			return 0, callErr
		}
		sum += float64(uint64(kernel.HighDateTime)<<32|uint64(kernel.LowDateTime))/1e7 + float64(uint64(user.HighDateTime)<<32|uint64(user.LowDateTime))/1e7
	}
	return sum, nil
}
