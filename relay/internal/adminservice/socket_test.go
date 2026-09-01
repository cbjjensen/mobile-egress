package adminservice

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"strings"
	"sync"
	"testing"
)

const (
	socketTestPath     = "/test/run/admin.sock"
	socketTestLockPath = "/test/run/admin.lock"
)

func TestAdminSocketValidatesCanonicalParentBeforeLockOrSocket(t *testing.T) {
	errACL := errors.New("unsafe ancestor ACL")
	tests := []struct {
		name  string
		alter func(*socketTestHarness)
	}{
		{
			name: "alternate canonical parent",
			alter: func(harness *socketTestHarness) {
				harness.platform.canonicalParent = "/alternate/run"
			},
		},
		{
			name: "unsafe ancestor type",
			alter: func(harness *socketTestHarness) {
				metadata := harness.platform.metadata["/test"][0].metadata
				metadata.Type = pathTypeRegular
				harness.platform.metadata["/test"] = []socketTestMetadataResult{{metadata: metadata}}
			},
		},
		{
			name: "unsafe ancestor owner",
			alter: func(harness *socketTestHarness) {
				metadata := harness.platform.metadata["/test"][0].metadata
				metadata.UID = 501
				harness.platform.metadata["/test"] = []socketTestMetadataResult{{metadata: metadata}}
			},
		},
		{
			name: "writable parent",
			alter: func(harness *socketTestHarness) {
				metadata := harness.platform.metadata["/test/run"][0].metadata
				metadata.Permissions = 0o775
				harness.platform.metadata["/test/run"] = []socketTestMetadataResult{{metadata: metadata}}
			},
		},
		{
			name: "weakening ACL",
			alter: func(harness *socketTestHarness) {
				harness.acl.pathErrors["/test"] = []error{errACL}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newSocketTestHarness()
			test.alter(harness)
			owner, err := openAdminSocket(context.Background(), harness.config())
			if err == nil || owner != nil {
				t.Fatalf("openAdminSocket = (%v, %v), want nil owner and error", owner, err)
			}
			for _, operation := range harness.platform.operations() {
				if strings.Contains(operation, socketTestLockPath) || strings.Contains(operation, socketTestPath) || strings.HasPrefix(operation, "listen") {
					t.Fatalf("unsafe parent reached lock/socket operation %q; operations: %v", operation, harness.platform.operations())
				}
			}
		})
	}
}

func TestAdminSocketCreatesOrOpensPersistentLockSafely(t *testing.T) {
	t.Run("create exclusive", func(t *testing.T) {
		harness := newSocketTestHarness()
		_, _ = openAdminSocket(context.Background(), harness.config())
		want := []socketTestLockOpen{{path: socketTestLockPath, disposition: lockOpenCreateExclusive, mode: 0o600}}
		assertSocketTestLockOpens(t, harness.platform.lockOpens, want)
	})

	t.Run("existing only after already exists", func(t *testing.T) {
		harness := newSocketTestHarness()
		harness.platform.openLockResults = []socketTestOpenLockResult{
			{err: fs.ErrExist},
			{lock: harness.lock},
		}
		_, _ = openAdminSocket(context.Background(), harness.config())
		want := []socketTestLockOpen{
			{path: socketTestLockPath, disposition: lockOpenCreateExclusive, mode: 0o600},
			{path: socketTestLockPath, disposition: lockOpenExisting, mode: 0o600},
		}
		assertSocketTestLockOpens(t, harness.platform.lockOpens, want)
	})

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "permission failure", err: fs.ErrPermission},
		{name: "arbitrary failure", err: errors.New("lock open failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newSocketTestHarness()
			harness.platform.openLockResults = []socketTestOpenLockResult{{err: test.err}}
			_, _ = openAdminSocket(context.Background(), harness.config())
			want := []socketTestLockOpen{{path: socketTestLockPath, disposition: lockOpenCreateExclusive, mode: 0o600}}
			assertSocketTestLockOpens(t, harness.platform.lockOpens, want)
		})
	}
}

func TestAdminSocketOpensDarwinVarRunLockThroughValidatedCanonicalParent(t *testing.T) {
	harness := newSocketTestHarness()
	harness.platform.canonicalParent = "/private/var/run"
	harness.platform.metadata = map[string][]socketTestMetadataResult{
		"/":                {{metadata: socketTestDirectoryMetadata(1)}},
		"/private":         {{metadata: socketTestDirectoryMetadata(2)}},
		"/private/var":     {{metadata: socketTestDirectoryMetadata(3)}},
		"/private/var/run": {{metadata: socketTestDirectoryMetadata(4)}},
		"/var/run/admin.lock": {
			{metadata: socketTestLockMetadata()},
			{metadata: socketTestLockMetadata()},
		},
		"/var/run/admin.sock": {{err: fs.ErrNotExist}},
	}
	config := adminSocketConfig{
		SocketPath:         "/var/run/admin.sock",
		LockPath:           "/var/run/admin.lock",
		LexicalParent:      "/var/run",
		CanonicalParent:    "/private/var/run",
		CanonicalAncestors: []string{"/", "/private", "/private/var", "/private/var/run"},
		AdminGID:           80,
		Platform:           harness.platform,
		ACL:                harness.acl,
	}
	harness.platform.openLockResults = []socketTestOpenLockResult{
		{err: fs.ErrExist},
		{lock: harness.lock},
	}

	_, _ = openAdminSocket(context.Background(), config)
	want := []socketTestLockOpen{
		{path: "/private/var/run/admin.lock", disposition: lockOpenCreateExclusive, mode: 0o600},
		{path: "/private/var/run/admin.lock", disposition: lockOpenExisting, mode: 0o600},
	}
	assertSocketTestLockOpens(t, harness.platform.lockOpens, want)
	operations := harness.platform.operations()
	canonicalIndex := socketTestOperationIndex(operations, "canonical:/var/run")
	openIndex := socketTestOperationIndex(operations, "open-lock:/private/var/run/admin.lock")
	if canonicalIndex < 0 || openIndex <= canonicalIndex {
		t.Fatalf("canonical validation must precede canonical lock open: %v", operations)
	}
	if socketTestOperationCount(operations, "lstat:/var/run/admin.lock") == 0 {
		t.Fatalf("logical lock path was not retained for path/descriptor verification: %v", operations)
	}
}

func TestAdminSocketRejectsUnexpectedDarwinVarRunMappingBeforeLockOpen(t *testing.T) {
	harness := newSocketTestHarness()
	harness.platform.canonicalParent = "/private/var/runtime"
	config := harness.config()
	config.SocketPath = "/var/run/admin.sock"
	config.LockPath = "/var/run/admin.lock"
	config.LexicalParent = "/var/run"
	config.CanonicalParent = "/private/var/run"
	config.CanonicalAncestors = []string{"/", "/private", "/private/var", "/private/var/run"}

	owner, err := openAdminSocket(context.Background(), config)
	if owner != nil || err == nil {
		t.Fatalf("unexpected parent mapping = (%v, %v), want rejection", owner, err)
	}
	if len(harness.platform.lockOpens) != 0 {
		t.Fatalf("unexpected parent mapping reached lock open: %+v", harness.platform.lockOpens)
	}
}

func TestAdminSocketVerifiesLockBeforeAndAfterExclusiveFlock(t *testing.T) {
	harness := newSocketTestHarness()
	_, _ = openAdminSocket(context.Background(), harness.config())
	want := []string{
		"lock-fstat",
		"lstat:" + socketTestLockPath,
		"acl-path:" + socketTestLockPath + ":reject-extended",
		"lstat:" + socketTestLockPath,
		"lock-exclusive",
		"lock-fstat",
		"lstat:" + socketTestLockPath,
		"acl-path:" + socketTestLockPath + ":reject-extended",
		"lstat:" + socketTestLockPath,
	}
	got := filterSocketTestOperations(harness.platform.operations(), func(operation string) bool {
		return strings.HasPrefix(operation, "lock-fstat") || strings.HasPrefix(operation, "lock-exclusive") ||
			strings.Contains(operation, socketTestLockPath) && !strings.HasPrefix(operation, "open-lock")
	})
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("lock verification order = %v, want %v", got, want)
	}

	valid := socketTestLockMetadata()
	mutations := []struct {
		name   string
		mutate func(*pathMetadata)
	}{
		{name: "identity", mutate: func(metadata *pathMetadata) { metadata.Inode++ }},
		{name: "owner", mutate: func(metadata *pathMetadata) { metadata.UID = 501 }},
		{name: "type", mutate: func(metadata *pathMetadata) { metadata.Type = pathTypeDirectory }},
		{name: "links", mutate: func(metadata *pathMetadata) { metadata.Links = 2 }},
		{name: "mode", mutate: func(metadata *pathMetadata) { metadata.Permissions = 0o640 }},
	}
	for _, mutation := range mutations {
		for _, phase := range []string{"before", "after"} {
			t.Run(phase+"_"+mutation.name, func(t *testing.T) {
				harness := newSocketTestHarness()
				invalid := valid
				mutation.mutate(&invalid)
				if phase == "before" {
					harness.lock.fstatResults = []socketTestMetadataResult{{metadata: invalid}}
				} else {
					harness.lock.fstatResults = []socketTestMetadataResult{{metadata: valid}, {metadata: invalid}}
				}
				owner, err := openAdminSocket(context.Background(), harness.config())
				if err == nil || owner != nil {
					t.Fatalf("invalid %s lock %s = (%v, %v), want failure", phase, mutation.name, owner, err)
				}
				operations := harness.platform.operations()
				if phase == "before" && socketTestOperationCount(operations, "lock-exclusive") != 0 {
					t.Fatalf("invalid pre-lock shape attempted flock: %v", operations)
				}
				if phase == "after" && (socketTestOperationCount(operations, "lock-unlock") != 1 || socketTestOperationCount(operations, "lock-close") != 1) {
					t.Fatalf("invalid post-lock shape did not release lock exactly once: %v", operations)
				}
			})
		}
	}
}

func TestAdminSocketBusyLockNeverInspectsSocket(t *testing.T) {
	for _, lockErr := range []error{fs.ErrExist, errors.New("flock failed")} {
		harness := newSocketTestHarness()
		harness.lock.tryErr = lockErr
		owner, err := openAdminSocket(context.Background(), harness.config())
		if err == nil || owner != nil {
			t.Fatalf("busy lock = (%v, %v), want failure", owner, err)
		}
		for _, operation := range harness.platform.operations() {
			if strings.Contains(operation, socketTestPath) || strings.HasPrefix(operation, "listen") || strings.HasPrefix(operation, "unlink:") {
				t.Fatalf("busy lock reached socket operation %q; operations: %v", operation, harness.platform.operations())
			}
		}
	}
}

func TestAdminSocketReportsDescriptorCloseFailureBeforeExclusiveOwnership(t *testing.T) {
	for _, phase := range []string{"preflight", "flock"} {
		t.Run(phase, func(t *testing.T) {
			harness := newSocketTestHarness()
			primaryErr := errors.New("primary lock failure")
			closeErr := errors.New("lock descriptor close failure")
			harness.lock.closeErr = closeErr
			if phase == "preflight" {
				harness.lock.fstatResults = []socketTestMetadataResult{{err: primaryErr}}
			} else {
				harness.lock.tryErr = primaryErr
			}
			owner, err := openAdminSocket(context.Background(), harness.config())
			if owner != nil {
				t.Fatalf("openAdminSocket owner = %v, want nil", owner)
			}
			for _, want := range []error{primaryErr, closeErr} {
				if !errors.Is(err, want) {
					t.Fatalf("openAdminSocket error %v does not preserve %v", err, want)
				}
			}
		})
	}
}

func TestAdminSocketNeverUnlinksPersistentLock(t *testing.T) {
	scenarios := []func(*socketTestHarness){
		func(harness *socketTestHarness) { harness.platform.canonicalErr = errors.New("canonical failure") },
		func(harness *socketTestHarness) {
			harness.platform.openLockResults = []socketTestOpenLockResult{{err: fs.ErrPermission}}
		},
		func(harness *socketTestHarness) { harness.lock.tryErr = errors.New("busy") },
		func(harness *socketTestHarness) {
			harness.lock.fstatResults = []socketTestMetadataResult{{metadata: socketTestLockMetadata()}, {err: errors.New("post-lock fstat")}}
		},
	}
	for index, scenario := range scenarios {
		harness := newSocketTestHarness()
		scenario(harness)
		_, _ = openAdminSocket(context.Background(), harness.config())
		assertSocketTestLockNotUnlinked(t, index, harness.platform.operations())
	}

	harness := newSocketTestHarness()
	owner := &AdminSocket{lock: harness.lock}
	_ = owner.Close()
	assertSocketTestLockNotUnlinked(t, len(scenarios), harness.platform.operations())
}

func TestAdminSocketAcceptsMissingSocketAfterExclusiveLock(t *testing.T) {
	harness := newSocketTestHarness()
	bindErr := errors.New("bind reached")
	harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{{err: fs.ErrNotExist}}
	harness.platform.listenErr = bindErr
	owner, err := openAdminSocket(context.Background(), harness.config())
	if owner != nil || !errors.Is(err, bindErr) {
		t.Fatalf("missing predecessor = (%v, %v), want bind sentinel", owner, err)
	}
	operations := harness.platform.operations()
	lockIndex := socketTestOperationIndex(operations, "lock-exclusive")
	socketIndex := socketTestOperationIndex(operations, "lstat:"+socketTestPath)
	listenIndex := socketTestOperationIndex(operations, "listen:"+socketTestPath)
	if lockIndex < 0 || socketIndex <= lockIndex || listenIndex <= socketIndex {
		t.Fatalf("missing predecessor order = %v", operations)
	}
}

func TestAdminSocketRecoversVerifiedProvisionalPredecessor(t *testing.T) {
	for _, gid := range []uint32{0, 20, 80, 999} {
		harness := newSocketTestHarness()
		bindErr := errors.New("bind reached")
		metadata := socketTestSocketMetadata(100+uint64(gid), gid, 0o700)
		harness.platform.metadata[socketTestPath] = repeatSocketTestMetadata(metadata, 4)
		harness.platform.listenErr = bindErr
		owner, err := openAdminSocket(context.Background(), harness.config())
		if owner != nil || !errors.Is(err, bindErr) {
			t.Fatalf("provisional gid %d = (%v, %v), want bind sentinel", gid, owner, err)
		}
		operations := harness.platform.operations()
		if socketTestOperationCount(operations, "unlink:"+socketTestPath) != 1 || socketTestOperationCount(operations, "listen:"+socketTestPath) != 1 {
			t.Fatalf("provisional gid %d recovery operations = %v", gid, operations)
		}
	}
}

func TestAdminSocketRecoversVerifiedCompletedPredecessor(t *testing.T) {
	harness := newSocketTestHarness()
	bindErr := errors.New("bind reached")
	metadata := socketTestSocketMetadata(181, 80, 0o660)
	harness.platform.metadata[socketTestPath] = repeatSocketTestMetadata(metadata, 4)
	harness.platform.listenErr = bindErr
	owner, err := openAdminSocket(context.Background(), harness.config())
	if owner != nil || !errors.Is(err, bindErr) {
		t.Fatalf("completed predecessor = (%v, %v), want bind sentinel", owner, err)
	}
	operations := harness.platform.operations()
	if socketTestOperationCount(operations, "unlink:"+socketTestPath) != 1 || socketTestOperationCount(operations, "listen:"+socketTestPath) != 1 {
		t.Fatalf("completed recovery operations = %v", operations)
	}
}

func TestAdminSocketRejectsUnexpectedPredecessorShapes(t *testing.T) {
	valid := socketTestSocketMetadata(200, 80, 0o660)
	tests := []struct {
		name   string
		mutate func(*pathMetadata)
		aclErr error
	}{
		{name: "symlink", mutate: func(metadata *pathMetadata) { metadata.Type = pathTypeSymlink }},
		{name: "regular", mutate: func(metadata *pathMetadata) { metadata.Type = pathTypeRegular }},
		{name: "directory", mutate: func(metadata *pathMetadata) { metadata.Type = pathTypeDirectory }},
		{name: "other special", mutate: func(metadata *pathMetadata) { metadata.Type = pathTypeOther }},
		{name: "nonroot owner", mutate: func(metadata *pathMetadata) { metadata.UID = 501 }},
		{name: "zero links", mutate: func(metadata *pathMetadata) { metadata.Links = 0 }},
		{name: "hard link", mutate: func(metadata *pathMetadata) { metadata.Links = 2 }},
		{name: "mode 0600", mutate: func(metadata *pathMetadata) { metadata.Permissions = 0o600 }},
		{name: "mode 0666", mutate: func(metadata *pathMetadata) { metadata.Permissions = 0o666 }},
		{name: "mode 0770", mutate: func(metadata *pathMetadata) { metadata.Permissions = 0o770 }},
		{name: "completed wrong gid", mutate: func(metadata *pathMetadata) { metadata.GID = 20 }},
		{name: "extended acl", aclErr: errors.New("extended ACL")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newSocketTestHarness()
			metadata := valid
			if test.mutate != nil {
				test.mutate(&metadata)
			}
			harness.platform.metadata[socketTestPath] = repeatSocketTestMetadata(metadata, 4)
			if test.aclErr != nil {
				harness.acl.pathErrors[socketTestPath] = []error{test.aclErr}
			}
			owner, err := openAdminSocket(context.Background(), harness.config())
			if owner != nil || err == nil {
				t.Fatalf("unexpected predecessor = (%v, %v), want failure", owner, err)
			}
			assertRejectedSocketTestLifecycle(t, harness)
		})
	}
}

func TestAdminSocketRevalidatesPredecessorImmediatelyBeforeUnlink(t *testing.T) {
	valid := socketTestSocketMetadata(300, 80, 0o660)
	for _, test := range []struct {
		name   string
		mutate func(*pathMetadata)
	}{
		{name: "identity replacement", mutate: func(metadata *pathMetadata) { metadata.Inode++ }},
		{name: "same inode mode change", mutate: func(metadata *pathMetadata) { metadata.Permissions = 0o666 }},
		{name: "same inode owner change", mutate: func(metadata *pathMetadata) { metadata.UID = 501 }},
		{name: "same inode acl race", mutate: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newSocketTestHarness()
			changed := valid
			if test.mutate != nil {
				test.mutate(&changed)
				harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{
					{metadata: valid}, {metadata: valid}, {metadata: valid}, {metadata: changed},
				}
			} else {
				harness.platform.metadata[socketTestPath] = repeatSocketTestMetadata(valid, 4)
				harness.acl.pathErrors[socketTestPath] = []error{nil, errors.New("ACL changed")}
			}
			owner, err := openAdminSocket(context.Background(), harness.config())
			if owner != nil || err == nil {
				t.Fatalf("changed predecessor = (%v, %v), want failure", owner, err)
			}
			assertRejectedSocketTestLifecycle(t, harness)
		})
	}
}

func TestAdminSocketNeverRecoversBeforeExclusiveLock(t *testing.T) {
	harness := newSocketTestHarness()
	harness.lock.tryErr = errors.New("busy")
	harness.platform.metadata[socketTestPath] = repeatSocketTestMetadata(socketTestSocketMetadata(400, 80, 0o660), 4)
	_, _ = openAdminSocket(context.Background(), harness.config())
	if harness.platform.metadataCalls[socketTestPath] != 0 {
		t.Fatalf("busy lock observed predecessor %d times; operations: %v", harness.platform.metadataCalls[socketTestPath], harness.platform.operations())
	}
	operations := harness.platform.operations()
	if socketTestOperationCount(operations, "lock-unlock") != 0 || socketTestOperationCount(operations, "lock-close") != 1 {
		t.Fatalf("failed flock cleanup = %v, want close without unlock", operations)
	}
	assertSocketTestLockNotUnlinked(t, -1, operations)
}

func TestAdminSocketPublicationOrder(t *testing.T) {
	harness := newSocketTestHarness()
	_, _, _ = configureSocketTestPublication(harness, 500)
	owner, err := openAdminSocket(context.Background(), harness.config())
	if err != nil || owner == nil {
		t.Fatalf("openAdminSocket = (%v, %v), want published owner", owner, err)
	}
	operations := harness.platform.operations()
	want := []string{
		"listen:" + socketTestPath,
		"listener-auto-unlink:false",
		"lstat:" + socketTestPath,
		"acl-path:" + socketTestPath + ":reject-extended",
		"chown:" + socketTestPath,
		"lstat:" + socketTestPath,
		"chmod:" + socketTestPath,
		"lstat:" + socketTestPath,
		"acl-path:" + socketTestPath + ":reject-extended",
	}
	last := -1
	for _, operation := range want {
		index := socketTestOperationIndexAfter(operations, operation, last+1)
		if index < 0 {
			t.Fatalf("publication operation %q missing after index %d: %v", operation, last, operations)
		}
		last = index
	}
	if harness.listener.unlinkOnClose {
		t.Fatal("listener automatic unlink remained enabled")
	}
}

func TestAdminSocketCapturesProvisionalIdentityBeforeMutation(t *testing.T) {
	harness := newSocketTestHarness()
	provisional, _, final := configureSocketTestPublication(harness, 510)
	harness.acl.pathErrors[socketTestPath] = []error{nil, errors.New("final ACL rejected")}
	harness.platform.metadata[socketTestPath] = append(harness.platform.metadata[socketTestPath], socketTestMetadataResult{metadata: final})
	owner, err := openAdminSocket(context.Background(), harness.config())
	if owner != nil || err == nil {
		t.Fatalf("publication ACL failure = (%v, %v), want error", owner, err)
	}
	if len(harness.platform.unlinkCalls) != 1 || harness.platform.unlinkCalls[0].identity != provisional.Identity() {
		t.Fatalf("cleanup unlink calls = %+v, want provisional identity %+v", harness.platform.unlinkCalls, provisional.Identity())
	}
}

func TestAdminSocketPublishesOnlyExactRootAdmin0660Socket(t *testing.T) {
	provisionalMutations := []struct {
		name   string
		mutate func(*pathMetadata)
	}{
		{name: "uid", mutate: func(metadata *pathMetadata) { metadata.UID = 501 }},
		{name: "gid", mutate: func(metadata *pathMetadata) { metadata.GID = 20 }},
		{name: "type", mutate: func(metadata *pathMetadata) { metadata.Type = pathTypeRegular }},
		{name: "links", mutate: func(metadata *pathMetadata) { metadata.Links = 2 }},
		{name: "wrong_umask", mutate: func(metadata *pathMetadata) { metadata.Permissions = 0o755 }},
	}
	for _, mutation := range provisionalMutations {
		t.Run("provisional_"+mutation.name, func(t *testing.T) {
			harness := newSocketTestHarness()
			provisional := socketTestSocketMetadata(520, 0, 0o700)
			mutation.mutate(&provisional)
			harness.listener = &socketTestListener{platform: harness.platform, unlinkOnClose: true}
			harness.platform.listener = harness.listener
			harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{{err: fs.ErrNotExist}, {metadata: provisional}}
			harness.platform.afterUnlink = map[string][]socketTestMetadataResult{socketTestPath: {{err: fs.ErrNotExist}}}
			owner, err := openAdminSocket(context.Background(), harness.config())
			if owner != nil || err == nil {
				t.Fatalf("invalid provisional = (%v, %v), want failure", owner, err)
			}
			if socketTestOperationCount(harness.platform.operations(), "chown:"+socketTestPath) != 0 {
				t.Fatalf("invalid provisional reached chown: %v", harness.platform.operations())
			}
			if provisional.Type == pathTypeSocket {
				if len(harness.platform.unlinkCalls) != 1 || harness.platform.unlinkCalls[0].identity != provisional.Identity() {
					t.Fatalf("invalid provisional socket cleanup = %+v, want captured identity %+v", harness.platform.unlinkCalls, provisional.Identity())
				}
				operations := harness.platform.operations()
				closeIndex := socketTestOperationIndex(operations, "listener-close")
				unlinkIndex := socketTestOperationIndex(operations, "unlink:"+socketTestPath)
				unlockIndex := socketTestOperationIndex(operations, "lock-unlock")
				if closeIndex < 0 || unlinkIndex <= closeIndex || unlockIndex <= unlinkIndex {
					t.Fatalf("invalid provisional cleanup order = %v", operations)
				}
			} else if len(harness.platform.unlinkCalls) != 0 {
				t.Fatalf("invalid provisional non-socket was removed: %+v", harness.platform.unlinkCalls)
			}
		})
	}

	t.Run("provisional_acl", func(t *testing.T) {
		harness := newSocketTestHarness()
		provisional, _, _ := configureSocketTestPublication(harness, 521)
		harness.acl.pathErrors[socketTestPath] = []error{errors.New("extended ACL")}
		harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{
			{err: fs.ErrNotExist}, {metadata: provisional}, {metadata: provisional}, {metadata: provisional},
		}
		owner, err := openAdminSocket(context.Background(), harness.config())
		if owner != nil || err == nil {
			t.Fatalf("provisional ACL = (%v, %v), want failure", owner, err)
		}
	})

	finalMutations := []struct {
		name   string
		mutate func(*pathMetadata)
	}{
		{name: "uid", mutate: func(metadata *pathMetadata) { metadata.UID = 501 }},
		{name: "gid", mutate: func(metadata *pathMetadata) { metadata.GID = 20 }},
		{name: "type", mutate: func(metadata *pathMetadata) { metadata.Type = pathTypeRegular }},
		{name: "links", mutate: func(metadata *pathMetadata) { metadata.Links = 2 }},
		{name: "mode", mutate: func(metadata *pathMetadata) { metadata.Permissions = 0o600 }},
	}
	for _, mutation := range finalMutations {
		t.Run("final_"+mutation.name, func(t *testing.T) {
			harness := newSocketTestHarness()
			provisional := socketTestSocketMetadata(530, 0, 0o700)
			chowned := provisional
			chowned.GID = 80
			final := chowned
			final.Permissions = 0o660
			mutation.mutate(&final)
			harness.listener = &socketTestListener{platform: harness.platform, unlinkOnClose: true}
			harness.platform.listener = harness.listener
			harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{
				{err: fs.ErrNotExist}, {metadata: provisional}, {metadata: provisional}, {metadata: provisional},
				{metadata: chowned}, {metadata: final}, {metadata: final},
			}
			harness.platform.afterUnlink = map[string][]socketTestMetadataResult{socketTestPath: {{err: fs.ErrNotExist}}}
			owner, err := openAdminSocket(context.Background(), harness.config())
			if owner != nil || err == nil {
				t.Fatalf("invalid final = (%v, %v), want failure", owner, err)
			}
		})
	}

	t.Run("final_acl", func(t *testing.T) {
		harness := newSocketTestHarness()
		_, _, final := configureSocketTestPublication(harness, 531)
		harness.acl.pathErrors[socketTestPath] = []error{nil, errors.New("extended ACL")}
		harness.platform.metadata[socketTestPath] = append(harness.platform.metadata[socketTestPath], socketTestMetadataResult{metadata: final})
		owner, err := openAdminSocket(context.Background(), harness.config())
		if owner != nil || err == nil {
			t.Fatalf("final ACL = (%v, %v), want failure", owner, err)
		}
	})
}

func TestAdminSocketInvalidProvisionalCleanupPreservesReplacementMissingAndNonSocket(t *testing.T) {
	for _, test := range []struct {
		name    string
		cleanup socketTestMetadataResult
	}{
		{name: "replacement", cleanup: socketTestMetadataResult{metadata: socketTestSocketMetadata(581, 20, 0o700)}},
		{name: "missing", cleanup: socketTestMetadataResult{err: fs.ErrNotExist}},
		{name: "non-socket", cleanup: socketTestMetadataResult{metadata: func() pathMetadata {
			metadata := socketTestSocketMetadata(580, 20, 0o700)
			metadata.Type = pathTypeRegular
			return metadata
		}()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newSocketTestHarness()
			invalid := socketTestSocketMetadata(580, 20, 0o700)
			harness.listener = &socketTestListener{platform: harness.platform, unlinkOnClose: true}
			harness.platform.listener = harness.listener
			harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{
				{err: fs.ErrNotExist}, {metadata: invalid}, test.cleanup,
			}

			owner, err := openAdminSocket(context.Background(), harness.config())
			if owner != nil || err == nil {
				t.Fatalf("invalid provisional cleanup %s = (%v, %v), want failure", test.name, owner, err)
			}
			if len(harness.platform.unlinkCalls) != 0 {
				t.Fatalf("invalid provisional cleanup %s removed an unverified path: %+v", test.name, harness.platform.unlinkCalls)
			}
		})
	}
}

func TestAdminSocketInvalidProvisionalCleanupAllowsRetry(t *testing.T) {
	harness := newSocketTestHarness()
	invalid := socketTestSocketMetadata(590, 20, 0o700)
	harness.listener = &socketTestListener{platform: harness.platform, unlinkOnClose: true}
	harness.platform.listener = harness.listener
	harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{
		{err: fs.ErrNotExist}, {metadata: invalid}, {metadata: invalid},
	}
	harness.platform.afterUnlink = map[string][]socketTestMetadataResult{socketTestPath: {{err: fs.ErrNotExist}}}

	owner, err := openAdminSocket(context.Background(), harness.config())
	if owner != nil || err == nil {
		t.Fatalf("invalid provisional first attempt = (%v, %v), want failure", owner, err)
	}
	if len(harness.platform.unlinkCalls) != 1 || harness.platform.unlinkCalls[0].identity != invalid.Identity() {
		t.Fatalf("first attempt did not remove captured poison socket: %+v", harness.platform.unlinkCalls)
	}

	configureSocketTestPublication(harness, 591)
	owner, err = openAdminSocket(context.Background(), harness.config())
	if err != nil || owner == nil {
		t.Fatalf("retry after captured cleanup = (%v, %v), want success", owner, err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("close retry owner: %v", err)
	}
}

func TestAdminSocketBindFailureReleasesLockWithoutSocketCleanup(t *testing.T) {
	harness := newSocketTestHarness()
	bindErr := errors.New("bind failed")
	predecessor := socketTestSocketMetadata(540, 80, 0o660)
	harness.platform.metadata[socketTestPath] = repeatSocketTestMetadata(predecessor, 8)
	harness.platform.listenErr = bindErr
	owner, err := openAdminSocket(context.Background(), harness.config())
	if owner != nil || !errors.Is(err, bindErr) {
		t.Fatalf("bind failure = (%v, %v), want sentinel", owner, err)
	}
	operations := harness.platform.operations()
	if socketTestOperationCount(operations, "unlink:"+socketTestPath) != 1 {
		t.Fatalf("bind failure cleanup guessed a second socket identity: %v", operations)
	}
	if socketTestOperationCount(operations, "listener-close") != 0 || socketTestOperationCount(operations, "lock-unlock") != 1 || socketTestOperationCount(operations, "lock-close") != 1 {
		t.Fatalf("bind failure cleanup = %v", operations)
	}
}

func TestAdminSocketPublicationFailureCleansOnlyCapturedInode(t *testing.T) {
	t.Run("unknown provisional identity is preserved", func(t *testing.T) {
		harness := newSocketTestHarness()
		harness.listener = &socketTestListener{platform: harness.platform, unlinkOnClose: true}
		harness.platform.listener = harness.listener
		harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{{err: fs.ErrNotExist}, {err: errors.New("provisional lstat failed")}}
		owner, err := openAdminSocket(context.Background(), harness.config())
		if owner != nil || err == nil {
			t.Fatalf("unknown provisional = (%v, %v), want failure", owner, err)
		}
		if len(harness.platform.unlinkCalls) != 0 || harness.listener.closeCalls != 1 {
			t.Fatalf("unknown provisional cleanup = unlinks %+v close %d", harness.platform.unlinkCalls, harness.listener.closeCalls)
		}
	})

	for _, phase := range []string{"provisional acl", "chown", "chmod", "final acl"} {
		t.Run(phase+" removes unchanged captured inode", func(t *testing.T) {
			harness := newSocketTestHarness()
			provisional, _, final := configureSocketTestPublication(harness, 550)
			switch phase {
			case "provisional acl":
				harness.acl.pathErrors[socketTestPath] = []error{errors.New("provisional ACL failed")}
				harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{
					{err: fs.ErrNotExist}, {metadata: provisional}, {metadata: provisional}, {metadata: provisional},
				}
			case "chown":
				harness.platform.chownErr = errors.New("chown failed")
				harness.platform.metadata[socketTestPath] = append(harness.platform.metadata[socketTestPath][:4], socketTestMetadataResult{metadata: provisional})
			case "chmod":
				harness.platform.chmodErr = errors.New("chmod failed")
				harness.platform.metadata[socketTestPath] = append(harness.platform.metadata[socketTestPath][:5], socketTestMetadataResult{metadata: final})
			case "final acl":
				harness.acl.pathErrors[socketTestPath] = []error{nil, errors.New("final ACL failed")}
				harness.platform.metadata[socketTestPath] = append(harness.platform.metadata[socketTestPath], socketTestMetadataResult{metadata: final})
			}
			owner, err := openAdminSocket(context.Background(), harness.config())
			if owner != nil || err == nil {
				t.Fatalf("%s failure = (%v, %v), want error", phase, owner, err)
			}
			if len(harness.platform.unlinkCalls) != 1 || harness.platform.unlinkCalls[0].identity != provisional.Identity() {
				t.Fatalf("%s cleanup unlinks = %+v, want captured %+v", phase, harness.platform.unlinkCalls, provisional.Identity())
			}
		})
	}

	for _, phase := range []string{"missing", "replacement", "wrong type"} {
		t.Run("cleanup preserves "+phase, func(t *testing.T) {
			harness := newSocketTestHarness()
			provisional, _, _ := configureSocketTestPublication(harness, 560)
			harness.platform.chownErr = errors.New("publication failed")
			cleanup := socketTestMetadataResult{metadata: provisional}
			switch phase {
			case "missing":
				cleanup = socketTestMetadataResult{err: fs.ErrNotExist}
			case "replacement":
				cleanup.metadata.Inode++
			case "wrong type":
				cleanup.metadata.Type = pathTypeRegular
			}
			harness.platform.metadata[socketTestPath] = []socketTestMetadataResult{
				{err: fs.ErrNotExist}, {metadata: provisional}, {metadata: provisional}, {metadata: provisional}, cleanup,
			}
			owner, err := openAdminSocket(context.Background(), harness.config())
			if owner != nil || err == nil {
				t.Fatalf("cleanup %s = (%v, %v), want failure", phase, owner, err)
			}
			if len(harness.platform.unlinkCalls) != 0 {
				t.Fatalf("cleanup %s removed replacement: %+v", phase, harness.platform.unlinkCalls)
			}
		})
	}
}

func configureSocketTestPublication(harness *socketTestHarness, inode uint64) (pathMetadata, pathMetadata, pathMetadata) {
	provisional := socketTestSocketMetadata(inode, 0, 0o700)
	chowned := provisional
	chowned.GID = 80
	final := chowned
	final.Permissions = 0o660
	harness.listener = &socketTestListener{platform: harness.platform, unlinkOnClose: true}
	harness.platform.listener = harness.listener
	harness.platform.setMetadata(socketTestPath, []socketTestMetadataResult{
		{err: fs.ErrNotExist},
		{metadata: provisional}, {metadata: provisional}, {metadata: provisional},
		{metadata: chowned},
		{metadata: final}, {metadata: final}, {metadata: final},
	})
	harness.platform.afterUnlink = map[string][]socketTestMetadataResult{socketTestPath: {{err: fs.ErrNotExist}}}
	return provisional, chowned, final
}

func TestAdminSocketCloseClosesListenerBeforePathInspection(t *testing.T) {
	harness := newSocketTestHarness()
	_, _, final := configureSocketTestPublication(harness, 600)
	owner, err := openAdminSocket(context.Background(), harness.config())
	if err != nil {
		t.Fatalf("openAdminSocket: %v", err)
	}
	harness.platform.setMetadata(socketTestPath, []socketTestMetadataResult{{metadata: final}})
	start := len(harness.platform.operations())
	if err := owner.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	operations := harness.platform.operations()[start:]
	closeIndex := socketTestOperationIndex(operations, "listener-close")
	inspectIndex := socketTestOperationIndex(operations, "lstat:"+socketTestPath)
	if closeIndex < 0 || inspectIndex <= closeIndex {
		t.Fatalf("close order = %v", operations)
	}
}

func TestAdminSocketCloseRemovesOnlyCapturedIdentity(t *testing.T) {
	harness := newSocketTestHarness()
	provisional, _, final := configureSocketTestPublication(harness, 610)
	owner, err := openAdminSocket(context.Background(), harness.config())
	if err != nil {
		t.Fatalf("openAdminSocket: %v", err)
	}
	harness.platform.setMetadata(socketTestPath, []socketTestMetadataResult{{metadata: final}})
	if err := owner.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(harness.platform.unlinkCalls) != 1 || harness.platform.unlinkCalls[0].identity != provisional.Identity() {
		t.Fatalf("Close unlinks = %+v, want captured identity %+v", harness.platform.unlinkCalls, provisional.Identity())
	}
}

func TestAdminSocketCloseRejectsMissingChangedOrWrongTypePath(t *testing.T) {
	for _, test := range []struct {
		name   string
		result func(pathMetadata) socketTestMetadataResult
	}{
		{name: "missing", result: func(pathMetadata) socketTestMetadataResult { return socketTestMetadataResult{err: fs.ErrNotExist} }},
		{name: "changed", result: func(metadata pathMetadata) socketTestMetadataResult {
			metadata.Inode++
			return socketTestMetadataResult{metadata: metadata}
		}},
		{name: "wrong type", result: func(metadata pathMetadata) socketTestMetadataResult {
			metadata.Type = pathTypeRegular
			return socketTestMetadataResult{metadata: metadata}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newSocketTestHarness()
			_, _, final := configureSocketTestPublication(harness, 620)
			owner, err := openAdminSocket(context.Background(), harness.config())
			if err != nil {
				t.Fatalf("openAdminSocket: %v", err)
			}
			harness.platform.setMetadata(socketTestPath, []socketTestMetadataResult{test.result(final)})
			if err := owner.Close(); err == nil {
				t.Fatal("Close returned nil for uncertain cleanup")
			}
			if len(harness.platform.unlinkCalls) != 0 {
				t.Fatalf("Close removed %s path: %+v", test.name, harness.platform.unlinkCalls)
			}
		})
	}
}

func TestAdminSocketListenerCloseCannotUnlinkReplacement(t *testing.T) {
	harness := newSocketTestHarness()
	_, _, final := configureSocketTestPublication(harness, 630)
	owner, err := openAdminSocket(context.Background(), harness.config())
	if err != nil {
		t.Fatalf("openAdminSocket: %v", err)
	}
	replacement := final
	replacement.Inode++
	harness.platform.setMetadata(socketTestPath, []socketTestMetadataResult{{metadata: replacement}})
	if err := owner.Listener().Close(); err != nil {
		t.Fatalf("raw listener Close: %v", err)
	}
	if socketTestOperationCount(harness.platform.operations(), "listener-automatic-unlink") != 0 {
		t.Fatalf("raw listener close automatically unlinked replacement: %v", harness.platform.operations())
	}
	if err := owner.Close(); err == nil {
		t.Fatal("owner Close accepted replacement")
	}
	if len(harness.platform.unlinkCalls) != 0 {
		t.Fatalf("owner Close removed replacement: %+v", harness.platform.unlinkCalls)
	}
}

func TestAdminSocketCloseUnlocksOnlyAfterCleanup(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		harness := newSocketTestHarness()
		_, _, final := configureSocketTestPublication(harness, 640)
		owner, err := openAdminSocket(context.Background(), harness.config())
		if err != nil {
			t.Fatalf("openAdminSocket: %v", err)
		}
		if replacement {
			final.Inode++
		}
		harness.platform.setMetadata(socketTestPath, []socketTestMetadataResult{{metadata: final}})
		start := len(harness.platform.operations())
		_ = owner.Close()
		operations := harness.platform.operations()[start:]
		cleanupIndex := socketTestOperationIndex(operations, "lstat:"+socketTestPath)
		unlockIndex := socketTestOperationIndex(operations, "lock-unlock")
		closeIndex := socketTestOperationIndex(operations, "lock-close")
		if cleanupIndex < 0 || unlockIndex <= cleanupIndex || closeIndex <= unlockIndex {
			t.Fatalf("replacement=%t cleanup/lock order = %v", replacement, operations)
		}
	}
}

func TestAdminSocketCloseJoinsListenerUnlinkUnlockAndDescriptorErrors(t *testing.T) {
	t.Run("listener unlock descriptor", func(t *testing.T) {
		harness := newSocketTestHarness()
		_, _, final := configureSocketTestPublication(harness, 650)
		owner, err := openAdminSocket(context.Background(), harness.config())
		if err != nil {
			t.Fatalf("openAdminSocket: %v", err)
		}
		harness.platform.setMetadata(socketTestPath, []socketTestMetadataResult{{metadata: final}})
		listenerErr := errors.New("listener close failed")
		unlockErr := errors.New("unlock failed")
		descriptorErr := errors.New("descriptor close failed")
		harness.listener.closeErrors = []error{listenerErr}
		harness.lock.unlockErr = unlockErr
		harness.lock.closeErr = descriptorErr
		closeErr := owner.Close()
		for _, want := range []error{listenerErr, unlockErr, descriptorErr} {
			if !errors.Is(closeErr, want) {
				t.Fatalf("Close error %v does not preserve %v", closeErr, want)
			}
		}
		if len(harness.platform.unlinkCalls) != 0 {
			t.Fatalf("listener close failure still unlinked: %+v", harness.platform.unlinkCalls)
		}
	})

	t.Run("unlink unlock descriptor", func(t *testing.T) {
		harness := newSocketTestHarness()
		_, _, final := configureSocketTestPublication(harness, 651)
		owner, err := openAdminSocket(context.Background(), harness.config())
		if err != nil {
			t.Fatalf("openAdminSocket: %v", err)
		}
		harness.platform.setMetadata(socketTestPath, []socketTestMetadataResult{{metadata: final}})
		unlinkErr := errors.New("unlink failed")
		unlockErr := errors.New("unlock failed")
		descriptorErr := errors.New("descriptor close failed")
		harness.platform.unlinkErr = unlinkErr
		harness.lock.unlockErr = unlockErr
		harness.lock.closeErr = descriptorErr
		closeErr := owner.Close()
		for _, want := range []error{unlinkErr, unlockErr, descriptorErr} {
			if !errors.Is(closeErr, want) {
				t.Fatalf("Close error %v does not preserve %v", closeErr, want)
			}
		}
	})
}

func TestAdminSocketConcurrentCloseRunsLifecycleOnce(t *testing.T) {
	harness := newSocketTestHarness()
	_, _, final := configureSocketTestPublication(harness, 660)
	owner, err := openAdminSocket(context.Background(), harness.config())
	if err != nil {
		t.Fatalf("openAdminSocket: %v", err)
	}
	harness.platform.setMetadata(socketTestPath, []socketTestMetadataResult{{metadata: final}})
	closeSentinel := errors.New("descriptor close sentinel")
	harness.lock.closeErr = closeSentinel

	const callers = 64
	start := make(chan struct{})
	results := make(chan error, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer wait.Done()
			<-start
			results <- owner.Close()
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var first error
	for result := range results {
		if first == nil {
			first = result
		}
		if result != first || !errors.Is(result, closeSentinel) {
			t.Fatalf("concurrent Close result %v differs from cached %v", result, first)
		}
	}
	operations := harness.platform.operations()
	if harness.listener.closeCalls != 1 || len(harness.platform.unlinkCalls) != 1 ||
		socketTestOperationCount(operations, "lock-unlock") != 1 || socketTestOperationCount(operations, "lock-close") != 1 {
		t.Fatalf("concurrent Close lifecycle repeated: listener=%d unlinks=%+v operations=%v", harness.listener.closeCalls, harness.platform.unlinkCalls, operations)
	}
}

func socketTestOperationIndexAfter(operations []string, want string, start int) int {
	for index := start; index < len(operations); index++ {
		if operations[index] == want {
			return index
		}
	}
	return -1
}

func assertRejectedSocketTestLifecycle(t *testing.T, harness *socketTestHarness) {
	t.Helper()
	operations := harness.platform.operations()
	if socketTestOperationCount(operations, "unlink:"+socketTestPath) != 0 || socketTestOperationCount(operations, "listen:"+socketTestPath) != 0 {
		t.Fatalf("rejected predecessor was mutated or bound: %v", operations)
	}
	if socketTestOperationCount(operations, "lock-unlock") != 1 || socketTestOperationCount(operations, "lock-close") != 1 {
		t.Fatalf("rejected predecessor did not release lock exactly once: %v", operations)
	}
	assertSocketTestLockNotUnlinked(t, -1, operations)
}

func socketTestSocketMetadata(inode uint64, gid uint32, mode uint16) pathMetadata {
	return pathMetadata{Device: 10, Inode: inode, UID: 0, GID: gid, Links: 1, RawType: 0o140000, Type: pathTypeSocket, Permissions: mode}
}

func repeatSocketTestMetadata(metadata pathMetadata, count int) []socketTestMetadataResult {
	results := make([]socketTestMetadataResult, count)
	for index := range results {
		results[index] = socketTestMetadataResult{metadata: metadata}
	}
	return results
}

func socketTestOperationIndex(operations []string, want string) int {
	for index, operation := range operations {
		if operation == want {
			return index
		}
	}
	return -1
}

func assertSocketTestLockOpens(t *testing.T, got, want []socketTestLockOpen) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("lock opens = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("lock opens = %+v, want %+v", got, want)
		}
	}
}

func assertSocketTestLockNotUnlinked(t *testing.T, scenario int, operations []string) {
	t.Helper()
	for _, operation := range operations {
		if operation == "unlink:"+socketTestLockPath {
			t.Fatalf("scenario %d unlinked persistent lock: %v", scenario, operations)
		}
	}
}

func filterSocketTestOperations(operations []string, include func(string) bool) []string {
	var filtered []string
	for _, operation := range operations {
		if include(operation) {
			filtered = append(filtered, operation)
		}
	}
	return filtered
}

func socketTestOperationCount(operations []string, want string) int {
	count := 0
	for _, operation := range operations {
		if operation == want {
			count++
		}
	}
	return count
}

type socketTestHarness struct {
	platform *socketTestPlatform
	acl      *socketTestACL
	lock     *socketTestLock
	listener *socketTestListener
}

func newSocketTestHarness() *socketTestHarness {
	platform := &socketTestPlatform{
		canonicalParent: "/test/run",
		metadata: map[string][]socketTestMetadataResult{
			"/":         {{metadata: socketTestDirectoryMetadata(1)}},
			"/test":     {{metadata: socketTestDirectoryMetadata(2)}},
			"/test/run": {{metadata: socketTestDirectoryMetadata(3)}},
			socketTestLockPath: {
				{metadata: socketTestLockMetadata()},
				{metadata: socketTestLockMetadata()},
			},
		},
	}
	lock := &socketTestLock{
		platform: platform,
		fstatResults: []socketTestMetadataResult{
			{metadata: socketTestLockMetadata()},
			{metadata: socketTestLockMetadata()},
		},
	}
	platform.defaultLock = lock
	acl := &socketTestACL{platform: platform, pathErrors: make(map[string][]error)}
	return &socketTestHarness{platform: platform, acl: acl, lock: lock}
}

func (harness *socketTestHarness) config() adminSocketConfig {
	return adminSocketConfig{
		SocketPath:         socketTestPath,
		LockPath:           socketTestLockPath,
		LexicalParent:      "/test/run",
		CanonicalParent:    "/test/run",
		CanonicalAncestors: []string{"/", "/test", "/test/run"},
		AdminGID:           80,
		Platform:           harness.platform,
		ACL:                harness.acl,
	}
}

func socketTestDirectoryMetadata(inode uint64) pathMetadata {
	return pathMetadata{Device: 1, Inode: inode, UID: 0, GID: 0, Links: 1, RawType: 0o040000, Type: pathTypeDirectory, Permissions: 0o755}
}

func socketTestLockMetadata() pathMetadata {
	return pathMetadata{Device: 9, Inode: 90, UID: 0, GID: 0, Links: 1, RawType: 0o100000, Type: pathTypeRegular, Permissions: 0o600}
}

type socketTestMetadataResult struct {
	metadata pathMetadata
	err      error
}

type socketTestLockOpen struct {
	path        string
	disposition lockOpenDisposition
	mode        uint16
}

type socketTestOpenLockResult struct {
	lock adminLock
	err  error
}

type socketTestUnlinkCall struct {
	path     string
	identity pathIdentity
}

type socketTestPlatform struct {
	mu              sync.Mutex
	log             []string
	canonicalParent string
	canonicalErr    error
	metadata        map[string][]socketTestMetadataResult
	metadataCalls   map[string]int
	lockOpens       []socketTestLockOpen
	openLockResults []socketTestOpenLockResult
	defaultLock     adminLock
	listenErr       error
	listener        adminUnixListener
	chownErr        error
	chmodErr        error
	unlinkErr       error
	unlinkCalls     []socketTestUnlinkCall
	afterUnlink     map[string][]socketTestMetadataResult
}

func (platform *socketTestPlatform) record(operation string) {
	platform.mu.Lock()
	defer platform.mu.Unlock()
	platform.log = append(platform.log, operation)
}

func (platform *socketTestPlatform) operations() []string {
	platform.mu.Lock()
	defer platform.mu.Unlock()
	return append([]string(nil), platform.log...)
}

func (platform *socketTestPlatform) setMetadata(path string, results []socketTestMetadataResult) {
	platform.mu.Lock()
	defer platform.mu.Unlock()
	platform.metadata[path] = results
	if platform.metadataCalls == nil {
		platform.metadataCalls = make(map[string]int)
	}
	platform.metadataCalls[path] = 0
}

func (platform *socketTestPlatform) CanonicalParent(_ context.Context, lexical string) (string, error) {
	platform.record("canonical:" + lexical)
	return platform.canonicalParent, platform.canonicalErr
}

func (platform *socketTestPlatform) Lstat(_ context.Context, path string) (pathMetadata, error) {
	platform.record("lstat:" + path)
	platform.mu.Lock()
	defer platform.mu.Unlock()
	if platform.metadataCalls == nil {
		platform.metadataCalls = make(map[string]int)
	}
	results := platform.metadata[path]
	index := platform.metadataCalls[path]
	platform.metadataCalls[path]++
	if len(results) == 0 {
		return pathMetadata{}, fs.ErrNotExist
	}
	if index >= len(results) {
		index = len(results) - 1
	}
	return results[index].metadata, results[index].err
}

func (platform *socketTestPlatform) OpenLock(_ context.Context, path string, disposition lockOpenDisposition, mode uint16) (adminLock, error) {
	platform.record("open-lock:" + path)
	platform.mu.Lock()
	defer platform.mu.Unlock()
	platform.lockOpens = append(platform.lockOpens, socketTestLockOpen{path: path, disposition: disposition, mode: mode})
	index := len(platform.lockOpens) - 1
	if index < len(platform.openLockResults) {
		return platform.openLockResults[index].lock, platform.openLockResults[index].err
	}
	return platform.defaultLock, nil
}

func (platform *socketTestPlatform) ListenUnix(_ context.Context, path string) (adminUnixListener, error) {
	platform.record("listen:" + path)
	return platform.listener, platform.listenErr
}

func (platform *socketTestPlatform) ChownNoFollow(_ context.Context, path string, _ pathIdentity, uid, gid uint32) error {
	platform.record("chown:" + path)
	return platform.chownErr
}

func (platform *socketTestPlatform) ChmodNoFollow(_ context.Context, path string, _ pathIdentity, mode uint16) error {
	platform.record("chmod:" + path)
	return platform.chmodErr
}

func (platform *socketTestPlatform) Unlink(_ context.Context, path string, identity pathIdentity) error {
	platform.record("unlink:" + path)
	platform.mu.Lock()
	defer platform.mu.Unlock()
	platform.unlinkCalls = append(platform.unlinkCalls, socketTestUnlinkCall{path: path, identity: identity})
	if platform.unlinkErr != nil {
		return platform.unlinkErr
	}
	if replacement, ok := platform.afterUnlink[path]; ok {
		platform.metadata[path] = replacement
		if platform.metadataCalls == nil {
			platform.metadataCalls = make(map[string]int)
		}
		platform.metadataCalls[path] = 0
	}
	return nil
}

type socketTestLock struct {
	platform     *socketTestPlatform
	mu           sync.Mutex
	fstatResults []socketTestMetadataResult
	fstatCalls   int
	tryErr       error
	unlockErr    error
	closeErr     error
}

func (lock *socketTestLock) Fstat(context.Context) (pathMetadata, error) {
	lock.platform.record("lock-fstat")
	lock.mu.Lock()
	defer lock.mu.Unlock()
	index := lock.fstatCalls
	lock.fstatCalls++
	if len(lock.fstatResults) == 0 {
		return pathMetadata{}, errors.New("missing lock fstat result")
	}
	if index >= len(lock.fstatResults) {
		index = len(lock.fstatResults) - 1
	}
	return lock.fstatResults[index].metadata, lock.fstatResults[index].err
}

func (lock *socketTestLock) TryExclusive() error {
	lock.platform.record("lock-exclusive")
	return lock.tryErr
}

func (lock *socketTestLock) Unlock() error {
	lock.platform.record("lock-unlock")
	return lock.unlockErr
}

func (lock *socketTestLock) Close() error {
	lock.platform.record("lock-close")
	return lock.closeErr
}

type socketTestACL struct {
	platform   *socketTestPlatform
	pathErrors map[string][]error
	pathCalls  map[string]int
}

func (*socketTestACL) Validate(context.Context, openedPath, pathACLPolicy) error { return nil }

func (acl *socketTestACL) ValidatePath(_ context.Context, path string, policy pathACLPolicy) error {
	policyName := "unknown"
	switch policy {
	case pathACLRejectExtended:
		policyName = "reject-extended"
	case pathACLRejectNonRootMutation:
		policyName = "reject-nonroot-mutation"
	}
	acl.platform.record("acl-path:" + path + ":" + policyName)
	if acl.pathCalls == nil {
		acl.pathCalls = make(map[string]int)
	}
	index := acl.pathCalls[path]
	acl.pathCalls[path]++
	errorsForPath := acl.pathErrors[path]
	if len(errorsForPath) == 0 {
		return nil
	}
	if index >= len(errorsForPath) {
		index = len(errorsForPath) - 1
	}
	return errorsForPath[index]
}

type socketTestListener struct {
	platform      *socketTestPlatform
	mu            sync.Mutex
	closeErr      error
	closeErrors   []error
	closeCalls    int
	unlinkOnClose bool
}

func (*socketTestListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }

func (listener *socketTestListener) Close() error {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	listener.closeCalls++
	listener.platform.record("listener-close")
	if listener.unlinkOnClose {
		listener.platform.record("listener-automatic-unlink")
	}
	if listener.closeCalls <= len(listener.closeErrors) {
		return listener.closeErrors[listener.closeCalls-1]
	}
	if listener.closeCalls > 1 {
		return net.ErrClosed
	}
	return listener.closeErr
}

func (*socketTestListener) Addr() net.Addr { return socketTestAddr("admin") }

func (listener *socketTestListener) SetUnlinkOnClose(enabled bool) {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	listener.unlinkOnClose = enabled
	listener.platform.record("listener-auto-unlink:" + map[bool]string{true: "true", false: "false"}[enabled])
}

type socketTestAddr string

func (socketTestAddr) Network() string        { return "unix" }
func (address socketTestAddr) String() string { return string(address) }

var _ adminSocketPlatform = (*socketTestPlatform)(nil)
var _ adminLock = (*socketTestLock)(nil)
var _ pathACLInspector = (*socketTestACL)(nil)
var _ adminUnixListener = (*socketTestListener)(nil)
