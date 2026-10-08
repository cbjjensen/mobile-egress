//go:build darwin && cgo && !bindings

package securestore

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include "user_keychain_darwin.h"
*/
import "C"

import (
	"sync"
	"unsafe"
)

var userKeychainMu sync.Mutex

type userKeychainNative struct{}

func newPlatformUserKeychainNative() (keychainNative, error) {
	userKeychainMu.Lock()
	defer userKeychainMu.Unlock()
	status := keychainStatus(C.me_user_keychain_check())
	if status != keychainStatusSuccess {
		return nil, keychainOperationError("open signed user login Keychain", status)
	}
	return userKeychainNative{}, nil
}

func (userKeychainNative) operation(operation int, service, account string, value []byte) ([]byte, keychainStatus) {
	userKeychainMu.Lock()
	defer userKeychainMu.Unlock()
	cs, ca := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	input := C.CBytes(value)
	defer C.free(input)
	var output unsafe.Pointer
	var length C.uint32_t
	status := keychainStatus(C.me_user_keychain_operation(C.int(operation), cs, ca, input, C.size_t(len(value)), &output, &length))
	if output != nil {
		defer C.free(output)
	}
	if status != keychainStatusSuccess || length == 0 {
		return nil, status
	}
	return C.GoBytes(output, C.int(length)), status
}

func (n userKeychainNative) Add(_, service, account string, value []byte) keychainStatus {
	_, s := n.operation(0, service, account, value)
	return s
}
func (n userKeychainNative) Update(_, service, account string, value []byte) keychainStatus {
	_, s := n.operation(1, service, account, value)
	return s
}
func (n userKeychainNative) Get(_, service, account string) ([]byte, keychainStatus) {
	return n.operation(2, service, account, nil)
}
func (n userKeychainNative) Delete(_, service, account string) keychainStatus {
	_, s := n.operation(3, service, account, nil)
	return s
}
