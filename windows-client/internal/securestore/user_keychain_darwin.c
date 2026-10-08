//go:build darwin && cgo && !bindings

#include "user_keychain_darwin.h"
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <unistd.h>
#include <sys/stat.h>
#include <pwd.h>
#include <limits.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

// This backend deliberately uses the existing Developer ID arrangement, without
// restricted access-group entitlements or a new provisioning profile.
#define USER_REQUIREMENT "anchor apple generic and identifier \"com.zfnf.mobile-egress.client.app\" and certificate leaf[subject.OU] = \"26VMY3JMQ9\" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists"
#define USER_SERVICE "com.zfnf.mobile-egress.client.app.user"
#define MAX_VALUE_BYTES (2 * 1024 * 1024)

static OSStatus open_user_keychain(SecKeychainRef *chain) {
    *chain = NULL;
    uid_t uid = geteuid();
    if (uid < 501 || getuid() != uid) return errSecAuthFailed;
    SecCodeRef code = NULL;
    SecRequirementRef requirement = NULL;
    OSStatus status = SecCodeCopySelf(kSecCSDefaultFlags, &code);
    if (status == errSecSuccess) status = SecRequirementCreateWithString(CFSTR(USER_REQUIREMENT), kSecCSDefaultFlags, &requirement);
    if (status == errSecSuccess) status = SecCodeCheckValidity(code, kSecCSStrictValidate, requirement);
    if (requirement) CFRelease(requirement);
    if (code) CFRelease(code);
    if (status != errSecSuccess) return status;

    struct passwd entry, *user = NULL;
    char user_buffer[16384], path[PATH_MAX];
    if (getpwuid_r(uid, &entry, user_buffer, sizeof(user_buffer), &user) != 0 || !user || !user->pw_dir || user->pw_dir[0] != '/') return errSecAuthFailed;
    int length = snprintf(path, sizeof(path), "%s/Library/Keychains/login.keychain-db", user->pw_dir);
    if (length < 0 || (size_t)length >= sizeof(path)) return errSecParam;
    struct stat st;
    if (lstat(path, &st) != 0 || !S_ISREG(st.st_mode) || st.st_uid != uid || (st.st_mode & 0022)) return errSecAuthFailed;
    // An explicit handle is mandatory for every query: never search System or
    // the user's combined search list, and never create/unlock a missing chain.
    status = SecKeychainOpen(path, chain);
    if (status == errSecSuccess) {
        SecKeychainStatus state = 0;
        status = SecKeychainGetStatus(*chain, &state);
        if (status == errSecSuccess && !(state & kSecUnlockStateStatus)) status = errSecInteractionNotAllowed;
    }
    return status;
}

int32_t me_user_keychain_check(void) {
    SecKeychainRef chain = NULL;
    OSStatus status = open_user_keychain(&chain);
    if (chain) CFRelease(chain);
    return status;
}

int32_t me_user_keychain_operation(int operation, const char *service, const char *account, const void *input, size_t input_length, void **output, uint32_t *output_length) {
    if (output) *output = NULL;
    if (output_length) *output_length = 0;
    if (operation < 0 || operation > 3 || !service || strcmp(service, USER_SERVICE) != 0 || !account || strlen(account) != 64 || input_length > MAX_VALUE_BYTES) return errSecParam;
    if ((operation == 0 || operation == 1) && (!input || input_length == 0)) return errSecParam;
    if (operation == 2 && (!output || !output_length)) return errSecParam;
    for (size_t i = 0; i < 64; i++) if (!((account[i] >= '0' && account[i] <= '9') || (account[i] >= 'a' && account[i] <= 'f'))) return errSecParam;
    SecKeychainRef chain = NULL;
    OSStatus status = open_user_keychain(&chain);
    if (status != errSecSuccess) { if (chain) CFRelease(chain); return status; }
    Boolean interactive = false;
    status = SecKeychainGetUserInteractionAllowed(&interactive);
    if (status == errSecSuccess) status = SecKeychainSetUserInteractionAllowed(false);
    if (status != errSecSuccess) { CFRelease(chain); return status; }
    SecKeychainItemRef item = NULL;
    status = SecKeychainFindGenericPassword(chain, (UInt32)strlen(service), service, (UInt32)strlen(account), account, NULL, NULL, &item);
    if (operation == 0) {
        if (status == errSecSuccess) status = errSecDuplicateItem;
        else if (status == errSecItemNotFound) {
            SecTrustedApplicationRef app = NULL;
            SecAccessRef access = NULL;
            CFArrayRef trusted = NULL;
            status = SecTrustedApplicationCreateFromPath(NULL, &app);
            if (status == errSecSuccess) trusted = CFArrayCreate(NULL, (const void **)&app, 1, &kCFTypeArrayCallBacks);
            if (status == errSecSuccess && !trusted) status = errSecAllocate;
            if (status == errSecSuccess) status = SecAccessCreate(CFSTR("Inevitable Mobile Relay user app"), trusted, &access);
            SecKeychainAttribute attrs[] = {{kSecServiceItemAttr, (UInt32)strlen(service), (void *)service}, {kSecAccountItemAttr, (UInt32)strlen(account), (void *)account}};
            SecKeychainAttributeList list = {2, attrs};
            if (status == errSecSuccess) status = SecKeychainItemCreateFromContent(kSecGenericPasswordItemClass, &list, (UInt32)input_length, input, chain, access, NULL);
            if (access) CFRelease(access);
            if (trusted) CFRelease(trusted);
            if (app) CFRelease(app);
        }
    } else if (status == errSecSuccess) {
        if (operation == 1) status = SecKeychainItemModifyAttributesAndData(item, NULL, (UInt32)input_length, input);
        else if (operation == 2) {
            void *data = NULL;
            UInt32 length = 0;
            status = SecKeychainItemCopyContent(item, NULL, NULL, &length, &data);
            if (status == errSecSuccess) {
                if (length == 0 || length > MAX_VALUE_BYTES) status = errSecDecode;
                else {
                    *output = malloc(length);
                    if (!*output) status = errSecAllocate;
                    else { memcpy(*output, data, length); *output_length = length; }
                }
                SecKeychainItemFreeContent(NULL, data);
            }
        } else status = SecKeychainItemDelete(item);
    }
    if (item) CFRelease(item);
    CFRelease(chain);
    OSStatus restored = SecKeychainSetUserInteractionAllowed(interactive);
    return status == errSecSuccess ? restored : status;
}
