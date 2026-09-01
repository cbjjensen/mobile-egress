//go:build darwin && cgo && macintegration && !bindings

package desktop

/*
#cgo CFLAGS: -DMOBILE_EGRESS_MENU_BAR_CONTRACT
#include <stdlib.h>
#include "menu_bar_darwin.h"
*/
import "C"

import "unsafe"

func cocoaMenuBarContractRunApplicationLoop() {
	C.mobile_egress_menu_bar_contract_run_application_loop()
}

func cocoaMenuBarContractStopApplicationLoop() {
	C.mobile_egress_menu_bar_contract_stop_application_loop()
}

func cocoaMenuBarContractInstallSentinelDelegate() {
	C.mobile_egress_menu_bar_contract_install_sentinel_delegate()
}

func cocoaMenuBarContractRemoveSentinelDelegate() {
	C.mobile_egress_menu_bar_contract_remove_sentinel_delegate()
}

func cocoaMenuBarContractResetMainThreadChecks() {
	C.mobile_egress_menu_bar_contract_reset_main_thread_checks()
}

func cocoaMenuBarContractAllOperationsOnMainThread() bool {
	return C.mobile_egress_menu_bar_contract_all_operations_on_main_thread() != 0
}

func cocoaMenuBarContractDelegatePreserved() bool {
	return C.mobile_egress_menu_bar_contract_delegate_preserved() != 0
}

func cocoaMenuBarContractMenuPresent() bool {
	return C.mobile_egress_menu_bar_contract_menu_present() != 0
}

func cocoaMenuBarContractStatus() string {
	value := C.mobile_egress_menu_bar_contract_copy_status()
	if value == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}

func cocoaMenuBarContractClickShow() bool {
	return C.mobile_egress_menu_bar_contract_click_show() != 0
}

func cocoaMenuBarContractClickQuit() bool {
	return C.mobile_egress_menu_bar_contract_click_quit() != 0
}
