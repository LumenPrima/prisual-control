package input

import (
	"syscall"
	"unsafe"
)

func rawIoctl(fd uintptr, request uintptr, buf []byte) (uintptr, uintptr, syscall.Errno) {
	return syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(&buf[0])))
}
