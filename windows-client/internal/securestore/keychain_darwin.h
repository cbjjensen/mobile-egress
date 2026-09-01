#ifndef MOBILE_EGRESS_KEYCHAIN_DARWIN_H
#define MOBILE_EGRESS_KEYCHAIN_DARWIN_H

#include <stddef.h>
#include <stdint.h>

int32_t mobile_egress_keychain_add(
    const char *service,
    const char *account,
    const void *value,
    size_t value_length);

int32_t mobile_egress_keychain_update(
    const char *service,
    const char *account,
    const void *value,
    size_t value_length);

int32_t mobile_egress_keychain_copy(
    const char *service,
    const char *account,
    unsigned char **value,
    size_t *value_length);

int32_t mobile_egress_keychain_delete(const char *service, const char *account);

int32_t mobile_egress_keychain_copy_attributes(
    const char *service,
    const char *account,
    char **stored_service,
    char **stored_account,
    char **access_group,
    int *synchronizable_state,
    int *when_unlocked_this_device_only);

void mobile_egress_keychain_free(void *value);

#endif
