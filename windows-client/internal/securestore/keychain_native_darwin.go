//go:build darwin && cgo && !bindings

package securestore

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include "keychain_darwin.h"
*/
import "C"

import "unsafe"

type darwinKeychainNative struct{}

func newPlatformKeychainNative() (keychainNative, error) {
	return darwinKeychainNative{}, nil
}

func (darwinKeychainNative) Add(service, account string, value []byte) keychainStatus {
	serviceValue, accountValue, data := darwinKeychainArguments(service, account, value)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	defer C.free(data)
	return keychainStatus(C.mobile_egress_keychain_add(
		serviceValue,
		accountValue,
		data,
		C.size_t(len(value)),
	))
}

func (darwinKeychainNative) Update(service, account string, value []byte) keychainStatus {
	serviceValue, accountValue, data := darwinKeychainArguments(service, account, value)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	defer C.free(data)
	return keychainStatus(C.mobile_egress_keychain_update(
		serviceValue,
		accountValue,
		data,
		C.size_t(len(value)),
	))
}

func (darwinKeychainNative) Get(service, account string) ([]byte, keychainStatus) {
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))

	var data *C.uchar
	var length C.size_t
	status := keychainStatus(C.mobile_egress_keychain_copy(
		serviceValue,
		accountValue,
		&data,
		&length,
	))
	if data != nil {
		defer C.mobile_egress_keychain_free(unsafe.Pointer(data))
	}
	if status != keychainStatusSuccess {
		return nil, status
	}
	value := unsafe.Slice((*byte)(unsafe.Pointer(data)), int(length))
	return append([]byte(nil), value...), status
}

func (darwinKeychainNative) Delete(service, account string) keychainStatus {
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	return keychainStatus(C.mobile_egress_keychain_delete(serviceValue, accountValue))
}

func darwinKeychainArguments(service, account string, value []byte) (*C.char, *C.char, unsafe.Pointer) {
	return C.CString(service), C.CString(account), C.CBytes(value)
}

type darwinKeychainAttributes struct {
	service                    string
	account                    string
	accessGroup                string
	explicitlyNonSynchronizing bool
	whenUnlockedThisDeviceOnly bool
}

func darwinKeychainItemAttributes(account string) (darwinKeychainAttributes, error) {
	serviceValue := C.CString(keychainService)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))

	var storedService *C.char
	var storedAccount *C.char
	var accessGroup *C.char
	var synchronizableState C.int
	var whenUnlockedThisDeviceOnly C.int
	status := keychainStatus(C.mobile_egress_keychain_copy_attributes(
		serviceValue,
		accountValue,
		&storedService,
		&storedAccount,
		&accessGroup,
		&synchronizableState,
		&whenUnlockedThisDeviceOnly,
	))
	if storedService != nil {
		defer C.mobile_egress_keychain_free(unsafe.Pointer(storedService))
	}
	if storedAccount != nil {
		defer C.mobile_egress_keychain_free(unsafe.Pointer(storedAccount))
	}
	if accessGroup != nil {
		defer C.mobile_egress_keychain_free(unsafe.Pointer(accessGroup))
	}
	if status != keychainStatusSuccess {
		return darwinKeychainAttributes{}, keychainOperationError("read secure value attributes", status)
	}
	return darwinKeychainAttributes{
		service:                    C.GoString(storedService),
		account:                    C.GoString(storedAccount),
		accessGroup:                C.GoString(accessGroup),
		explicitlyNonSynchronizing: synchronizableState == 1,
		whenUnlockedThisDeviceOnly: whenUnlockedThisDeviceOnly != 0,
	}, nil
}
