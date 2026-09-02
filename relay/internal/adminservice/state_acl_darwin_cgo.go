//go:build darwin && cgo

package adminservice

/*
#cgo CFLAGS: -Wall -Wextra

#include <sys/types.h>
#include <sys/acl.h>
#include <sys/stat.h>
#include <membership.h>
#include <uuid/uuid.h>
#include <errno.h>
#include <stdint.h>
#include <stdlib.h>

enum {
	ZFNF_ACL_REJECT_EXTENDED = 1,
	ZFNF_ACL_REJECT_NON_ROOT_MUTATION = 2
};

static int zfnf_acl_mutates(acl_permset_t permissions, int *error_number) {
	acl_perm_t mutation_permissions[] = {
		ACL_WRITE_DATA,
		ACL_APPEND_DATA,
		ACL_DELETE,
		ACL_DELETE_CHILD,
		ACL_WRITE_ATTRIBUTES,
		ACL_WRITE_EXTATTRIBUTES,
		ACL_WRITE_SECURITY,
		ACL_CHANGE_OWNER
	};
	size_t count = sizeof(mutation_permissions) / sizeof(mutation_permissions[0]);
	for (size_t index = 0; index < count; index++) {
		errno = 0;
		int present = acl_get_perm_np(permissions, mutation_permissions[index]);
		if (present < 0) {
			*error_number = errno != 0 ? errno : EIO;
			return -1;
		}
		if (present > 0) {
			return 1;
		}
	}
	return 0;
}

static int zfnf_validate_acl_contents(acl_t acl, int policy, int *error_number) {
	if (acl_valid(acl) != 0) {
		*error_number = errno != 0 ? errno : EINVAL;
		return -1;
	}

	int outcome = 0;
	int entry_identifier = ACL_FIRST_ENTRY;
	for (;;) {
		acl_entry_t entry;
		errno = 0;
		int result = acl_get_entry(acl, entry_identifier, &entry);
		if (result != 0) {
			int entry_error = errno;
			if (entry_error == EINVAL) {
				break;
			}
			*error_number = entry_error != 0 ? entry_error : EIO;
			outcome = -1;
			break;
		}
		entry_identifier = ACL_NEXT_ENTRY;

		if (policy == ZFNF_ACL_REJECT_EXTENDED) {
			outcome = 1;
			break;
		}
		if (policy != ZFNF_ACL_REJECT_NON_ROOT_MUTATION) {
			*error_number = EINVAL;
			outcome = -1;
			break;
		}

		acl_tag_t tag;
		if (acl_get_tag_type(entry, &tag) != 0) {
			*error_number = errno != 0 ? errno : EIO;
			outcome = -1;
			break;
		}
		if (tag == ACL_EXTENDED_DENY) {
			continue;
		}
		if (tag != ACL_EXTENDED_ALLOW) {
			outcome = 1;
			break;
		}

		acl_permset_t permissions;
		if (acl_get_permset(entry, &permissions) != 0) {
			*error_number = errno != 0 ? errno : EIO;
			outcome = -1;
			break;
		}
		int mutates = zfnf_acl_mutates(permissions, error_number);
		if (mutates < 0) {
			outcome = -1;
			break;
		}
		if (mutates == 0) {
			continue;
		}

		void *qualifier = acl_get_qualifier(entry);
		if (qualifier == NULL) {
			*error_number = errno != 0 ? errno : EIO;
			outcome = -1;
			break;
		}
		id_t identifier = 0;
		int identifier_type = 0;
		int membership_result = mbr_uuid_to_id(*(uuid_t *)qualifier, &identifier, &identifier_type);
		int qualifier_free_result = acl_free(qualifier);
		if (qualifier_free_result != 0) {
			*error_number = errno != 0 ? errno : EIO;
			outcome = -1;
			break;
		}
		if (membership_result != 0 || identifier_type != ID_TYPE_UID || identifier != 0) {
			outcome = 1;
			break;
		}
	}

	return outcome;
}

static int zfnf_validate_acl_object(acl_t acl, int policy, int *error_number) {
	*error_number = 0;
	if (acl == NULL) {
		*error_number = errno != 0 ? errno : EIO;
		return -1;
	}
	int outcome = zfnf_validate_acl_contents(acl, policy, error_number);
	if (acl_free(acl) != 0 && outcome == 0) {
		*error_number = errno != 0 ? errno : EIO;
		return -1;
	}
	return outcome;
}

// The caller opened fd with O_NOFOLLOW_ANY and verified its complete metadata.
static int zfnf_validate_acl_fd(int fd, int policy, int *error_number) {
	errno = 0;
	return zfnf_validate_acl_object(acl_get_fd_np(fd, ACL_TYPE_EXTENDED), policy, error_number);
}

// acl_get_link_np inspects the named object itself rather than following a final symlink.
// Go brackets this read with complete Lstat equality checks.
static int zfnf_validate_acl_path(const char *path, int policy, int *error_number) {
	errno = 0;
	acl_t acl = acl_get_link_np(path, ACL_TYPE_EXTENDED);
	if (acl == NULL && errno == ENOENT) {
		// APFS reports ENOENT when an existing system path has no extended ACL.
		// Confirm the named object still exists without following a final symlink;
		// Go brackets this check with full no-follow Lstat identity validation.
		struct stat metadata;
		if (lstat(path, &metadata) == 0) {
			*error_number = 0;
			return 0;
		}
	}
	return zfnf_validate_acl_object(acl, policy, error_number);
}
*/
import "C"

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

type darwinACLInspector struct{}

func newDarwinACLInspector() pathACLInspector {
	return darwinACLInspector{}
}

func (darwinACLInspector) Validate(ctx context.Context, opened openedPath, policy pathACLPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	native, ok := opened.(interface{ nativeFileDescriptor() (int, error) })
	if !ok || native == nil {
		return errStatePathUnsafe
	}
	fd, err := native.nativeFileDescriptor()
	if err != nil {
		return errStatePathUnsafe
	}
	nativePolicy, err := nativeDarwinACLPolicy(policy)
	if err != nil {
		return err
	}
	var errorNumber C.int
	result := C.zfnf_validate_acl_fd(C.int(fd), nativePolicy, &errorNumber)
	return darwinACLValidationResult(ctx, result, errorNumber)
}

func (darwinACLInspector) ValidatePath(ctx context.Context, path string, policy pathACLPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" || strings.ContainsRune(path, '\x00') {
		return errStatePathUnsafe
	}
	nativePolicy, err := nativeDarwinACLPolicy(policy)
	if err != nil {
		return err
	}
	nativePath := C.CString(path)
	defer C.free(unsafe.Pointer(nativePath))
	var errorNumber C.int
	result := C.zfnf_validate_acl_path(nativePath, nativePolicy, &errorNumber)
	return darwinACLValidationResult(ctx, result, errorNumber)
}

func nativeDarwinACLPolicy(policy pathACLPolicy) (C.int, error) {
	switch policy {
	case pathACLRejectExtended:
		return C.ZFNF_ACL_REJECT_EXTENDED, nil
	case pathACLRejectNonRootMutation:
		return C.ZFNF_ACL_REJECT_NON_ROOT_MUTATION, nil
	default:
		return 0, errStatePathUnsafe
	}
}

func darwinACLValidationResult(ctx context.Context, result C.int, errorNumber C.int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch result {
	case 0:
		return nil
	case 1:
		return errStatePathUnsafe
	default:
		if errorNumber == 0 {
			return errStatePathUnsafe
		}
		return fmt.Errorf("native ACL inspection: %w", syscall.Errno(errorNumber))
	}
}

var _ pathACLInspector = darwinACLInspector{}
