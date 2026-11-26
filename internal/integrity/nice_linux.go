package integrity

import (
	"runtime"
	"syscall"
)

// lowPriority makes this thread the least important on the box, the goroutine has to end with it
func lowPriority() {
	runtime.LockOSThread()
	syscall.Setpriority(syscall.PRIO_PROCESS, syscall.Gettid(), 19)
}
