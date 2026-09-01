//go:build darwin && cgo && !bindings

package desktop

/*
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "fatal_darwin.h"
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"
)

func showDarwinFatal(class darwinFatalClass) {
	message := darwinFatalMessage(class)
	heading := C.CString(message.heading)
	body := C.CString(message.body)
	defer C.free(unsafe.Pointer(heading))
	defer C.free(unsafe.Pointer(body))
	C.mobile_egress_show_fatal_alert(heading, body)
	_, _ = fmt.Fprintln(os.Stderr, message.stderr)
}
