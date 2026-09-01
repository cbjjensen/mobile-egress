//go:build darwin && cgo && !bindings

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <Security/SecTask.h>

#include <limits.h>
#include <stdlib.h>

#include "keychain_darwin.h"

static char *mobile_egress_copy_cfstring(CFStringRef value);

static CFMutableDictionaryRef mobile_egress_keychain_query(
    const char *access_group,
    const char *service,
    const char *account) {
    if (access_group == NULL || service == NULL || account == NULL) {
        return NULL;
    }

    CFStringRef access_group_value = CFStringCreateWithCString(
        kCFAllocatorDefault,
        access_group,
        kCFStringEncodingUTF8);
    CFStringRef service_value = CFStringCreateWithCString(
        kCFAllocatorDefault,
        service,
        kCFStringEncodingUTF8);
    CFStringRef account_value = CFStringCreateWithCString(
        kCFAllocatorDefault,
        account,
        kCFStringEncodingUTF8);
    if (access_group_value == NULL || service_value == NULL || account_value == NULL) {
        if (access_group_value != NULL) {
            CFRelease(access_group_value);
        }
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
        CFDictionarySetValue(query, kSecAttrAccessGroup, access_group_value);
        CFDictionarySetValue(query, kSecAttrService, service_value);
        CFDictionarySetValue(query, kSecAttrAccount, account_value);
        CFDictionarySetValue(query, kSecAttrSynchronizable, kCFBooleanFalse);
        CFDictionarySetValue(query, kSecUseDataProtectionKeychain, kCFBooleanTrue);
    }

    CFRelease(access_group_value);
    CFRelease(service_value);
    CFRelease(account_value);
    return query;
}

int32_t mobile_egress_keychain_copy_signing_identity(
    char **application_identifier,
    char **team_identifier) {
    if (application_identifier == NULL || team_identifier == NULL) {
        return errSecParam;
    }
    *application_identifier = NULL;
    *team_identifier = NULL;

    SecTaskRef task = SecTaskCreateFromSelf(kCFAllocatorDefault);
    if (task == NULL) {
        return errSecMissingEntitlement;
    }
    CFErrorRef application_error = NULL;
    CFErrorRef team_error = NULL;
    CFTypeRef application_value = SecTaskCopyValueForEntitlement(
        task,
        CFSTR("com.apple.application-identifier"),
        &application_error);
    CFTypeRef team_value = SecTaskCopyValueForEntitlement(
        task,
        CFSTR("com.apple.developer.team-identifier"),
        &team_error);
    CFRelease(task);
    if (application_error != NULL) {
        CFRelease(application_error);
    }
    if (team_error != NULL) {
        CFRelease(team_error);
    }
    if (application_value == NULL || team_value == NULL ||
        CFGetTypeID(application_value) != CFStringGetTypeID() ||
        CFGetTypeID(team_value) != CFStringGetTypeID()) {
        if (application_value != NULL) {
            CFRelease(application_value);
        }
        if (team_value != NULL) {
            CFRelease(team_value);
        }
        return errSecMissingEntitlement;
    }

    *application_identifier = mobile_egress_copy_cfstring((CFStringRef)application_value);
    *team_identifier = mobile_egress_copy_cfstring((CFStringRef)team_value);
    CFRelease(application_value);
    CFRelease(team_value);
    if (*application_identifier == NULL || *team_identifier == NULL) {
        free(*application_identifier);
        free(*team_identifier);
        *application_identifier = NULL;
        *team_identifier = NULL;
        return errSecAllocate;
    }
    return errSecSuccess;
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
    const char *access_group,
    const char *service,
    const char *account,
    const void *value,
    size_t value_length) {
    CFMutableDictionaryRef attributes = mobile_egress_keychain_query(access_group, service, account);
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
    const char *access_group,
    const char *service,
    const char *account,
    const void *value,
    size_t value_length) {
    CFMutableDictionaryRef query = mobile_egress_keychain_query(access_group, service, account);
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
    const char *access_group,
    const char *service,
    const char *account,
    unsigned char **value,
    size_t *value_length) {
    if (value == NULL || value_length == NULL) {
        return errSecParam;
    }
    *value = NULL;
    *value_length = 0;

    CFMutableDictionaryRef query = mobile_egress_keychain_query(access_group, service, account);
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

int32_t mobile_egress_keychain_delete(
    const char *access_group,
    const char *service,
    const char *account) {
    CFMutableDictionaryRef query = mobile_egress_keychain_query(access_group, service, account);
    if (query == NULL) {
        return errSecParam;
    }
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    return status;
}

int32_t mobile_egress_keychain_copy_persistent_reference(
    const char *access_group,
    const char *service,
    const char *account,
    unsigned char **value,
    size_t *value_length) {
    if (value == NULL || value_length == NULL) {
        return errSecParam;
    }
    *value = NULL;
    *value_length = 0;

    CFMutableDictionaryRef query = mobile_egress_keychain_query(access_group, service, account);
    if (query == NULL) {
        return errSecParam;
    }
    CFDictionarySetValue(query, kSecReturnPersistentRef, kCFBooleanTrue);
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
    if (length <= 0) {
        CFRelease(result);
        return errSecInternalError;
    }
    *value = malloc((size_t)length);
    if (*value == NULL) {
        CFRelease(result);
        return errSecAllocate;
    }
    CFDataGetBytes(data, CFRangeMake(0, length), *value);
    *value_length = (size_t)length;
    CFRelease(result);
    return errSecSuccess;
}

int32_t mobile_egress_keychain_copy_attributes(
    const char *access_group,
    const char *service,
    const char *account,
    char **stored_service,
    char **stored_account,
    char **stored_access_group,
    int *synchronizable_state,
    int *when_unlocked_this_device_only) {
    if (stored_service == NULL || stored_account == NULL || stored_access_group == NULL ||
        synchronizable_state == NULL || when_unlocked_this_device_only == NULL) {
        return errSecParam;
    }
    *stored_service = NULL;
    *stored_account = NULL;
    *stored_access_group = NULL;
    *synchronizable_state = 0;
    *when_unlocked_this_device_only = 0;

    CFMutableDictionaryRef query = mobile_egress_keychain_query(access_group, service, account);
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
    *stored_access_group = mobile_egress_copy_cfstring(access_group_value);
    if (*stored_service == NULL || *stored_account == NULL || *stored_access_group == NULL) {
        free(*stored_service);
        free(*stored_account);
        free(*stored_access_group);
        *stored_service = NULL;
        *stored_account = NULL;
        *stored_access_group = NULL;
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
