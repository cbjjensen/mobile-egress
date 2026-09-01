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

func newPlatformKeychainNative() (keychainNative, string, string, error) {
	var applicationIdentifier *C.char
	var teamIdentifier *C.char
	status := keychainStatus(C.mobile_egress_keychain_copy_signing_identity(
		&applicationIdentifier,
		&teamIdentifier,
	))
	if applicationIdentifier != nil {
		defer C.mobile_egress_keychain_free(unsafe.Pointer(applicationIdentifier))
	}
	if teamIdentifier != nil {
		defer C.mobile_egress_keychain_free(unsafe.Pointer(teamIdentifier))
	}
	if status != keychainStatusSuccess {
		return nil, "", "", keychainOperationError("read signed macOS application identity", status)
	}
	return darwinKeychainNative{}, C.GoString(applicationIdentifier), C.GoString(teamIdentifier), nil
}

func (darwinKeychainNative) Add(accessGroup, service, account string, value []byte) keychainStatus {
	accessGroupValue, serviceValue, accountValue, data := darwinKeychainArguments(accessGroup, service, account, value)
	defer C.free(unsafe.Pointer(accessGroupValue))
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	defer C.free(data)
	return keychainStatus(C.mobile_egress_keychain_add(
		accessGroupValue,
		serviceValue,
		accountValue,
		data,
		C.size_t(len(value)),
	))
}

func (darwinKeychainNative) Update(accessGroup, service, account string, value []byte) keychainStatus {
	accessGroupValue, serviceValue, accountValue, data := darwinKeychainArguments(accessGroup, service, account, value)
	defer C.free(unsafe.Pointer(accessGroupValue))
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	defer C.free(data)
	return keychainStatus(C.mobile_egress_keychain_update(
		accessGroupValue,
		serviceValue,
		accountValue,
		data,
		C.size_t(len(value)),
	))
}

func (darwinKeychainNative) Get(accessGroup, service, account string) ([]byte, keychainStatus) {
	accessGroupValue := C.CString(accessGroup)
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(accessGroupValue))
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))

	var data *C.uchar
	var length C.size_t
	status := keychainStatus(C.mobile_egress_keychain_copy(
		accessGroupValue,
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

func (darwinKeychainNative) Delete(accessGroup, service, account string) keychainStatus {
	accessGroupValue := C.CString(accessGroup)
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(accessGroupValue))
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	return keychainStatus(C.mobile_egress_keychain_delete(accessGroupValue, serviceValue, accountValue))
}

func darwinKeychainArguments(accessGroup, service, account string, value []byte) (*C.char, *C.char, *C.char, unsafe.Pointer) {
	return C.CString(accessGroup), C.CString(service), C.CString(account), C.CBytes(value)
}

type darwinKeychainAttributes struct {
	service                    string
	account                    string
	accessGroup                string
	explicitlyNonSynchronizing bool
	whenUnlockedThisDeviceOnly bool
}

func darwinKeychainItemAttributes(accessGroupValueString, account string) (darwinKeychainAttributes, error) {
	accessGroupValue := C.CString(accessGroupValueString)
	serviceValue := C.CString(keychainService)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(accessGroupValue))
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))

	var storedService *C.char
	var storedAccount *C.char
	var accessGroup *C.char
	var synchronizableState C.int
	var whenUnlockedThisDeviceOnly C.int
	status := keychainStatus(C.mobile_egress_keychain_copy_attributes(
		accessGroupValue,
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

func darwinKeychainItemPersistentReference(accessGroupValueString, account string) ([]byte, error) {
	accessGroupValue := C.CString(accessGroupValueString)
	serviceValue := C.CString(keychainService)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(accessGroupValue))
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))

	var data *C.uchar
	var length C.size_t
	status := keychainStatus(C.mobile_egress_keychain_copy_persistent_reference(
		accessGroupValue,
		serviceValue,
		accountValue,
		&data,
		&length,
	))
	if data != nil {
		defer C.mobile_egress_keychain_free(unsafe.Pointer(data))
	}
	if status != keychainStatusSuccess {
		return nil, keychainOperationError("read secure value persistent reference", status)
	}
	value := unsafe.Slice((*byte)(unsafe.Pointer(data)), int(length))
	return append([]byte(nil), value...), nil
}
