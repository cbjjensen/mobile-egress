//go:build darwin && cgo && !bindings

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

#include <limits.h>
#include <stdlib.h>

#include "keychain_darwin.h"

static CFMutableDictionaryRef mobile_egress_keychain_query(
    const char *service,
    const char *account) {
    if (service == NULL || account == NULL) {
        return NULL;
    }

    CFStringRef service_value = CFStringCreateWithCString(
        kCFAllocatorDefault,
        service,
        kCFStringEncodingUTF8);
    CFStringRef account_value = CFStringCreateWithCString(
        kCFAllocatorDefault,
        account,
        kCFStringEncodingUTF8);
    if (service_value == NULL || account_value == NULL) {
        if (service_value != NULL) {
            CFRelease(service_value);
        }
        if (account_value != NULL) {
            CFRelease(account_value);
        }
        return NULL;
    }

    CFMutableDictionaryRef query = CFDictionaryCreateMutable(
        kCFAllocatorDefault,
        0,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks);
    if (query != NULL) {
        CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
        CFDictionarySetValue(query, kSecAttrService, service_value);
        CFDictionarySetValue(query, kSecAttrAccount, account_value);
        CFDictionarySetValue(query, kSecAttrSynchronizable, kCFBooleanFalse);
        CFDictionarySetValue(query, kSecUseDataProtectionKeychain, kCFBooleanTrue);
        // Deliberately use the signed app's default access group. A stable
        // Developer ID team and bundle identifier therefore retain access
        // across upgrades without granting access to another application.
    }

    CFRelease(service_value);
    CFRelease(account_value);
    return query;
}

static char *mobile_egress_copy_cfstring(CFStringRef value) {
    if (value == NULL || CFGetTypeID(value) != CFStringGetTypeID()) {
        return NULL;
    }
    CFIndex capacity = CFStringGetMaximumSizeForEncoding(
        CFStringGetLength(value),
        kCFStringEncodingUTF8) + 1;
    if (capacity <= 0 || capacity > INT_MAX) {
        return NULL;
    }
    char *copy = malloc((size_t)capacity);
    if (copy == NULL) {
        return NULL;
    }
    if (!CFStringGetCString(value, copy, capacity, kCFStringEncodingUTF8)) {
        free(copy);
        return NULL;
    }
    return copy;
}

int32_t mobile_egress_keychain_add(
    const char *service,
    const char *account,
    const void *value,
    size_t value_length) {
    CFMutableDictionaryRef attributes = mobile_egress_keychain_query(service, account);
    if (attributes == NULL || value == NULL || value_length > LONG_MAX) {
        if (attributes != NULL) {
            CFRelease(attributes);
        }
        return errSecParam;
    }

    CFDataRef data = CFDataCreate(
        kCFAllocatorDefault,
        (const UInt8 *)value,
        (CFIndex)value_length);
    if (data == NULL) {
        CFRelease(attributes);
        return errSecAllocate;
    }
    CFDictionarySetValue(attributes, kSecValueData, data);
    CFDictionarySetValue(
        attributes,
        kSecAttrAccessible,
        kSecAttrAccessibleWhenUnlockedThisDeviceOnly);

    OSStatus status = SecItemAdd(attributes, NULL);
    CFRelease(data);
    CFRelease(attributes);
    return status;
}

int32_t mobile_egress_keychain_update(
    const char *service,
    const char *account,
    const void *value,
    size_t value_length) {
    CFMutableDictionaryRef query = mobile_egress_keychain_query(service, account);
    if (query == NULL || value == NULL || value_length > LONG_MAX) {
        if (query != NULL) {
            CFRelease(query);
        }
        return errSecParam;
    }

    CFDataRef data = CFDataCreate(
        kCFAllocatorDefault,
        (const UInt8 *)value,
        (CFIndex)value_length);
    CFMutableDictionaryRef attributes = CFDictionaryCreateMutable(
        kCFAllocatorDefault,
        0,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks);
    if (data == NULL || attributes == NULL) {
        if (data != NULL) {
            CFRelease(data);
        }
        if (attributes != NULL) {
            CFRelease(attributes);
        }
        CFRelease(query);
        return errSecAllocate;
    }
    CFDictionarySetValue(attributes, kSecValueData, data);

    OSStatus status = SecItemUpdate(query, attributes);
    CFRelease(attributes);
    CFRelease(data);
    CFRelease(query);
    return status;
}

int32_t mobile_egress_keychain_copy(
    const char *service,
    const char *account,
    unsigned char **value,
    size_t *value_length) {
    if (value == NULL || value_length == NULL) {
        return errSecParam;
    }
    *value = NULL;
    *value_length = 0;

    CFMutableDictionaryRef query = mobile_egress_keychain_query(service, account);
    if (query == NULL) {
        return errSecParam;
    }
    CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);

    CFTypeRef result = NULL;
    OSStatus status = SecItemCopyMatching(query, &result);
    CFRelease(query);
    if (status != errSecSuccess) {
        return status;
    }
    if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
        if (result != NULL) {
            CFRelease(result);
        }
        return errSecInternalError;
    }

    CFDataRef data = (CFDataRef)result;
    CFIndex length = CFDataGetLength(data);
    if (length < 0) {
        CFRelease(result);
        return errSecInternalError;
    }
    if (length > 0) {
        *value = malloc((size_t)length);
        if (*value == NULL) {
            CFRelease(result);
            return errSecAllocate;
        }
        CFDataGetBytes(data, CFRangeMake(0, length), *value);
    }
    *value_length = (size_t)length;
    CFRelease(result);
    return errSecSuccess;
}

int32_t mobile_egress_keychain_delete(const char *service, const char *account) {
    CFMutableDictionaryRef query = mobile_egress_keychain_query(service, account);
    if (query == NULL) {
        return errSecParam;
    }
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    return status;
}

int32_t mobile_egress_keychain_copy_attributes(
    const char *service,
    const char *account,
    char **stored_service,
    char **stored_account,
    char **access_group,
    int *synchronizable_state,
    int *when_unlocked_this_device_only) {
    if (stored_service == NULL || stored_account == NULL || access_group == NULL ||
        synchronizable_state == NULL || when_unlocked_this_device_only == NULL) {
        return errSecParam;
    }
    *stored_service = NULL;
    *stored_account = NULL;
    *access_group = NULL;
    *synchronizable_state = 0;
    *when_unlocked_this_device_only = 0;

    CFMutableDictionaryRef query = mobile_egress_keychain_query(service, account);
    if (query == NULL) {
        return errSecParam;
    }
    CFDictionarySetValue(query, kSecReturnAttributes, kCFBooleanTrue);
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);

    CFTypeRef result = NULL;
    OSStatus status = SecItemCopyMatching(query, &result);
    CFRelease(query);
    if (status != errSecSuccess) {
        return status;
    }
    if (result == NULL || CFGetTypeID(result) != CFDictionaryGetTypeID()) {
        if (result != NULL) {
            CFRelease(result);
        }
        return errSecInternalError;
    }

    CFDictionaryRef attributes = (CFDictionaryRef)result;
    CFStringRef service_value = CFDictionaryGetValue(attributes, kSecAttrService);
    CFStringRef account_value = CFDictionaryGetValue(attributes, kSecAttrAccount);
    CFStringRef access_group_value = CFDictionaryGetValue(attributes, kSecAttrAccessGroup);
    CFTypeRef synchronizable_value = CFDictionaryGetValue(attributes, kSecAttrSynchronizable);
    CFTypeRef accessible_value = CFDictionaryGetValue(attributes, kSecAttrAccessible);

    *stored_service = mobile_egress_copy_cfstring(service_value);
    *stored_account = mobile_egress_copy_cfstring(account_value);
    *access_group = mobile_egress_copy_cfstring(access_group_value);
    if (*stored_service == NULL || *stored_account == NULL || *access_group == NULL) {
        free(*stored_service);
        free(*stored_account);
        free(*access_group);
        *stored_service = NULL;
        *stored_account = NULL;
        *access_group = NULL;
        CFRelease(result);
        return errSecAllocate;
    }

    if (synchronizable_value != NULL &&
        CFGetTypeID(synchronizable_value) == CFBooleanGetTypeID()) {
        *synchronizable_state = CFBooleanGetValue((CFBooleanRef)synchronizable_value) ? 2 : 1;
    }
    if (accessible_value != NULL &&
        CFGetTypeID(accessible_value) == CFStringGetTypeID() &&
        CFEqual(accessible_value, kSecAttrAccessibleWhenUnlockedThisDeviceOnly)) {
        *when_unlocked_this_device_only = 1;
    }

    CFRelease(result);
    return errSecSuccess;
}

void mobile_egress_keychain_free(void *value) {
    free(value);
}
