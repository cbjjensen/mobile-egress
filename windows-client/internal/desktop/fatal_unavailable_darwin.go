//go:build darwin && (!cgo || bindings)

package desktop

import (
	"fmt"
	"os"
)

func showDarwinFatal(class darwinFatalClass) {
	_, _ = fmt.Fprintln(os.Stderr, darwinFatalMessage(class).stderr)
}
