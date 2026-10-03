#include <stdint.h>
#include <stddef.h>
int32_t me_system_keychain_check(void);
int32_t me_system_keychain_operation(int operation, const char *service, const char *account, const void *input, size_t input_length, void **output, uint32_t *output_length);
