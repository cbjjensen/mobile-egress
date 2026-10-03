//go:build darwin && cgo && !bindings

package securestore

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include "system_keychain_darwin.h"
*/
import "C"

import (
	"sync"
	"unsafe"
)

var systemKeychainMu sync.Mutex

type systemKeychainNative struct{}

func newPlatformSystemKeychainNative() (keychainNative, error) {
	systemKeychainMu.Lock()
	defer systemKeychainMu.Unlock()
	status := keychainStatus(C.me_system_keychain_check())
	if status != keychainStatusSuccess {
		return nil, keychainOperationError("open signed daemon System Keychain", status)
	}
	return systemKeychainNative{}, nil
}

func (systemKeychainNative) operation(operation int, service, account string, value []byte) ([]byte, keychainStatus) {
	systemKeychainMu.Lock()
	defer systemKeychainMu.Unlock()
	cs, ca := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	input := C.CBytes(value)
	defer C.free(input)
	var output unsafe.Pointer
	var length C.uint32_t
	status := keychainStatus(C.me_system_keychain_operation(C.int(operation), cs, ca, input, C.size_t(len(value)), &output, &length))
	if output != nil {
		defer C.free(output)
	}
	if status != keychainStatusSuccess {
		return nil, status
	}
	if length == 0 {
		return nil, status
	}
	return C.GoBytes(output, C.int(length)), status
}
func (n systemKeychainNative) Add(_, service, account string, value []byte) keychainStatus {
	_, s := n.operation(0, service, account, value)
	return s
}
func (n systemKeychainNative) Update(_, service, account string, value []byte) keychainStatus {
	_, s := n.operation(1, service, account, value)
	return s
}
func (n systemKeychainNative) Get(_, service, account string) ([]byte, keychainStatus) {
	return n.operation(2, service, account, nil)
}
func (n systemKeychainNative) Delete(_, service, account string) keychainStatus {
	_, s := n.operation(3, service, account, nil)
	return s
}
