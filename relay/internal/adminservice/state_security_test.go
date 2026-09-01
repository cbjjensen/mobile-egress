package adminservice

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

const (
	testSetupID    = "0123456789abcdef0123456789abcdef"
	testRotationID = "abcdef0123456789abcdef0123456789"
)

var testBaseStateNames = []string{
	"ca.crt",
	"ca.key",
	"relay.crt",
	"relay.key",
	"state.db",
	"state.db-wal",
	"state.db-shm",
}

var testRotationSuffixes = []string{
	".crt.new",
	".key.new",
	".crt.old",
	".key.old",
}

func TestStateGuardAcceptsFreshEmptyProduct(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	if err := fixture.guard.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	fixture.assertNoMutations(t)
}

func TestStateGuardAcceptsEachPartialCanonicalSetupStage(t *testing.T) {
	t.Parallel()

	for subset := 0; subset < 1<<len(testBaseStateNames); subset++ {
		fixture := newStateGuardFixture(t)
		stage := filepath.Join(fixture.product, ".relay-setup-"+testSetupID)
		fixture.filesystem.addDirectory(stage, 0o700)
		for index, name := range testBaseStateNames {
			if subset&(1<<index) != 0 {
				fixture.filesystem.addFile(filepath.Join(stage, name), 0o600)
			}
		}
		if err := fixture.guard.Validate(context.Background()); err != nil {
			t.Fatalf("Validate(partial setup subset %07b) error = %v", subset, err)
		}
		fixture.assertNoMutations(t)
	}
}

func TestStateGuardRejectsWorldReadableSetupStageCertificates(t *testing.T) {
	for _, name := range []string{"ca.crt", "relay.crt"} {
		t.Run(name, func(t *testing.T) {
			fixture := newStateGuardFixture(t)
			stage := filepath.Join(fixture.product, ".relay-setup-"+testSetupID)
			fixture.filesystem.addDirectory(stage, 0o700)
			fixture.filesystem.addFile(filepath.Join(stage, name), 0o644)
			fixture.filesystem.operations = nil

			if err := fixture.guard.Validate(context.Background()); err == nil {
				t.Fatalf("Validate() accepted setup-stage %s mode 0644", name)
			}
			fixture.assertNoMutations(t)
		})
	}
}

func TestStateGuardAcceptsSafeReadyStateAndSQLiteSidecars(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.addReadyState(testBaseStateNames...)
	if err := fixture.guard.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	fixture.assertNoMutations(t)
}

func TestStateGuardAcceptsOneCanonicalRotationArtifactSet(t *testing.T) {
	t.Parallel()

	for subset := 1; subset < 1<<len(testRotationSuffixes); subset++ {
		fixture := newStateGuardFixture(t)
		fixture.addReadyState(testBaseStateNames...)
		for index, suffix := range testRotationSuffixes {
			if subset&(1<<index) != 0 {
				fixture.filesystem.addFile(
					filepath.Join(fixture.state, ".relay-rotate-"+testRotationID+suffix),
					0o600,
				)
			}
		}
		if err := fixture.guard.Validate(context.Background()); err != nil {
			t.Fatalf("Validate(rotation subset %04b) error = %v", subset, err)
		}
		fixture.assertNoMutations(t)
	}
}

func TestStateGuardRejectsUnknownProductAndStateEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*stateGuardFixture)
	}{
		{
			name: "unknown product file",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.addFile(filepath.Join(fixture.product, "notes.txt"), 0o600)
			},
		},
		{
			name: "unknown product directory",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.addDirectory(filepath.Join(fixture.product, "relay-backup"), 0o700)
			},
		},
		{
			name: "unknown state file",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState(testBaseStateNames...)
				fixture.filesystem.addFile(filepath.Join(fixture.state, "owner.key"), 0o600)
			},
		},
		{
			name: "case varied state file",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState(testBaseStateNames...)
				fixture.filesystem.remove(filepath.Join(fixture.state, "ca.crt"))
				fixture.filesystem.addFile(filepath.Join(fixture.state, "CA.crt"), 0o600)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newStateGuardFixture(t)
			test.mutate(fixture)
			if err := fixture.guard.Validate(context.Background()); err == nil {
				t.Fatal("Validate() error = nil, want fail-closed rejection")
			}
			fixture.assertNoMutations(t)
		})
	}
}

func TestStateGuardRejectsMalformedCaseVariedAndMultipleRequestIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*stateGuardFixture)
	}{
		{
			name: "short setup request ID",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.addDirectory(filepath.Join(fixture.product, ".relay-setup-abc"), 0o700)
			},
		},
		{
			name: "uppercase setup request ID",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.addDirectory(filepath.Join(fixture.product, ".relay-setup-0123456789ABCDEF0123456789ABCDEF"), 0o700)
			},
		},
		{
			name: "two setup request IDs",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.addDirectory(filepath.Join(fixture.product, ".relay-setup-"+testSetupID), 0o700)
				fixture.filesystem.addDirectory(filepath.Join(fixture.product, ".relay-setup-11111111111111111111111111111111"), 0o700)
			},
		},
		{
			name: "uppercase rotation request ID",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState(testBaseStateNames...)
				fixture.filesystem.addFile(filepath.Join(fixture.state, ".relay-rotate-ABCDEF0123456789ABCDEF0123456789.crt.new"), 0o600)
			},
		},
		{
			name: "two rotation request IDs",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState(testBaseStateNames...)
				fixture.filesystem.addFile(filepath.Join(fixture.state, ".relay-rotate-"+testRotationID+".crt.new"), 0o600)
				fixture.filesystem.addFile(filepath.Join(fixture.state, ".relay-rotate-11111111111111111111111111111111.key.old"), 0o600)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newStateGuardFixture(t)
			test.mutate(fixture)
			if err := fixture.guard.Validate(context.Background()); err == nil {
				t.Fatal("Validate() error = nil, want fail-closed rejection")
			}
			fixture.assertNoMutations(t)
		})
	}
}

func TestStateGuardRejectsSymlinkWrongTypeWrongOwnerAndHardLink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*stateGuardFixture)
	}{
		{
			name: "product symlink",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.product] = fixture.filesystem.newMetadata(pathTypeSymlink, 0o700)
			},
		},
		{
			name: "Relay regular file",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.addFile(fixture.state, 0o600)
			},
		},
		{
			name: "setup stage symlink",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.addObject(filepath.Join(fixture.product, ".relay-setup-"+testSetupID), pathTypeSymlink, 0o700)
			},
		},
		{
			name: "state file directory",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("ca.crt")
				fixture.filesystem.metadata[filepath.Join(fixture.state, "ca.crt")].Type = pathTypeDirectory
			},
		},
		{
			name: "wrong product owner",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.product].UID = 501
			},
		},
		{
			name: "wrong state file owner",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("ca.key")
				fixture.filesystem.metadata[filepath.Join(fixture.state, "ca.key")].UID = 501
			},
		},
		{
			name: "hard-linked state file",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("state.db")
				fixture.filesystem.metadata[filepath.Join(fixture.state, "state.db")].Links = 2
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newStateGuardFixture(t)
			test.mutate(fixture)
			if err := fixture.guard.Validate(context.Background()); err == nil {
				t.Fatal("Validate() error = nil, want fail-closed rejection")
			}
			fixture.assertNoMutations(t)
		})
	}
}

func TestStateGuardRejectsUnsafeDirectoryAndFileModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*stateGuardFixture)
	}{
		{
			name: "product is not exact 0700",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.product].Permissions = 0o750
			},
		},
		{
			name: "Relay is not exact 0700",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("ca.crt")
				fixture.filesystem.metadata[fixture.state].Permissions = 0o755
			},
		},
		{
			name: "trusted ancestor is group writable",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.ancestors[1]].Permissions = 0o775
			},
		},
		{
			name: "certificate has noncanonical mode",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("ca.crt")
				fixture.filesystem.metadata[filepath.Join(fixture.state, "ca.crt")].Permissions = 0o640
			},
		},
		{
			name: "private key is group readable",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("ca.key")
				fixture.filesystem.metadata[filepath.Join(fixture.state, "ca.key")].Permissions = 0o640
			},
		},
		{
			name: "database is group writable",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("state.db")
				fixture.filesystem.metadata[filepath.Join(fixture.state, "state.db")].Permissions = 0o620
			},
		},
		{
			name: "artifact is executable",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState(testBaseStateNames...)
				path := filepath.Join(fixture.state, ".relay-rotate-"+testRotationID+".key.new")
				fixture.filesystem.addFile(path, 0o700)
			},
		},
		{
			name: "file has set-ID bit",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("relay.key")
				fixture.filesystem.metadata[filepath.Join(fixture.state, "relay.key")].Permissions = 0o4600
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newStateGuardFixture(t)
			test.mutate(fixture)
			if err := fixture.guard.Validate(context.Background()); err == nil {
				t.Fatal("Validate() error = nil, want fail-closed rejection")
			}
			fixture.assertNoMutations(t)
		})
	}
}

func TestStateGuardRejectsRelayAndSetupStageTogether(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.addReadyState(testBaseStateNames...)
	fixture.filesystem.addDirectory(filepath.Join(fixture.product, ".relay-setup-"+testSetupID), 0o700)
	if err := fixture.guard.Validate(context.Background()); err == nil {
		t.Fatal("Validate() error = nil, want fail-closed rejection")
	}
	fixture.assertNoMutations(t)
}

func TestStateGuardChecksContextDuringInventory(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.addReadyState(testBaseStateNames...)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.filesystem.afterObservation = func(count int) {
		if count == 5 {
			cancel()
		}
	}
	if err := fixture.guard.Validate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Validate() error = %v, want context.Canceled", err)
	}
	if got, total := fixture.filesystem.observations, len(fixture.filesystem.metadata)+2; got >= total {
		t.Fatalf("Validate() performed %d observations after cancellation, want fewer than %d", got, total)
	}
	fixture.assertNoMutations(t)
}

func TestStateGuardPrepareCreatesOnlyMissingProductWith0700(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.filesystem.remove(fixture.product)
	fixture.filesystem.operations = nil
	if err := fixture.guard.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	mutations := fixture.filesystem.mutations()
	want := []stateFilesystemOperation{{Name: "mkdir", Path: fixture.product, Mode: 0o700}}
	if !reflectStateOperationsEqual(mutations, want) {
		t.Fatalf("Prepare() mutations = %#v, want %#v", mutations, want)
	}
	product, ok := fixture.filesystem.metadata[fixture.product]
	if !ok || product.Type != pathTypeDirectory || product.UID != 0 || product.Permissions != 0o700 {
		t.Fatalf("created product metadata = %#v, want root-owned directory 0700", product)
	}
	if _, exists := fixture.filesystem.metadata[fixture.state]; exists {
		t.Fatal("Prepare() created Relay")
	}
}

func TestStateGuardPrepareRejectsUnsafeNearestAncestor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*stateGuardFixture)
	}{
		{
			name: "missing nearest ancestor",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.remove(fixture.ancestors[len(fixture.ancestors)-1])
			},
		},
		{
			name: "wrong owner",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.ancestors[len(fixture.ancestors)-1]].UID = 501
			},
		},
		{
			name: "group writable",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.ancestors[len(fixture.ancestors)-1]].Permissions = 0o775
			},
		},
		{
			name: "symlink",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.ancestors[len(fixture.ancestors)-1]].Type = pathTypeSymlink
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newStateGuardFixture(t)
			fixture.filesystem.remove(fixture.product)
			test.mutate(fixture)
			fixture.filesystem.operations = nil
			if err := fixture.guard.Prepare(context.Background()); err == nil {
				t.Fatal("Prepare() error = nil, want fail-closed rejection")
			}
			fixture.assertNoMutations(t)
		})
	}
}

func TestStateGuardPrepareNeverCreatesRelayOrUsesMkdirAll(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.filesystem.remove(fixture.product)
	fixture.filesystem.operations = nil
	if err := fixture.guard.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	for _, operation := range fixture.filesystem.operations {
		if operation.Name == "mkdir" && operation.Path != fixture.product {
			t.Fatalf("Prepare() created unexpected directory %q", operation.Path)
		}
		if operation.Name == "mkdir-all" {
			t.Fatal("Prepare() used recursive directory creation")
		}
	}
	if _, exists := fixture.filesystem.metadata[fixture.state]; exists {
		t.Fatal("Prepare() created Relay")
	}
}

func TestStateGuardRepairTightensOnlyVerifiedKnownObjects(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.filesystem.metadata[fixture.product].Permissions = 0o755
	fixture.addReadyState("ca.crt", "ca.key", "state.db")
	fixture.filesystem.metadata[fixture.state].Permissions = 0o711
	fixture.filesystem.metadata[filepath.Join(fixture.state, "ca.crt")].Permissions = 0o644
	fixture.filesystem.metadata[filepath.Join(fixture.state, "ca.key")].Permissions = 0o640
	fixture.filesystem.metadata[filepath.Join(fixture.state, "state.db")].Permissions = 0o400
	fixture.filesystem.operations = nil

	if err := fixture.guard.Repair(context.Background()); err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	want := []stateFilesystemOperation{
		{Name: "chmod", Path: fixture.product, Mode: 0o700},
		{Name: "chmod", Path: fixture.state, Mode: 0o700},
		{Name: "chmod", Path: filepath.Join(fixture.state, "ca.crt"), Mode: 0o600},
		{Name: "chmod", Path: filepath.Join(fixture.state, "ca.key"), Mode: 0o600},
		{Name: "chmod", Path: filepath.Join(fixture.state, "state.db"), Mode: 0o600},
	}
	if got := fixture.filesystem.mutations(); !reflectStateOperationsEqual(got, want) {
		t.Fatalf("Repair() mutations = %#v, want %#v", got, want)
	}
	firstMutation := firstStateMutationIndex(fixture.filesystem.operations)
	for _, path := range []string{
		fixture.product,
		fixture.state,
		filepath.Join(fixture.state, "ca.crt"),
		filepath.Join(fixture.state, "ca.key"),
		filepath.Join(fixture.state, "state.db"),
	} {
		if !statePathObservedBefore(fixture.filesystem.operations, path, firstMutation) {
			t.Fatalf("Repair() mutated before complete inventory of %q; operations = %#v", path, fixture.filesystem.operations)
		}
	}
	if err := fixture.guard.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() after Repair error = %v", err)
	}
}

func TestStateGuardRepairRejectsWritableTreeBeforeMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*stateGuardFixture)
	}{
		{
			name: "product group writable",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.product].Permissions = 0o770
			},
		},
		{
			name: "Relay other writable",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.state].Permissions = 0o707
			},
		},
		{
			name: "file group writable",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[filepath.Join(fixture.state, "state.db")].Permissions = 0o620
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newStateGuardFixture(t)
			fixture.addReadyState("state.db")
			fixture.filesystem.metadata[fixture.product].Permissions = 0o755
			test.mutate(fixture)
			fixture.filesystem.operations = nil
			if err := fixture.guard.Repair(context.Background()); err == nil {
				t.Fatal("Repair() error = nil, want fail-closed rejection")
			}
			fixture.assertNoMutations(t)
		})
	}
}

func TestStateGuardRepairRefusesDeviceInodeReplacement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inject func(*fakeStateFilesystem, string)
	}{
		{
			name: "replacement before descriptor chmod",
			inject: func(filesystem *fakeStateFilesystem, target string) {
				filesystem.beforeChmod = func(path string) {
					if path == target {
						replacement := filesystem.newMetadata(pathTypeRegular, 0o640)
						filesystem.metadata[path] = replacement
					}
				}
			},
		},
		{
			name: "replacement after descriptor chmod",
			inject: func(filesystem *fakeStateFilesystem, target string) {
				filesystem.afterChmod = func(path string) {
					if path == target {
						replacement := filesystem.newMetadata(pathTypeRegular, 0o600)
						filesystem.metadata[path] = replacement
					}
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newStateGuardFixture(t)
			fixture.addReadyState("state.db")
			target := filepath.Join(fixture.state, "state.db")
			fixture.filesystem.metadata[target].Permissions = 0o640
			test.inject(fixture.filesystem, target)
			fixture.filesystem.operations = nil
			if err := fixture.guard.Repair(context.Background()); !errors.Is(err, errStatePathChanged) {
				t.Fatalf("Repair() error = %v, want errStatePathChanged", err)
			}
		})
	}
}

func TestStateGuardDirectoryEnumerationDoesNotFollowValidatedPathReplacement(t *testing.T) {
	fixture := newStateGuardFixture(t)
	recorder := &recordingStateACLInspector{}
	fixture.guard.acl = recorder
	recorder.afterValidate = func(path string, policy pathACLPolicy) {
		if path != fixture.product || policy != pathACLRejectExtended {
			return
		}
		recorder.afterValidate = nil
		fixture.filesystem.metadata[path] = fixture.filesystem.newMetadata(pathTypeSymlink, 0o777)
		fixture.filesystem.children[path] = []string{"unadmitted-secret"}
	}
	fixture.filesystem.operations = nil

	if err := fixture.guard.Validate(context.Background()); err == nil {
		t.Fatal("Validate() error = nil after validated product was replaced by a symlink")
	}
	for _, operation := range fixture.filesystem.operations {
		if (operation.Name == "readdir" || operation.Name == "readdir-fd") && operation.Path == fixture.product {
			t.Fatalf("Validate() enumerated replacement through product pathname: %#v", fixture.filesystem.operations)
		}
	}
}

func TestStateGuardOpenUsesAdmittedMetadataAndRejectsSpecialReplacementBeforeUse(t *testing.T) {
	t.Run("directory replaced by FIFO", func(t *testing.T) {
		fixture := newStateGuardFixture(t)
		recorder := &recordingStateACLInspector{}
		fixture.guard.acl = recorder
		admitted := *fixture.filesystem.metadata[fixture.product]
		var gotExpected pathMetadata
		cutoff := -1
		fixture.filesystem.beforeOpen = func(path string, expected pathMetadata) {
			if path != fixture.product || cutoff >= 0 {
				return
			}
			gotExpected = expected
			replacement := fixture.filesystem.newMetadata(pathTypeOther, 0o600)
			replacement.RawType = 0o010000
			fixture.filesystem.metadata[path] = replacement
			cutoff = len(fixture.filesystem.operations)
		}
		fixture.filesystem.operations = nil

		if err := fixture.guard.Validate(context.Background()); !errors.Is(err, errStatePathChanged) {
			t.Fatalf("Validate() error = %v, want errStatePathChanged", err)
		}
		if gotExpected != admitted {
			t.Fatalf("Open() expected metadata = %#v, want admitted %#v", gotExpected, admitted)
		}
		assertNoOpenedPathUseAfter(t, fixture.filesystem.operations, cutoff, fixture.product)
		for _, observation := range recorder.observations {
			if observation.Path == fixture.product {
				t.Fatalf("Validate() inspected the replacement FIFO ACL: %#v", recorder.observations)
			}
		}
	})

	t.Run("regular file replaced by device before repair open", func(t *testing.T) {
		fixture := newStateGuardFixture(t)
		fixture.addReadyState("ca.key")
		target := filepath.Join(fixture.state, "ca.key")
		fixture.filesystem.metadata[target].Permissions = 0o400
		admitted := *fixture.filesystem.metadata[target]
		recorder := &recordingStateACLInspector{}
		fixture.guard.acl = recorder
		openCount := 0
		cutoff := -1
		aclCount := -1
		var gotExpected pathMetadata
		fixture.filesystem.beforeOpen = func(path string, expected pathMetadata) {
			if path != target {
				return
			}
			openCount++
			if openCount != 2 {
				return
			}
			gotExpected = expected
			replacement := fixture.filesystem.newMetadata(pathTypeOther, 0o600)
			replacement.RawType = 0o020000
			fixture.filesystem.metadata[path] = replacement
			cutoff = len(fixture.filesystem.operations)
			aclCount = len(recorder.observations)
		}
		fixture.filesystem.operations = nil

		if err := fixture.guard.Repair(context.Background()); !errors.Is(err, errStatePathChanged) {
			t.Fatalf("Repair() error = %v, want errStatePathChanged", err)
		}
		if openCount != 2 {
			t.Fatalf("target Open() count = %d, want 2", openCount)
		}
		if gotExpected != admitted {
			t.Fatalf("repair Open() expected metadata = %#v, want admitted %#v", gotExpected, admitted)
		}
		assertNoOpenedPathUseAfter(t, fixture.filesystem.operations, cutoff, target)
		if len(recorder.observations) != aclCount {
			t.Fatalf("Repair() inspected replacement device ACL: before=%d after=%#v", aclCount, recorder.observations)
		}
	})
}

func TestStateGuardRepairRefusesSameInodeMetadataAndACLRacesBeforeFchmod(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeStateFilesystem, *recordingStateACLInspector, string)
	}{
		{
			name: "link count",
			mutate: func(filesystem *fakeStateFilesystem, _ *recordingStateACLInspector, target string) {
				filesystem.metadata[target].Links = 2
			},
		},
		{
			name: "mode",
			mutate: func(filesystem *fakeStateFilesystem, _ *recordingStateACLInspector, target string) {
				filesystem.metadata[target].Permissions = 0o622
			},
		},
		{
			name: "extended ACL",
			mutate: func(_ *fakeStateFilesystem, inspector *recordingStateACLInspector, target string) {
				inspector.failPath = target
				inspector.failErr = errors.New("injected same-inode ACL race")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStateGuardFixture(t)
			fixture.addReadyState("ca.key")
			target := filepath.Join(fixture.state, "ca.key")
			fixture.filesystem.metadata[target].Permissions = 0o400
			inspector := &recordingStateACLInspector{}
			fixture.guard.acl = inspector
			fixture.filesystem.beforeChmod = func(path string) {
				if path == target {
					test.mutate(fixture.filesystem, inspector, target)
				}
			}
			fixture.filesystem.operations = nil

			if err := fixture.guard.Repair(context.Background()); err == nil {
				t.Fatal("Repair() error = nil after same-inode metadata/ACL race")
			}
			for _, operation := range fixture.filesystem.operations {
				if operation.Name == "chmod" && operation.Path == target {
					t.Fatalf("Repair() fchmodded an object changed after admission: %#v", fixture.filesystem.operations)
				}
			}
		})
	}
}

func TestStateGuardRepairNeverChownsOrDeletes(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.addReadyState("ca.crt", "ca.key", "state.db")
	fixture.filesystem.metadata[fixture.product].Permissions = 0o755
	fixture.filesystem.operations = nil
	owners := make(map[string]uint32, len(fixture.filesystem.metadata))
	for path, metadata := range fixture.filesystem.metadata {
		owners[path] = metadata.UID
	}
	if err := fixture.guard.Repair(context.Background()); err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	for _, operation := range fixture.filesystem.operations {
		if operation.Name != "lstat" && operation.Name != "open" && operation.Name != "fstat" &&
			operation.Name != "readdir-fd" && operation.Name != "chmod" {
			t.Fatalf("Repair() performed forbidden operation %#v", operation)
		}
	}
	for path, owner := range owners {
		metadata, exists := fixture.filesystem.metadata[path]
		if !exists {
			t.Fatalf("Repair() deleted %q", path)
		}
		if metadata.UID != owner {
			t.Fatalf("Repair() changed owner of %q from %d to %d", path, owner, metadata.UID)
		}
	}
}

func TestStateGuardRepairLeavesArtifactsForServiceRecovery(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.addReadyState(testBaseStateNames...)
	artifact := filepath.Join(fixture.state, ".relay-rotate-"+testRotationID+".key.old")
	fixture.filesystem.addFile(artifact, 0o400)
	identity := fixture.filesystem.metadata[artifact].Identity()
	fixture.filesystem.operations = nil
	if err := fixture.guard.Repair(context.Background()); err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	metadata, exists := fixture.filesystem.metadata[artifact]
	if !exists || metadata.Identity() != identity || metadata.Permissions != 0o600 {
		t.Fatalf("artifact after Repair = %#v, exists %v; want same inode mode 0600", metadata, exists)
	}
	for _, operation := range fixture.filesystem.operations {
		if operation.Name == "remove" || operation.Name == "rename" {
			t.Fatalf("Repair() took service-owned recovery action %#v", operation)
		}
	}
}

func TestDarwinStateGuardAcceptsOnlyRealRootOwnedAncestors(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	if err := fixture.guard.Validate(context.Background()); err != nil {
		t.Fatalf("Validate(safe ancestors) error = %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*pathMetadata)
	}{
		{name: "symlink", mutate: func(metadata *pathMetadata) { metadata.Type = pathTypeSymlink }},
		{name: "regular file", mutate: func(metadata *pathMetadata) { metadata.Type = pathTypeRegular }},
		{name: "wrong owner", mutate: func(metadata *pathMetadata) { metadata.UID = 501 }},
		{name: "group writable", mutate: func(metadata *pathMetadata) { metadata.Permissions = 0o775 }},
		{name: "other writable", mutate: func(metadata *pathMetadata) { metadata.Permissions = 0o757 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := newStateGuardFixture(t)
			test.mutate(candidate.filesystem.metadata[candidate.ancestors[1]])
			if err := candidate.guard.Validate(context.Background()); err == nil {
				t.Fatal("Validate() error = nil, want unsafe ancestor rejection")
			}
			candidate.assertNoMutations(t)
		})
	}
}

func TestDarwinStateGuardRejectsEveryStateTreeSymlinkComponent(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*stateGuardFixture)
	}{
		{
			name: "trusted root",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.ancestors[0]].Type = pathTypeSymlink
			},
		},
		{
			name: "Library",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.ancestors[1]].Type = pathTypeSymlink
			},
		},
		{
			name: "Application Support",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.ancestors[2]].Type = pathTypeSymlink
			},
		},
		{
			name: "product",
			mutate: func(fixture *stateGuardFixture) {
				fixture.filesystem.metadata[fixture.product].Type = pathTypeSymlink
			},
		},
		{
			name: "Relay",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("state.db")
				fixture.filesystem.metadata[fixture.state].Type = pathTypeSymlink
			},
		},
		{
			name: "state file",
			mutate: func(fixture *stateGuardFixture) {
				fixture.addReadyState("state.db")
				fixture.filesystem.metadata[filepath.Join(fixture.state, "state.db")].Type = pathTypeSymlink
			},
		},
		{
			name: "setup stage",
			mutate: func(fixture *stateGuardFixture) {
				stage := filepath.Join(fixture.product, ".relay-setup-"+testSetupID)
				fixture.filesystem.addObject(stage, pathTypeSymlink, 0o700)
			},
		},
		{
			name: "setup stage file",
			mutate: func(fixture *stateGuardFixture) {
				stage := filepath.Join(fixture.product, ".relay-setup-"+testSetupID)
				fixture.filesystem.addDirectory(stage, 0o700)
				fixture.filesystem.addObject(filepath.Join(stage, "state.db"), pathTypeSymlink, 0o600)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newStateGuardFixture(t)
			test.mutate(fixture)
			if err := fixture.guard.Validate(context.Background()); err == nil {
				t.Fatal("Validate() error = nil, want symlink rejection")
			}
			fixture.assertNoMutations(t)
		})
	}
}

func TestDarwinStateGuardAllowsOnlyVarRunToPrivateVarRunCanonicalException(t *testing.T) {
	t.Parallel()

	if err := validateCanonicalPrivilegedParent(
		"/var/run",
		"/private/var/run",
		canonicalParentDarwinVarRun,
	); err != nil {
		t.Fatalf("validateCanonicalPrivilegedParent(/var/run) error = %v", err)
	}
	if err := validateCanonicalPrivilegedParent(
		"/Library/Application Support",
		"/Library/Application Support",
		canonicalParentExact,
	); err != nil {
		t.Fatalf("validateCanonicalPrivilegedParent(exact state parent) error = %v", err)
	}
}

func TestDarwinStateGuardRejectsAlternateCanonicalParent(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		spelled   string
		canonical string
		policy    canonicalParentPolicy
	}{
		{spelled: "/var/run", canonical: "/tmp/run", policy: canonicalParentDarwinVarRun},
		{spelled: "/private/var/run", canonical: "/private/var/run", policy: canonicalParentDarwinVarRun},
		{spelled: "/var/run", canonical: "/var/run", policy: canonicalParentDarwinVarRun},
		{spelled: "/Library/Application Support", canonical: "/private/tmp/support", policy: canonicalParentExact},
	} {
		if err := validateCanonicalPrivilegedParent(test.spelled, test.canonical, test.policy); err == nil {
			t.Fatalf("validateCanonicalPrivilegedParent(%q, %q, %d) error = nil, want rejection", test.spelled, test.canonical, test.policy)
		}
	}
}

func TestDarwinStateGuardFailsClosedWithoutACLInspection(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.guard.acl = unavailablePathACLInspector{}
	if err := fixture.guard.Validate(context.Background()); err == nil {
		t.Fatal("Validate() error = nil without ACL inspection")
	}
	fixture.assertNoMutations(t)
}

func TestStateGuardUsesExactACLPolicyForAncestorsAndProtectedTree(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.addReadyState("state.db")
	recorder := &recordingStateACLInspector{}
	fixture.guard.acl = recorder
	if err := fixture.guard.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	want := map[string]pathACLPolicy{
		fixture.ancestors[0]:                     pathACLRejectNonRootMutation,
		fixture.ancestors[1]:                     pathACLRejectNonRootMutation,
		fixture.ancestors[2]:                     pathACLRejectNonRootMutation,
		fixture.product:                          pathACLRejectExtended,
		fixture.state:                            pathACLRejectExtended,
		filepath.Join(fixture.state, "state.db"): pathACLRejectExtended,
	}
	seen := make(map[string]bool, len(want))
	for _, observation := range recorder.observations {
		policy, ok := want[observation.Path]
		if !ok || observation.Policy != policy {
			t.Fatalf("unexpected ACL policy observation %#v; want path policies %#v", observation, want)
		}
		seen[observation.Path] = true
	}
	for path := range want {
		if !seen[path] {
			t.Fatalf("ACL policy was not checked for %q; observations = %#v", path, recorder.observations)
		}
	}
}

func TestStateGuardRepairACLFailurePreventsEveryChmod(t *testing.T) {
	t.Parallel()

	fixture := newStateGuardFixture(t)
	fixture.addReadyState("state.db")
	fixture.filesystem.metadata[fixture.product].Permissions = 0o755
	recorder := &recordingStateACLInspector{
		failPath: filepath.Join(fixture.state, "state.db"),
		failErr:  errors.New("injected extended ACL"),
	}
	fixture.guard.acl = recorder
	fixture.filesystem.operations = nil
	if err := fixture.guard.Repair(context.Background()); err == nil {
		t.Fatal("Repair() error = nil, want ACL rejection")
	}
	fixture.assertNoMutations(t)
}

type stateGuardFixture struct {
	filesystem *fakeStateFilesystem
	guard      *statePathGuard
	ancestors  []string
	product    string
	state      string
}

func newStateGuardFixture(t *testing.T) *stateGuardFixture {
	t.Helper()

	root := filepath.Join(t.TempDir(), "system-root")
	library := filepath.Join(root, "Library")
	support := filepath.Join(library, "Application Support")
	product := filepath.Join(support, "ZFNF Mobile Egress")
	state := filepath.Join(product, "Relay")
	filesystem := newFakeStateFilesystem()
	for _, ancestor := range []string{root, library, support} {
		filesystem.addDirectory(ancestor, 0o755)
	}
	filesystem.addDirectory(product, 0o700)
	guard, err := newStatePathGuard(statePathGuardConfig{
		ProductDir:       product,
		StateDir:         state,
		TrustedAncestors: []string{root, library, support},
		Filesystem:       filesystem,
		ACL:              allowStateACLs{},
	})
	if err != nil {
		t.Fatalf("newStatePathGuard() error = %v", err)
	}
	filesystem.operations = nil
	return &stateGuardFixture{
		filesystem: filesystem,
		guard:      guard,
		ancestors:  []string{root, library, support},
		product:    product,
		state:      state,
	}
}

func (fixture *stateGuardFixture) addReadyState(names ...string) {
	fixture.filesystem.addDirectory(fixture.state, 0o700)
	for _, name := range names {
		fixture.filesystem.addFile(filepath.Join(fixture.state, name), safeStateFileMode(name))
	}
}

func (fixture *stateGuardFixture) assertNoMutations(t *testing.T) {
	t.Helper()
	if mutations := fixture.filesystem.mutations(); len(mutations) != 0 {
		t.Fatalf("filesystem mutations = %#v, want none", mutations)
	}
}

func safeStateFileMode(name string) uint16 {
	switch name {
	case "ca.crt", "relay.crt":
		return 0o644
	default:
		return 0o600
	}
}

type fakeStateFilesystem struct {
	metadata         map[string]*pathMetadata
	children         map[string][]string
	operations       []stateFilesystemOperation
	nextInode        uint64
	observations     int
	afterObservation func(int)
	beforeOpen       func(string, pathMetadata)
	beforeChmod      func(string)
	afterChmod       func(string)
}

type stateFilesystemOperation struct {
	Name     string
	Path     string
	Mode     uint16
	Expected pathIdentity
	Admitted pathMetadata
}

func newFakeStateFilesystem() *fakeStateFilesystem {
	return &fakeStateFilesystem{
		metadata:  make(map[string]*pathMetadata),
		children:  make(map[string][]string),
		nextInode: 100,
	}
}

func (filesystem *fakeStateFilesystem) Lstat(_ context.Context, stringPath string) (pathMetadata, error) {
	path := filepath.Clean(stringPath)
	filesystem.observe(stateFilesystemOperation{Name: "lstat", Path: path})
	metadata, ok := filesystem.metadata[path]
	if !ok {
		return pathMetadata{}, fs.ErrNotExist
	}
	return *metadata, nil
}

func (filesystem *fakeStateFilesystem) Open(_ context.Context, stringPath string, expected pathMetadata) (openedPath, error) {
	path := filepath.Clean(stringPath)
	filesystem.observe(stateFilesystemOperation{Name: "open", Path: path, Admitted: expected})
	if filesystem.beforeOpen != nil {
		filesystem.beforeOpen(path, expected)
	}
	metadata, ok := filesystem.metadata[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	if *metadata != expected || expected.Type != pathTypeDirectory && expected.Type != pathTypeRegular {
		return nil, errStatePathChanged
	}
	return &fakeOpenedPath{
		filesystem: filesystem,
		path:       path,
		metadata:   metadata,
		entries:    append([]string(nil), filesystem.children[path]...),
	}, nil
}

func assertNoOpenedPathUseAfter(t *testing.T, operations []stateFilesystemOperation, cutoff int, path string) {
	t.Helper()
	if cutoff < 0 || cutoff > len(operations) {
		t.Fatalf("replacement cutoff = %d for operations %#v", cutoff, operations)
	}
	for _, operation := range operations[cutoff:] {
		if operation.Path == path && (operation.Name == "fstat" || operation.Name == "readdir-fd" || operation.Name == "chmod") {
			t.Fatalf("replacement was used after opened-path rejection: %#v", operations[cutoff:])
		}
	}
}

func (filesystem *fakeStateFilesystem) Mkdir(_ context.Context, stringPath string, mode uint16) error {
	path := filepath.Clean(stringPath)
	filesystem.operations = append(filesystem.operations, stateFilesystemOperation{Name: "mkdir", Path: path, Mode: mode})
	if _, exists := filesystem.metadata[path]; exists {
		return fs.ErrExist
	}
	filesystem.addDirectory(path, mode)
	return nil
}

type fakeOpenedPath struct {
	filesystem *fakeStateFilesystem
	path       string
	metadata   *pathMetadata
	entries    []string
	closed     bool
}

func (opened *fakeOpenedPath) Path() string { return opened.path }

func (opened *fakeOpenedPath) Metadata(_ context.Context) (pathMetadata, error) {
	opened.filesystem.observe(stateFilesystemOperation{Name: "fstat", Path: opened.path})
	if opened.closed {
		return pathMetadata{}, fs.ErrClosed
	}
	return *opened.metadata, nil
}

func (opened *fakeOpenedPath) ReadDir(_ context.Context) ([]string, error) {
	opened.filesystem.observe(stateFilesystemOperation{Name: "readdir-fd", Path: opened.path})
	if opened.closed {
		return nil, fs.ErrClosed
	}
	if opened.metadata.Type != pathTypeDirectory {
		return nil, errors.New("not a directory")
	}
	entries := append([]string(nil), opened.entries...)
	sort.Strings(entries)
	return entries, nil
}

func (opened *fakeOpenedPath) Chmod(
	ctx context.Context,
	expected pathMetadata,
	mode uint16,
	validateACL func(context.Context) error,
) error {
	if opened.filesystem.beforeChmod != nil {
		opened.filesystem.beforeChmod(opened.path)
	}
	if err := opened.verify(ctx, expected); err != nil {
		return err
	}
	if validateACL == nil {
		return errStatePathUnsafe
	}
	if err := validateACL(ctx); err != nil {
		return err
	}
	if err := opened.verify(ctx, expected); err != nil {
		return err
	}
	opened.filesystem.operations = append(opened.filesystem.operations, stateFilesystemOperation{
		Name: "chmod", Path: opened.path, Mode: mode, Expected: expected.Identity(),
	})
	opened.metadata.Permissions = mode
	if opened.filesystem.afterChmod != nil {
		opened.filesystem.afterChmod(opened.path)
	}
	wantAfter := expected
	wantAfter.Permissions = mode
	if err := opened.verify(ctx, wantAfter); err != nil {
		return err
	}
	if err := validateACL(ctx); err != nil {
		return err
	}
	return opened.verify(ctx, wantAfter)
}

func (opened *fakeOpenedPath) verify(ctx context.Context, expected pathMetadata) error {
	metadata, err := opened.Metadata(ctx)
	if err != nil || metadata != expected {
		return errStatePathChanged
	}
	pathMetadata, err := opened.filesystem.Lstat(ctx, opened.path)
	if err != nil || pathMetadata != expected {
		return errStatePathChanged
	}
	return ctx.Err()
}

func (opened *fakeOpenedPath) Close() error {
	if opened.closed {
		return fs.ErrClosed
	}
	opened.closed = true
	return nil
}

func (filesystem *fakeStateFilesystem) addDirectory(path string, mode uint16) {
	filesystem.addObject(path, pathTypeDirectory, mode)
}

func (filesystem *fakeStateFilesystem) addFile(path string, mode uint16) {
	filesystem.addObject(path, pathTypeRegular, mode)
}

func (filesystem *fakeStateFilesystem) addObject(stringPath string, objectType pathObjectType, mode uint16) {
	path := filepath.Clean(stringPath)
	if _, exists := filesystem.metadata[path]; !exists {
		filesystem.metadata[path] = filesystem.newMetadata(objectType, mode)
		parent := filepath.Dir(path)
		name := filepath.Base(path)
		if !containsString(filesystem.children[parent], name) {
			filesystem.children[parent] = append(filesystem.children[parent], name)
		}
		if objectType == pathTypeDirectory {
			filesystem.children[path] = nil
		}
		return
	}
	metadata := filesystem.metadata[path]
	metadata.Type = objectType
	metadata.Permissions = mode
	filesystem.metadata[path] = metadata
}

func (filesystem *fakeStateFilesystem) newMetadata(objectType pathObjectType, mode uint16) *pathMetadata {
	filesystem.nextInode++
	return &pathMetadata{
		Device:      7,
		Inode:       filesystem.nextInode,
		UID:         0,
		GID:         0,
		Links:       1,
		RawType:     testRawPathType(objectType),
		Type:        objectType,
		Permissions: mode,
	}
}

func testRawPathType(objectType pathObjectType) uint16 {
	switch objectType {
	case pathTypeDirectory:
		return 0o040000
	case pathTypeRegular:
		return 0o100000
	case pathTypeSymlink:
		return 0o120000
	default:
		return 0
	}
}

func (filesystem *fakeStateFilesystem) remove(stringPath string) {
	path := filepath.Clean(stringPath)
	delete(filesystem.metadata, path)
	delete(filesystem.children, path)
	parent := filepath.Dir(path)
	name := filepath.Base(path)
	entries := filesystem.children[parent][:0]
	for _, entry := range filesystem.children[parent] {
		if entry != name {
			entries = append(entries, entry)
		}
	}
	filesystem.children[parent] = entries
}

func (filesystem *fakeStateFilesystem) observe(operation stateFilesystemOperation) {
	filesystem.operations = append(filesystem.operations, operation)
	filesystem.observations++
	if filesystem.afterObservation != nil {
		filesystem.afterObservation(filesystem.observations)
	}
}

func (filesystem *fakeStateFilesystem) mutations() []stateFilesystemOperation {
	var result []stateFilesystemOperation
	for _, operation := range filesystem.operations {
		if operation.Name == "mkdir" || operation.Name == "chmod" {
			result = append(result, operation)
		}
	}
	return result
}

type allowStateACLs struct{}

func (allowStateACLs) Validate(context.Context, openedPath, pathACLPolicy) error { return nil }

type stateACLObservation struct {
	Path   string
	Policy pathACLPolicy
}

type recordingStateACLInspector struct {
	observations  []stateACLObservation
	failPath      string
	failErr       error
	afterValidate func(string, pathACLPolicy)
}

func (inspector *recordingStateACLInspector) Validate(_ context.Context, opened openedPath, policy pathACLPolicy) error {
	path := opened.Path()
	inspector.observations = append(inspector.observations, stateACLObservation{Path: path, Policy: policy})
	if path == inspector.failPath {
		return inspector.failErr
	}
	if inspector.afterValidate != nil {
		inspector.afterValidate(path, policy)
	}
	return nil
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func reflectStateOperationsEqual(got, want []stateFilesystemOperation) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index].Name != want[index].Name || got[index].Path != want[index].Path || got[index].Mode != want[index].Mode {
			return false
		}
	}
	return true
}

func firstStateMutationIndex(operations []stateFilesystemOperation) int {
	for index, operation := range operations {
		if operation.Name == "mkdir" || operation.Name == "chmod" {
			return index
		}
	}
	return len(operations)
}

func statePathObservedBefore(operations []stateFilesystemOperation, path string, before int) bool {
	for index, operation := range operations {
		if index >= before {
			break
		}
		if operation.Name == "lstat" && operation.Path == path {
			return true
		}
	}
	return false
}
