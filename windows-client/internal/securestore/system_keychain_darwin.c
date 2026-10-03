//go:build darwin && cgo && !bindings

#include "system_keychain_darwin.h"
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <unistd.h>
#include <sys/stat.h>
#include <string.h>
#include <stdlib.h>

// The file-based System Keychain is available to launchd after logout. Explicit
// keychain handles prevent falling through to the invoking user's login chain.
static const char *keychain_path = "/Library/Keychains/System.keychain";

int32_t me_system_keychain_check(void) {
    if (geteuid() != 0) return errSecAuthFailed;
    struct stat st;
    if (lstat(keychain_path, &st) != 0 || !S_ISREG(st.st_mode) || st.st_uid != 0 || (st.st_mode & 0022)) return errSecAuthFailed;
    SecCodeRef code = NULL;
    SecRequirementRef requirement = NULL;
    OSStatus status = SecCodeCopySelf(kSecCSDefaultFlags, &code);
    if (status == errSecSuccess) status = SecRequirementCreateWithString(CFSTR("anchor apple generic and identifier \"com.zfnf.mobile-egress.client\" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists"), kSecCSDefaultFlags, &requirement);
    if (status == errSecSuccess) status = SecCodeCheckValidity(code, kSecCSStrictValidate, requirement);
    if (requirement) CFRelease(requirement);
    if (code) CFRelease(code);
    if (status == errSecSuccess) status = SecKeychainSetUserInteractionAllowed(false);
    if (status == errSecSuccess) {
        SecKeychainRef chain = NULL;
        status = SecKeychainOpen(keychain_path, &chain);
        if (chain) CFRelease(chain);
    }
    return status;
}

int32_t me_system_keychain_operation(int operation, const char *service, const char *account, const void *input, size_t input_length, void **output, uint32_t *output_length) {
    if (!service || strcmp(service, "com.zfnf.mobile-egress.client") != 0 || !account || strlen(account) != 64 || input_length > 0xffffffff) return errSecParam;
    if (output) *output = NULL;
    if (output_length) *output_length = 0;
    OSStatus status = me_system_keychain_check();
    if (status != errSecSuccess) return status;
    SecKeychainRef chain = NULL;
    status = SecKeychainOpen(keychain_path, &chain);
    if (status != errSecSuccess) return status;
    SecKeychainItemRef item = NULL;
    status = SecKeychainFindGenericPassword(chain, (UInt32)strlen(service), service, (UInt32)strlen(account), account, NULL, NULL, &item);
    if (operation == 0) { // add, with a service-only code-signature ACL
        if (status == errSecSuccess) status = errSecDuplicateItem;
        else if (status == errSecItemNotFound) {
            SecTrustedApplicationRef application = NULL;
            SecAccessRef access = NULL;
            CFArrayRef trusted = NULL;
            status = SecTrustedApplicationCreateFromPath(NULL, &application);
            if (status == errSecSuccess) trusted = CFArrayCreate(NULL, (const void **)&application, 1, &kCFTypeArrayCallBacks);
            if (status == errSecSuccess && !trusted) status = errSecAllocate;
            if (status == errSecSuccess) status = SecAccessCreate(CFSTR("Mobile Egress Client daemon"), trusted, &access);
            SecKeychainAttribute attrs[] = {{kSecServiceItemAttr, (UInt32)strlen(service), (void *)service}, {kSecAccountItemAttr, (UInt32)strlen(account), (void *)account}};
            SecKeychainAttributeList list = {2, attrs};
            if (status == errSecSuccess) status = SecKeychainItemCreateFromContent(kSecGenericPasswordItemClass, &list, (UInt32)input_length, input, chain, access, NULL);
            if (access) CFRelease(access);
            if (trusted) CFRelease(trusted);
            if (application) CFRelease(application);
        }
    } else if (status == errSecSuccess) {
        if (operation == 1) status = SecKeychainItemModifyAttributesAndData(item, NULL, (UInt32)input_length, input);
        else if (operation == 2 && output && output_length) {
            void *data = NULL;
            UInt32 length = 0;
            status = SecKeychainItemCopyContent(item, NULL, NULL, &length, &data);
            if (status == errSecSuccess) {
                *output = malloc(length);
                if (!*output) status = errSecAllocate;
                else { memcpy(*output, data, length); *output_length = length; }
                SecKeychainItemFreeContent(NULL, data);
            }
        } else if (operation == 3) status = SecKeychainItemDelete(item);
        else status = errSecParam;
    }
    if (item) CFRelease(item);
    CFRelease(chain);
    return status;
}
