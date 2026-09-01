package tailscale

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestResolveAndStageMacPKGRetainsVerifiedDescriptor(t *testing.T) {
	t.Parallel()

	payload := []byte("fixture package")
	digest := sha256.Sum256(payload)
	release := MacRelease{
		Version:     "1.100.1",
		PKGURL:      StablePackagesURL + "Tailscale-1.100.1-macos.pkg",
		ChecksumURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256",
	}
	client, requested := macTransactionClient(release, payload, hex.EncodeToString(digest[:]))
	gotRelease, stage, err := resolveAndStageMacPKG(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if gotRelease != release {
		t.Fatalf("release = %#v, want %#v", gotRelease, release)
	}
	wantRequests := []string{StablePackagesURL, release.ChecksumURL, release.PKGURL}
	if !reflect.DeepEqual(*requested, wantRequests) {
		t.Fatalf("requests = %#v, want %#v", *requested, wantRequests)
	}
	if stage == nil || stage.file == nil {
		t.Fatal("stage did not retain its descriptor")
	}
	if err := stage.Revalidate(context.Background()); err != nil {
		t.Fatalf("fresh stage revalidation: %v", err)
	}
	content, err := os.ReadFile(stage.Path())
	if err != nil || !bytes.Equal(content, payload) {
		t.Fatalf("staged content = %q/%v", content, err)
	}
	path := stage.Path()
	directory := filepath.Dir(path)
	if err := stage.RemoveAfterQuiescence(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged path remains: %v", err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory remains: %v", err)
	}
	if err := stage.RemoveAfterQuiescence(); err != nil {
		t.Fatalf("idempotent removal: %v", err)
	}
}

func TestMacPathPhaseSwapRejectsPersistentMutation(t *testing.T) {
	t.Parallel()

	payload := []byte("fixture package")
	digest := sha256.Sum256(payload)
	release := MacRelease{Version: "1.100.1", PKGURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg", ChecksumURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256"}
	client, _ := macTransactionClient(release, payload, hex.EncodeToString(digest[:]))
	_, stage, err := resolveAndStageMacPKG(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stage.file.Close()
		_ = stage.directory.Close()
		_ = stage.parent.Close()
		_ = os.Remove(stage.path)
		_ = os.Remove(filepath.Dir(stage.path))
	})
	file, err := os.OpenFile(stage.Path(), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("replacement")); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if err := stage.Revalidate(context.Background()); err == nil {
		t.Fatal("persistent append passed the stage guard")
	}
}

func TestMacPathIntraCommandABACharacterizesResidual(t *testing.T) {
	t.Parallel()

	guard := &countingPathPhaseGuard{}
	consumerSaw := ""
	err := runGuardedPathPhase(context.Background(), guard, func(string) error {
		consumerSaw = "replacement"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if guard.revalidations != 2 || consumerSaw != "replacement" {
		t.Fatalf("pre/post=%d consumer=%q", guard.revalidations, consumerSaw)
	}
	// This deliberately records the same-UID intra-command ABA residual: the
	// boundary guard saw the admitted identity before and after while the
	// pathname consumer observed a replacement in between.
}

func TestMacStageRejectsAndPreservesPersistentIdentityMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*modelStageOperations)
	}{
		{name: "symlink replacement", mutate: func(operations *modelStageOperations) { operations.replaceFile(modelStageSymlink) }},
		{name: "hard link", mutate: func(operations *modelStageOperations) { operations.fileNode.links = 2; operations.fileNode.revision++ }},
		{name: "mode", mutate: func(operations *modelStageOperations) {
			operations.fileNode.mode = 0o644
			operations.fileNode.revision++
		}},
		{name: "owner", mutate: func(operations *modelStageOperations) { operations.fileNode.uid++; operations.fileNode.revision++ }},
		{name: "truncate", mutate: func(operations *modelStageOperations) {
			operations.fileNode.data = operations.fileNode.data[:3]
			operations.fileNode.revision++
		}},
		{name: "append", mutate: func(operations *modelStageOperations) {
			operations.fileNode.data = append(operations.fileNode.data, []byte("changed")...)
			operations.fileNode.revision++
		}},
		{name: "rename replacement", mutate: func(operations *modelStageOperations) { operations.replaceFile(modelStageRegular) }},
		{name: "directory rename replacement", mutate: func(operations *modelStageOperations) { operations.replaceDirectory() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stage, operations := newModelStagedPackage(t)
			test.mutate(operations)
			if err := stage.Revalidate(context.Background()); err == nil {
				t.Fatal("persistent identity mutation passed revalidation")
			}
			if err := stage.RemoveAfterQuiescence(); !errors.Is(err, errMacCleanupPending) {
				t.Fatalf("mutation cleanup = %v", err)
			}
			if operations.removeFileCalls != 0 || operations.removeDirectoryCalls != 0 {
				t.Fatalf("mutation triggered removal calls %d/%d", operations.removeFileCalls, operations.removeDirectoryCalls)
			}
			if operations.replacementNode != nil && !operations.replacementNode.sentinel {
				t.Fatal("replacement sentinel was not preserved")
			}
			if stage.Path() != "" {
				t.Fatalf("uncertain stage remained usable at %q", stage.Path())
			}
		})
	}
}

func TestMacStageRevalidationRejectsMutationAtEveryDetectableInternalBarrier(t *testing.T) {
	for _, barrier := range []string{
		"parent precheck", "directory precheck", "enumeration", "file prehash",
		"descriptor hash", "file posthash", "directory postcheck",
	} {
		t.Run(barrier, func(t *testing.T) {
			stage, model := newModelStagedPackage(t)
			operations := &revalidationBoundaryOperations{macStageOperations: model, model: model, barrier: barrier}
			stage.operations = operations
			if barrier == "descriptor hash" {
				stage.file = &revalidationBoundaryFile{macStageFile: stage.file, operations: operations}
			}
			if err := stage.Revalidate(context.Background()); err == nil {
				t.Fatal("persistent exact-path replacement passed an internal barrier")
			}
			if !operations.triggered || model.replacementNode == nil || !model.replacementNode.sentinel || model.currentFileNode != model.replacementNode {
				t.Fatal("internal barrier did not preserve the exact-path replacement sentinel")
			}
			if err := stage.RemoveAfterQuiescence(); !errors.Is(err, errMacCleanupPending) {
				t.Fatalf("internal barrier cleanup = %v", err)
			}
		})
	}
}

func TestMacStageFinalRevalidationIntervalRemainsCharacterizedResidual(t *testing.T) {
	stage, model := newModelStagedPackage(t)
	operations := &revalidationBoundaryOperations{macStageOperations: model, model: model, barrier: "final parent return"}
	stage.operations = operations
	if err := stage.Revalidate(context.Background()); err != nil {
		t.Fatalf("final-interval characterization unexpectedly rejected: %v", err)
	}
	if !operations.triggered || model.currentFileNode != model.replacementNode || model.replacementNode == nil || !model.replacementNode.sentinel {
		t.Fatal("final-interval consumer did not observe the modeled replacement")
	}
	// This is intentionally not a security-pass claim: a replacement inserted
	// after the final child check can persist into the documented final handoff
	// interval. The next boundary check rejects it and cleanup preserves it.
	if err := stage.Revalidate(context.Background()); err == nil {
		t.Fatal("next boundary accepted the persistent final-interval replacement")
	}
	if err := stage.RemoveAfterQuiescence(); !errors.Is(err, errMacCleanupPending) {
		t.Fatalf("final-interval cleanup = %v", err)
	}
}

func TestMacPathIntraCommandABAConsumerSeesReplacementWhileBoundaryChecksPass(t *testing.T) {
	for _, phase := range []string{"package identity", "package policy", "Gatekeeper policy", "Installer launch"} {
		t.Run(phase, func(t *testing.T) {
			stage, operations := newModelStagedPackage(t)
			admittedIdentity := operations.fileNode.identity
			consumerIdentity := uint64(0)
			if err := runGuardedPathPhase(context.Background(), stage, func(path string) error {
				if path != stage.path {
					return errors.New("wrong guarded path")
				}
				replacement := operations.swapFileForABA()
				consumerIdentity = replacement.identity
				operations.restoreFileAfterABA()
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if consumerIdentity == 0 || consumerIdentity == admittedIdentity {
				t.Fatalf("consumer identity = %d, admitted %d", consumerIdentity, admittedIdentity)
			}
			if err := stage.RemoveAfterQuiescence(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMacStageCleanupConcurrentCallersJoinOneAttempt(t *testing.T) {
	for _, test := range []struct {
		name      string
		removeErr error
		wantErr   error
	}{
		{name: "success"},
		{name: "failure", removeErr: errors.New("hostile close path"), wantErr: errMacCleanupPending},
	} {
		t.Run(test.name, func(t *testing.T) {
			stage, operations := newModelStagedPackage(t)
			operations.removeFileErr = test.removeErr
			const callers = 32
			results := make(chan error, callers)
			var wait sync.WaitGroup
			wait.Add(callers)
			for index := 0; index < callers; index++ {
				go func() {
					defer wait.Done()
					results <- stage.RemoveAfterQuiescence()
				}()
			}
			wait.Wait()
			close(results)
			for err := range results {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("cleanup result = %v, want %v", err, test.wantErr)
				}
			}
			if operations.removeFileCalls != 1 {
				t.Fatalf("file removal attempts = %d", operations.removeFileCalls)
			}
			if test.removeErr == nil && operations.removeDirectoryCalls != 1 {
				t.Fatalf("directory removal attempts = %d", operations.removeDirectoryCalls)
			}
			if test.removeErr != nil && (operations.fileNode == nil || stage.Path() != "") {
				t.Fatal("failed cleanup did not retain an unusable owned file")
			}
		})
	}
}

func TestRunGuardedPathPhaseRedactsConsumerFailure(t *testing.T) {
	t.Parallel()

	guard := &countingPathPhaseGuard{}
	err := runGuardedPathPhase(context.Background(), guard, func(string) error {
		return errors.New(`hostile output C:\Users\operator\private-stage.pkg`)
	})
	if !errors.Is(err, errMacStage) || strings.Contains(err.Error(), "hostile") || strings.Contains(err.Error(), "private-stage") {
		t.Fatalf("guarded phase error = %q", err)
	}
	if guard.revalidations != 2 {
		t.Fatalf("guarded phase revalidations = %d", guard.revalidations)
	}
}

func TestResolveAndStageMacPKGReturnsNoUsableStageOnTransactionFailure(t *testing.T) {
	t.Parallel()

	payload := []byte("fixture package")
	release := MacRelease{Version: "1.100.1", PKGURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg", ChecksumURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256"}
	client, _ := macTransactionClient(release, payload, strings.Repeat("0", 64))
	if _, stage, err := resolveAndStageMacPKG(context.Background(), client); err == nil || stage != nil {
		t.Fatalf("digest failure returned stage=%#v err=%v", stage, err)
	}
}

func TestStageMacPKGRejectsMissingDescriptorRelativeOperations(t *testing.T) {
	t.Parallel()

	release := MacRelease{
		Version:     "1.100.1",
		PKGURL:      StablePackagesURL + "Tailscale-1.100.1-macos.pkg",
		ChecksumURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256",
	}
	if stage, err := stageMacPKGWithOperations(
		context.Background(),
		&http.Client{},
		release,
		strings.Repeat("0", 64),
		nil,
		"mobile-egress-tailscale-0123456789abcdef0123456789abcdef",
	); err == nil || stage != nil {
		t.Fatalf("missing descriptor-relative operations returned stage=%#v err=%v", stage, err)
	}
}

func TestPlatformMacStageOperationsRemoveEmptyAdmittedDirectory(t *testing.T) {
	operations, err := newPlatformMacStageOperations()
	if err != nil {
		t.Fatal(err)
	}
	base, err := operations.ResolveTemporaryBase(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := operations.OpenParent(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	name, err := newMacStageDirectoryName()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := operations.CreateDirectory(context.Background(), parent, name, 0o700)
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	if err := operations.AdmitDirectory(context.Background(), parent, directory, name); err != nil {
		t.Fatal(err)
	}
	if err := operations.RemoveDirectory(context.Background(), parent, directory, name); err != nil {
		t.Fatalf("RemoveDirectory: %v", err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHasExactStageEntryDoesNotFoldCase(t *testing.T) {
	entries := []string{"Tailscale-1.100.1-macos.pkg", "unrelated"}
	if !hasExactStageEntry(entries, "Tailscale-1.100.1-macos.pkg") {
		t.Fatal("exact child entry was not found")
	}
	for _, candidate := range []string{"tailscale-1.100.1-macos.pkg", "TAILSCALE-1.100.1-MACOS.PKG", "Tailscale-1.100.1-macos.pkg/"} {
		if hasExactStageEntry(entries, candidate) {
			t.Fatalf("non-exact child entry %q matched", candidate)
		}
	}
}

func TestPortableStageFailureCleanupPreservesSupplementalSiblingSentinel(t *testing.T) {
	payload := []byte("fixture package")
	digest := sha256.Sum256(payload)
	release := MacRelease{
		Version:     "1.100.1",
		PKGURL:      StablePackagesURL + "Tailscale-1.100.1-macos.pkg",
		ChecksumURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256",
	}
	baseOperations, err := newPlatformMacStageOperations()
	if err != nil {
		t.Fatal(err)
	}
	operations := &replacementOnAdmitStageOperations{macStageOperations: baseOperations}
	client := packageBodyClient(release.PKGURL, int64(len(payload)), bytes.NewReader(payload))
	directoryName, err := newMacStageDirectoryName()
	if err != nil {
		t.Fatal(err)
	}
	stage, err := stageMacPKGWithOperations(
		context.Background(), client, release, hex.EncodeToString(digest[:]), operations, directoryName,
	)
	if !errors.Is(err, errMacCleanupPending) || stage == nil || stage.Path() != "" {
		t.Fatalf("replacement cleanup = stage %#v err %v", stage, err)
	}
	replacementSentinel := filepath.Join(operations.replacementPath, "do-not-delete")
	if value, readErr := os.ReadFile(replacementSentinel); readErr != nil || string(value) != "replacement" {
		t.Fatalf("replacement sentinel = %q/%v", value, readErr)
	}
	if operations.removeFileCalls != 0 || operations.removeDirectoryCalls != 0 {
		t.Fatalf("replacement removal calls = file %d directory %d", operations.removeFileCalls, operations.removeDirectoryCalls)
	}
	firstValidationCount := operations.directoryIdentityValidations
	if secondErr := stage.RemoveAfterQuiescence(); !errors.Is(secondErr, errMacCleanupPending) {
		t.Fatalf("joined cleanup result = %v", secondErr)
	}
	if operations.directoryIdentityValidations != firstValidationCount {
		t.Fatalf("cleanup retried: validations %d -> %d", firstValidationCount, operations.directoryIdentityValidations)
	}

	// The exact guarded-path replacement contract is exercised by the model
	// matrix above. This supplemental portable-filesystem test owns both sibling
	// identities and closes retained descriptors before nonrecursive teardown.
	_ = stage.file.Close()
	_ = stage.directory.Close()
	_ = stage.parent.Close()
	_ = os.Remove(filepath.Join(operations.admittedPath, stage.basename))
	_ = os.Remove(operations.admittedPath)
	_ = os.Remove(replacementSentinel)
	_ = os.Remove(operations.replacementPath)
}

func TestStageFailureBoundariesPreserveInjectedReplacementIdentity(t *testing.T) {
	for _, boundary := range []string{
		"create directory", "create file", "admit file", "admit directory",
		"validate parent", "validate directory", "read directory", "validate file",
	} {
		t.Run(boundary, func(t *testing.T) {
			payload := []byte("fixture package")
			digest := sha256.Sum256(payload)
			release := MacRelease{Version: "1.100.1", PKGURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg", ChecksumURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256"}
			model := newModelStageOperations(t.TempDir())
			operations := &boundaryReplacementStageOperations{macStageOperations: model, model: model, boundary: boundary}
			client := packageBodyClient(release.PKGURL, int64(len(payload)), bytes.NewReader(payload))
			stage, err := stageMacPKGWithOperations(
				context.Background(), client, release, hex.EncodeToString(digest[:]), operations,
				macStageDirectoryPrefix+"0123456789abcdef0123456789abcdef",
			)
			if !operations.triggered || !errors.Is(err, errMacCleanupPending) || stage == nil || stage.Path() != "" {
				t.Fatalf("boundary result triggered/stage/error = %t/%#v/%v", operations.triggered, stage, err)
			}
			if model.replacementNode == nil || !model.replacementNode.sentinel ||
				model.currentDirectoryNode != model.replacementNode && model.currentFileNode != model.replacementNode {
				t.Fatal("injected replacement identity was not preserved")
			}
			calls := model.removeFileCalls + model.removeDirectoryCalls
			if secondErr := stage.RemoveAfterQuiescence(); !errors.Is(secondErr, errMacCleanupPending) {
				t.Fatalf("joined cleanup result = %v", secondErr)
			}
			if model.removeFileCalls+model.removeDirectoryCalls != calls {
				t.Fatal("cleanup uncertainty was retried")
			}
		})
	}
}

func TestCleanupBoundariesPreserveReplacementOrCloseUncertainty(t *testing.T) {
	for _, boundary := range []string{
		"cleanup parent validation", "cleanup directory identity", "cleanup remove file",
		"cleanup read directory", "cleanup remove directory", "cleanup parent close",
	} {
		t.Run(boundary, func(t *testing.T) {
			stage, model := newModelStagedPackage(t)
			operations := &boundaryReplacementStageOperations{macStageOperations: model, model: model, boundary: boundary, armed: true}
			stage.operations = operations
			if boundary == "cleanup parent close" {
				stage.parent.(*modelStageDirectoryHandle).closeErr = errors.New("hostile parent close path")
			}
			if err := stage.RemoveAfterQuiescence(); !errors.Is(err, errMacCleanupPending) {
				t.Fatalf("cleanup boundary result = %v", err)
			}
			if boundary != "cleanup parent close" {
				if model.replacementNode == nil || !model.replacementNode.sentinel ||
					model.currentDirectoryNode != model.replacementNode && model.currentFileNode != model.replacementNode {
					t.Fatal("cleanup replacement identity was not preserved")
				}
			} else if stage.parent == nil {
				t.Fatal("parent close uncertainty lost retained ownership")
			}
			counts := operations.callCount
			if err := stage.RemoveAfterQuiescence(); !errors.Is(err, errMacCleanupPending) || operations.callCount != counts {
				t.Fatalf("cleanup join = %v/calls %d -> %d", err, counts, operations.callCount)
			}
		})
	}
}

func TestResolveAndStageMacPKGFailureMatrixReturnsNoUsableStage(t *testing.T) {
	payload := []byte("fixture package")
	payloadDigest := sha256.Sum256(payload)
	release := MacRelease{
		Version:     "1.100.1",
		PKGURL:      StablePackagesURL + "Tailscale-1.100.1-macos.pkg",
		ChecksumURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256",
	}
	tests := []struct {
		name             string
		indexStatus      int
		indexBody        func() io.Reader
		indexRedirect    string
		checksumStatus   int
		checksumBody     func() io.Reader
		packageStatus    int
		packageBody      func() io.Reader
		packageLength    int64
		wrapOperations   func(macStageOperations) macStageOperations
		expectedRequests []string
	}{
		{name: "index HTTP", indexStatus: http.StatusForbidden, expectedRequests: []string{StablePackagesURL}},
		{name: "index partial reader", indexBody: func() io.Reader {
			return &partialErrorReader{value: []byte(`<a href="Tailscale-1.100.1-macos.pkg">download</a>`)}
		}, expectedRequests: []string{StablePackagesURL}},
		{name: "index redirect", indexRedirect: "https://evil.example/stable/", expectedRequests: []string{StablePackagesURL}},
		{name: "checksum HTTP", checksumStatus: http.StatusForbidden, expectedRequests: []string{StablePackagesURL, release.ChecksumURL}},
		{name: "checksum partial reader", checksumBody: func() io.Reader { return &partialErrorReader{value: []byte(hex.EncodeToString(payloadDigest[:]))} }, expectedRequests: []string{StablePackagesURL, release.ChecksumURL}},
		{name: "malformed checksum", checksumBody: func() io.Reader { return strings.NewReader("hostile checksum text") }, expectedRequests: []string{StablePackagesURL, release.ChecksumURL}},
		{name: "package HTTP", packageStatus: http.StatusForbidden, expectedRequests: []string{StablePackagesURL, release.ChecksumURL, release.PKGURL}},
		{name: "package partial reader", packageBody: func() io.Reader { return &partialErrorReader{value: payload} }, expectedRequests: []string{StablePackagesURL, release.ChecksumURL, release.PKGURL}},
		{name: "package digest", checksumBody: func() io.Reader { return strings.NewReader(strings.Repeat("0", 64)) }, expectedRequests: []string{StablePackagesURL, release.ChecksumURL, release.PKGURL}},
		{name: "package size", packageLength: maximumPKGBytes + 1, expectedRequests: []string{StablePackagesURL, release.ChecksumURL, release.PKGURL}},
		{name: "package sync", wrapOperations: func(base macStageOperations) macStageOperations {
			return &syncFailureStageOperations{macStageOperations: base}
		}, expectedRequests: []string{StablePackagesURL, release.ChecksumURL, release.PKGURL}},
		{name: "file identity", wrapOperations: func(base macStageOperations) macStageOperations {
			return &admitFailureStageOperations{macStageOperations: base}
		}, expectedRequests: []string{StablePackagesURL, release.ChecksumURL, release.PKGURL}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseOperations, err := newPlatformMacStageOperations()
			if err != nil {
				t.Fatal(err)
			}
			operations := baseOperations
			if test.wrapOperations != nil {
				operations = test.wrapOperations(baseOperations)
			}
			client, requested := macFailureMatrixClient(release, payload, hex.EncodeToString(payloadDigest[:]), test)
			directoryName, err := newMacStageDirectoryName()
			if err != nil {
				t.Fatal(err)
			}
			_, stage, err := resolveAndStageMacPKGWithOperations(context.Background(), client, operations, directoryName)
			if err == nil || stage != nil {
				t.Fatalf("failure returned stage=%#v err=%v", stage, err)
			}
			if err.Error() != errMacStage.Error() || strings.Contains(err.Error(), "hostile") || strings.Contains(err.Error(), directoryName) {
				t.Fatalf("unfixed or unredacted error %q", err)
			}
			if !reflect.DeepEqual(*requested, test.expectedRequests) {
				t.Fatalf("requests = %#v, want %#v", *requested, test.expectedRequests)
			}
		})
	}
}

func macFailureMatrixClient(
	release MacRelease,
	payload []byte,
	digest string,
	test struct {
		name             string
		indexStatus      int
		indexBody        func() io.Reader
		indexRedirect    string
		checksumStatus   int
		checksumBody     func() io.Reader
		packageStatus    int
		packageBody      func() io.Reader
		packageLength    int64
		wrapOperations   func(macStageOperations) macStageOperations
		expectedRequests []string
	},
) (*http.Client, *[]string) {
	requested := &[]string{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*requested = append(*requested, request.URL.String())
		status := http.StatusOK
		contentLength := int64(-1)
		var body io.Reader
		switch request.URL.String() {
		case StablePackagesURL:
			if test.indexRedirect != "" {
				return redirectResponse(request, test.indexRedirect), nil
			}
			if test.indexStatus != 0 {
				status = test.indexStatus
			}
			if test.indexBody != nil {
				body = test.indexBody()
			} else {
				body = strings.NewReader(`<a href="Tailscale-1.100.1-macos.pkg">download</a>`)
			}
		case release.ChecksumURL:
			if test.checksumStatus != 0 {
				status = test.checksumStatus
			}
			if test.checksumBody != nil {
				body = test.checksumBody()
			} else {
				body = strings.NewReader(digest)
			}
		case release.PKGURL:
			if test.packageStatus != 0 {
				status = test.packageStatus
			}
			if test.packageBody != nil {
				body = test.packageBody()
			} else {
				body = bytes.NewReader(payload)
			}
			contentLength = test.packageLength
		default:
			return nil, errors.New("unexpected request")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(body), Request: request, ContentLength: contentLength}, nil
	})}
	return client, requested
}

type syncFailureStageOperations struct{ macStageOperations }
type syncFailureStageFile struct{ macStageFile }

func (operations *syncFailureStageOperations) CreateFile(
	ctx context.Context,
	directory macStageDirectory,
	name string,
	mode os.FileMode,
) (macStageFile, error) {
	file, err := operations.macStageOperations.CreateFile(ctx, directory, name, mode)
	if file == nil {
		return nil, err
	}
	return &syncFailureStageFile{macStageFile: file}, err
}

func (*syncFailureStageFile) Sync() error { return errors.New("hostile sync path") }

func unwrapSyncFailureStageFile(file macStageFile) macStageFile {
	if wrapped, ok := file.(*syncFailureStageFile); ok {
		return wrapped.macStageFile
	}
	return file
}

func (operations *syncFailureStageOperations) AdmitFile(ctx context.Context, directory macStageDirectory, file macStageFile, name string) (int64, error) {
	return operations.macStageOperations.AdmitFile(ctx, directory, unwrapSyncFailureStageFile(file), name)
}
func (operations *syncFailureStageOperations) ValidateFile(ctx context.Context, directory macStageDirectory, file macStageFile, name string) (int64, error) {
	return operations.macStageOperations.ValidateFile(ctx, directory, unwrapSyncFailureStageFile(file), name)
}
func (operations *syncFailureStageOperations) RemoveFile(ctx context.Context, directory macStageDirectory, file macStageFile, name string) error {
	return operations.macStageOperations.RemoveFile(ctx, directory, unwrapSyncFailureStageFile(file), name)
}

type admitFailureStageOperations struct{ macStageOperations }

func (*admitFailureStageOperations) AdmitFile(context.Context, macStageDirectory, macStageFile, string) (int64, error) {
	return 0, errors.New("hostile identity path")
}

type modelStageKind uint8

const (
	modelStageDirectory modelStageKind = iota + 1
	modelStageRegular
	modelStageSymlink
)

type modelStageNode struct {
	identity uint64
	kind     modelStageKind
	mode     os.FileMode
	uid      uint32
	links    uint64
	revision uint64
	data     []byte
	sentinel bool
}

type modelStageSnapshot struct {
	identity uint64
	kind     modelStageKind
	mode     os.FileMode
	uid      uint32
	links    uint64
	revision uint64
	size     int
}

func snapshotModelStageNode(node *modelStageNode) modelStageSnapshot {
	if node == nil {
		return modelStageSnapshot{}
	}
	return modelStageSnapshot{
		identity: node.identity,
		kind:     node.kind,
		mode:     node.mode,
		uid:      node.uid,
		links:    node.links,
		revision: node.revision,
		size:     len(node.data),
	}
}

type modelStageDirectoryHandle struct {
	path     string
	node     *modelStageNode
	created  modelStageSnapshot
	admitted modelStageSnapshot
	closed   bool
	closes   int
	closeErr error
}

func (directory *modelStageDirectoryHandle) Path() string { return directory.path }
func (directory *modelStageDirectoryHandle) Close() error {
	if directory.closed {
		return os.ErrClosed
	}
	directory.closed = true
	directory.closes++
	return directory.closeErr
}

type modelStageFileHandle struct {
	node     *modelStageNode
	created  modelStageSnapshot
	admitted modelStageSnapshot
	closed   bool
}

func (file *modelStageFileHandle) Write(value []byte) (int, error) {
	if file.closed {
		return 0, os.ErrClosed
	}
	file.node.data = append(file.node.data, value...)
	file.node.revision++
	return len(value), nil
}
func (file *modelStageFileHandle) ReadAt(value []byte, offset int64) (int, error) {
	if file.closed {
		return 0, os.ErrClosed
	}
	if offset >= int64(len(file.node.data)) {
		return 0, io.EOF
	}
	count := copy(value, file.node.data[offset:])
	if count < len(value) {
		return count, io.EOF
	}
	return count, nil
}
func (*modelStageFileHandle) Sync() error { return nil }
func (file *modelStageFileHandle) Close() error {
	if file.closed {
		return os.ErrClosed
	}
	file.closed = true
	return nil
}

type modelStageOperations struct {
	basePath             string
	nextIdentity         uint64
	parentNode           *modelStageNode
	directoryNode        *modelStageNode
	fileNode             *modelStageNode
	currentDirectoryNode *modelStageNode
	currentFileNode      *modelStageNode
	replacementNode      *modelStageNode
	abaAdmittedFile      *modelStageNode
	removeFileCalls      int
	removeDirectoryCalls int
	removeFileErr        error
}

func newModelStageOperations(basePath string) *modelStageOperations {
	operations := &modelStageOperations{basePath: basePath, nextIdentity: 10}
	operations.parentNode = operations.newNode(modelStageDirectory, 0o700)
	return operations
}

func (operations *modelStageOperations) newNode(kind modelStageKind, mode os.FileMode) *modelStageNode {
	operations.nextIdentity++
	return &modelStageNode{identity: operations.nextIdentity, kind: kind, mode: mode, uid: 501, links: 1}
}

func (operations *modelStageOperations) ResolveTemporaryBase(ctx context.Context) (string, error) {
	return operations.basePath, ctx.Err()
}

func (operations *modelStageOperations) OpenParent(ctx context.Context, path string) (macStageDirectory, error) {
	if err := ctx.Err(); err != nil || path != operations.basePath {
		return nil, errMacStage
	}
	return &modelStageDirectoryHandle{path: path, node: operations.parentNode, created: snapshotModelStageNode(operations.parentNode), admitted: snapshotModelStageNode(operations.parentNode)}, nil
}

func (operations *modelStageOperations) CreateDirectory(ctx context.Context, parent macStageDirectory, name string, mode os.FileMode) (macStageDirectory, error) {
	parentHandle, ok := parent.(*modelStageDirectoryHandle)
	if err := ctx.Err(); err != nil || !ok || parentHandle.node != operations.parentNode || mode != 0o700 || !validStageChildName(name) {
		return nil, errMacStage
	}
	operations.directoryNode = operations.newNode(modelStageDirectory, mode)
	operations.currentDirectoryNode = operations.directoryNode
	operations.parentNode.revision++
	return &modelStageDirectoryHandle{path: filepath.Join(operations.basePath, name), node: operations.directoryNode, created: snapshotModelStageNode(operations.directoryNode)}, nil
}

func (operations *modelStageOperations) CreateFile(ctx context.Context, directory macStageDirectory, name string, mode os.FileMode) (macStageFile, error) {
	directoryHandle, ok := directory.(*modelStageDirectoryHandle)
	if err := ctx.Err(); err != nil || !ok || directoryHandle.node != operations.currentDirectoryNode || mode != 0o600 || !validStageChildName(name) {
		return nil, errMacStage
	}
	operations.fileNode = operations.newNode(modelStageRegular, mode)
	operations.currentFileNode = operations.fileNode
	operations.directoryNode.revision++
	return &modelStageFileHandle{node: operations.fileNode, created: snapshotModelStageNode(operations.fileNode)}, nil
}

func (operations *modelStageOperations) AdmitFile(ctx context.Context, directory macStageDirectory, file macStageFile, _ string) (int64, error) {
	directoryHandle, directoryOK := directory.(*modelStageDirectoryHandle)
	fileHandle, fileOK := file.(*modelStageFileHandle)
	if err := ctx.Err(); err != nil || !directoryOK || !fileOK || directoryHandle.node != operations.currentDirectoryNode ||
		fileHandle.node != operations.currentFileNode || fileHandle.node.kind != modelStageRegular || fileHandle.node.mode != 0o600 ||
		fileHandle.node.links != 1 || len(fileHandle.node.data) == 0 {
		return 0, errMacStage
	}
	fileHandle.admitted = snapshotModelStageNode(fileHandle.node)
	return int64(len(fileHandle.node.data)), nil
}

func (operations *modelStageOperations) AdmitDirectory(ctx context.Context, parent macStageDirectory, directory macStageDirectory, _ string) error {
	parentHandle, parentOK := parent.(*modelStageDirectoryHandle)
	directoryHandle, directoryOK := directory.(*modelStageDirectoryHandle)
	if err := ctx.Err(); err != nil || !parentOK || !directoryOK || parentHandle.node != operations.parentNode ||
		directoryHandle.node != operations.currentDirectoryNode || directoryHandle.node.kind != modelStageDirectory || directoryHandle.node.mode != 0o700 {
		return errMacStage
	}
	directoryHandle.admitted = snapshotModelStageNode(directoryHandle.node)
	parentHandle.admitted = snapshotModelStageNode(parentHandle.node)
	return nil
}

func (operations *modelStageOperations) ValidateParent(ctx context.Context, parent macStageDirectory) error {
	handle, ok := parent.(*modelStageDirectoryHandle)
	if err := ctx.Err(); err != nil || !ok || handle.closed || handle.node != operations.parentNode ||
		handle.created.identity != operations.parentNode.identity || handle.created.kind != operations.parentNode.kind ||
		handle.created.mode != operations.parentNode.mode || handle.created.uid != operations.parentNode.uid {
		return errMacStage
	}
	return nil
}

func (operations *modelStageOperations) ValidateDirectoryIdentity(ctx context.Context, parent macStageDirectory, directory macStageDirectory, _ string) error {
	parentHandle, parentOK := parent.(*modelStageDirectoryHandle)
	directoryHandle, directoryOK := directory.(*modelStageDirectoryHandle)
	if err := ctx.Err(); err != nil || !parentOK || !directoryOK || parentHandle.closed || directoryHandle.closed ||
		directoryHandle.node != operations.currentDirectoryNode || directoryHandle.created.identity != operations.currentDirectoryNode.identity ||
		operations.currentDirectoryNode.kind != modelStageDirectory || operations.currentDirectoryNode.mode != 0o700 {
		return errMacStage
	}
	return nil
}

func (operations *modelStageOperations) ValidateDirectory(ctx context.Context, parent macStageDirectory, directory macStageDirectory, name string) error {
	if err := operations.ValidateDirectoryIdentity(ctx, parent, directory, name); err != nil {
		return err
	}
	handle := directory.(*modelStageDirectoryHandle)
	if handle.admitted != snapshotModelStageNode(operations.currentDirectoryNode) {
		return errMacStage
	}
	return nil
}

func (operations *modelStageOperations) ValidateFile(ctx context.Context, directory macStageDirectory, file macStageFile, _ string) (int64, error) {
	directoryHandle, directoryOK := directory.(*modelStageDirectoryHandle)
	fileHandle, fileOK := file.(*modelStageFileHandle)
	if err := ctx.Err(); err != nil || !directoryOK || !fileOK || directoryHandle.closed || fileHandle.closed ||
		directoryHandle.node != operations.currentDirectoryNode || fileHandle.node != operations.currentFileNode ||
		fileHandle.admitted != snapshotModelStageNode(operations.currentFileNode) || operations.currentFileNode.kind != modelStageRegular ||
		operations.currentFileNode.mode != 0o600 || operations.currentFileNode.links != 1 {
		return 0, errMacStage
	}
	return int64(len(operations.currentFileNode.data)), nil
}

func (operations *modelStageOperations) ReadDirectory(ctx context.Context, directory macStageDirectory) ([]string, error) {
	handle, ok := directory.(*modelStageDirectoryHandle)
	if err := ctx.Err(); err != nil || !ok || handle.closed || handle.node != operations.currentDirectoryNode {
		return nil, errMacStage
	}
	if operations.currentDirectoryNode.sentinel {
		return []string{"do-not-delete"}, nil
	}
	if operations.currentFileNode != nil {
		return []string{"Tailscale-1.100.1-macos.pkg"}, nil
	}
	return nil, nil
}

func (operations *modelStageOperations) RemoveFile(ctx context.Context, directory macStageDirectory, file macStageFile, _ string) error {
	operations.removeFileCalls++
	if operations.removeFileErr != nil {
		return operations.removeFileErr
	}
	directoryHandle, directoryOK := directory.(*modelStageDirectoryHandle)
	fileHandle, fileOK := file.(*modelStageFileHandle)
	if err := ctx.Err(); err != nil || !directoryOK || !fileOK || directoryHandle.node != operations.currentDirectoryNode ||
		fileHandle.node != operations.currentFileNode || fileHandle.created.identity != operations.currentFileNode.identity {
		return errMacStage
	}
	if err := fileHandle.Close(); err != nil {
		return err
	}
	operations.currentFileNode = nil
	operations.directoryNode.revision++
	return nil
}

func (operations *modelStageOperations) RemoveDirectory(ctx context.Context, parent macStageDirectory, directory macStageDirectory, _ string) error {
	operations.removeDirectoryCalls++
	parentHandle, parentOK := parent.(*modelStageDirectoryHandle)
	directoryHandle, directoryOK := directory.(*modelStageDirectoryHandle)
	if err := ctx.Err(); err != nil || !parentOK || !directoryOK || parentHandle.node != operations.parentNode ||
		directoryHandle.node != operations.currentDirectoryNode || directoryHandle.created.identity != operations.currentDirectoryNode.identity ||
		operations.currentFileNode != nil || operations.currentDirectoryNode.sentinel {
		return errMacStage
	}
	if err := directoryHandle.Close(); err != nil {
		return err
	}
	operations.currentDirectoryNode = nil
	operations.parentNode.revision++
	parentHandle.admitted = snapshotModelStageNode(parentHandle.node)
	return nil
}

func (operations *modelStageOperations) replaceFile(kind modelStageKind) {
	replacement := operations.newNode(kind, 0o600)
	replacement.data = []byte("replacement")
	replacement.sentinel = true
	operations.currentFileNode = replacement
	operations.replacementNode = replacement
	operations.directoryNode.revision++
}

func (operations *modelStageOperations) replaceDirectory() {
	replacement := operations.newNode(modelStageDirectory, 0o700)
	replacement.sentinel = true
	operations.currentDirectoryNode = replacement
	operations.replacementNode = replacement
	operations.parentNode.revision++
}

func (operations *modelStageOperations) swapFileForABA() *modelStageNode {
	operations.abaAdmittedFile = operations.currentFileNode
	replacement := operations.newNode(modelStageRegular, 0o600)
	replacement.data = []byte("replacement")
	operations.currentFileNode = replacement
	return replacement
}

func (operations *modelStageOperations) restoreFileAfterABA() {
	operations.currentFileNode = operations.abaAdmittedFile
	operations.abaAdmittedFile = nil
}

func newModelStagedPackage(t *testing.T) (*stagedMacPKG, *modelStageOperations) {
	t.Helper()
	payload := []byte("fixture package")
	digest := sha256.Sum256(payload)
	release := MacRelease{
		Version:     "1.100.1",
		PKGURL:      StablePackagesURL + "Tailscale-1.100.1-macos.pkg",
		ChecksumURL: StablePackagesURL + "Tailscale-1.100.1-macos.pkg.sha256",
	}
	operations := newModelStageOperations(t.TempDir())
	client := packageBodyClient(release.PKGURL, int64(len(payload)), bytes.NewReader(payload))
	stage, err := stageMacPKGWithOperations(
		context.Background(), client, release, hex.EncodeToString(digest[:]), operations,
		macStageDirectoryPrefix+"0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	return stage, operations
}

type boundaryReplacementStageOperations struct {
	macStageOperations
	model     *modelStageOperations
	boundary  string
	triggered bool
	armed     bool
	callCount int
	parentUse int
	readUse   int
}

type revalidationBoundaryOperations struct {
	macStageOperations
	model          *modelStageOperations
	barrier        string
	triggered      bool
	parentCalls    int
	directoryCalls int
	fileCalls      int
	readCalls      int
}

func (operations *revalidationBoundaryOperations) replace() {
	if operations.triggered {
		return
	}
	operations.triggered = true
	operations.model.replaceFile(modelStageRegular)
}

func (operations *revalidationBoundaryOperations) ValidateParent(ctx context.Context, parent macStageDirectory) error {
	operations.parentCalls++
	if operations.barrier == "parent precheck" && operations.parentCalls == 1 ||
		operations.barrier == "final parent return" && operations.parentCalls == 2 {
		operations.replace()
	}
	return operations.macStageOperations.ValidateParent(ctx, parent)
}

func (operations *revalidationBoundaryOperations) ValidateDirectory(ctx context.Context, parent macStageDirectory, directory macStageDirectory, name string) error {
	operations.directoryCalls++
	if operations.barrier == "directory precheck" && operations.directoryCalls == 1 ||
		operations.barrier == "directory postcheck" && operations.directoryCalls == 2 {
		operations.replace()
	}
	return operations.macStageOperations.ValidateDirectory(ctx, parent, directory, name)
}

func (operations *revalidationBoundaryOperations) ReadDirectory(ctx context.Context, directory macStageDirectory) ([]string, error) {
	operations.readCalls++
	if operations.barrier == "enumeration" && operations.readCalls == 1 {
		operations.replace()
	}
	return operations.macStageOperations.ReadDirectory(ctx, directory)
}

func (operations *revalidationBoundaryOperations) ValidateFile(ctx context.Context, directory macStageDirectory, file macStageFile, name string) (int64, error) {
	operations.fileCalls++
	if operations.barrier == "file prehash" && operations.fileCalls == 1 ||
		operations.barrier == "file posthash" && operations.fileCalls == 2 {
		operations.replace()
	}
	return operations.macStageOperations.ValidateFile(ctx, directory, unwrapRevalidationBoundaryFile(file), name)
}

func (operations *revalidationBoundaryOperations) RemoveFile(ctx context.Context, directory macStageDirectory, file macStageFile, name string) error {
	return operations.macStageOperations.RemoveFile(ctx, directory, unwrapRevalidationBoundaryFile(file), name)
}

type revalidationBoundaryFile struct {
	macStageFile
	operations *revalidationBoundaryOperations
}

func (file *revalidationBoundaryFile) ReadAt(value []byte, offset int64) (int, error) {
	if file.operations.barrier == "descriptor hash" {
		file.operations.replace()
	}
	return file.macStageFile.ReadAt(value, offset)
}

func unwrapRevalidationBoundaryFile(file macStageFile) macStageFile {
	if wrapped, ok := file.(*revalidationBoundaryFile); ok {
		return wrapped.macStageFile
	}
	return file
}

func (operations *boundaryReplacementStageOperations) triggerDirectory() error {
	operations.triggered = true
	operations.model.replaceDirectory()
	return errors.New("injected exact-path directory replacement")
}

func (operations *boundaryReplacementStageOperations) triggerFile() error {
	operations.triggered = true
	operations.model.replaceFile(modelStageRegular)
	return errors.New("injected exact-path file replacement")
}

func (operations *boundaryReplacementStageOperations) CreateDirectory(ctx context.Context, parent macStageDirectory, name string, mode os.FileMode) (macStageDirectory, error) {
	directory, err := operations.macStageOperations.CreateDirectory(ctx, parent, name, mode)
	if err == nil && operations.boundary == "create directory" {
		return directory, operations.triggerDirectory()
	}
	return directory, err
}

func (operations *boundaryReplacementStageOperations) CreateFile(ctx context.Context, directory macStageDirectory, name string, mode os.FileMode) (macStageFile, error) {
	file, err := operations.macStageOperations.CreateFile(ctx, directory, name, mode)
	if err == nil && operations.boundary == "create file" {
		return file, operations.triggerFile()
	}
	return file, err
}

func (operations *boundaryReplacementStageOperations) AdmitFile(ctx context.Context, directory macStageDirectory, file macStageFile, name string) (int64, error) {
	if operations.boundary == "admit file" {
		return 0, operations.triggerFile()
	}
	return operations.macStageOperations.AdmitFile(ctx, directory, file, name)
}

func (operations *boundaryReplacementStageOperations) AdmitDirectory(ctx context.Context, parent macStageDirectory, directory macStageDirectory, name string) error {
	if operations.boundary == "admit directory" {
		return operations.triggerDirectory()
	}
	return operations.macStageOperations.AdmitDirectory(ctx, parent, directory, name)
}

func (operations *boundaryReplacementStageOperations) ValidateParent(ctx context.Context, parent macStageDirectory) error {
	operations.callCount++
	operations.parentUse++
	if operations.boundary == "validate parent" && !operations.triggered ||
		operations.armed && operations.boundary == "cleanup parent validation" && operations.parentUse == 3 {
		_ = operations.triggerDirectory()
	}
	return operations.macStageOperations.ValidateParent(ctx, parent)
}

func (operations *boundaryReplacementStageOperations) ValidateDirectory(ctx context.Context, parent macStageDirectory, directory macStageDirectory, name string) error {
	operations.callCount++
	if operations.boundary == "validate directory" && !operations.triggered {
		_ = operations.triggerDirectory()
	}
	return operations.macStageOperations.ValidateDirectory(ctx, parent, directory, name)
}

func (operations *boundaryReplacementStageOperations) ValidateDirectoryIdentity(ctx context.Context, parent macStageDirectory, directory macStageDirectory, name string) error {
	operations.callCount++
	if operations.armed && operations.boundary == "cleanup directory identity" {
		_ = operations.triggerDirectory()
	}
	return operations.macStageOperations.ValidateDirectoryIdentity(ctx, parent, directory, name)
}

func (operations *boundaryReplacementStageOperations) ReadDirectory(ctx context.Context, directory macStageDirectory) ([]string, error) {
	operations.callCount++
	operations.readUse++
	if operations.boundary == "read directory" && !operations.triggered ||
		operations.armed && operations.boundary == "cleanup read directory" && operations.readUse == 2 {
		_ = operations.triggerDirectory()
	}
	return operations.macStageOperations.ReadDirectory(ctx, directory)
}

func (operations *boundaryReplacementStageOperations) ValidateFile(ctx context.Context, directory macStageDirectory, file macStageFile, name string) (int64, error) {
	operations.callCount++
	if operations.boundary == "validate file" && !operations.triggered {
		_ = operations.triggerFile()
	}
	return operations.macStageOperations.ValidateFile(ctx, directory, file, name)
}

func (operations *boundaryReplacementStageOperations) RemoveFile(ctx context.Context, directory macStageDirectory, file macStageFile, name string) error {
	operations.callCount++
	if operations.armed && operations.boundary == "cleanup remove file" {
		_ = operations.triggerFile()
	}
	return operations.macStageOperations.RemoveFile(ctx, directory, file, name)
}

func (operations *boundaryReplacementStageOperations) RemoveDirectory(ctx context.Context, parent macStageDirectory, directory macStageDirectory, name string) error {
	operations.callCount++
	if operations.armed && operations.boundary == "cleanup remove directory" {
		_ = operations.triggerDirectory()
	}
	return operations.macStageOperations.RemoveDirectory(ctx, parent, directory, name)
}

var _ macStageOperations = (*modelStageOperations)(nil)
var _ macStageDirectory = (*modelStageDirectoryHandle)(nil)
var _ macStageFile = (*modelStageFileHandle)(nil)

type replacementOnAdmitStageOperations struct {
	macStageOperations
	replacementPath              string
	admittedPath                 string
	directoryIdentityValidations int
	removeFileCalls              int
	removeDirectoryCalls         int
	directoryReplaced            bool
}

func (operations *replacementOnAdmitStageOperations) AdmitFile(
	_ context.Context,
	directory macStageDirectory,
	_ macStageFile,
	_ string,
) (int64, error) {
	original := directory.Path()
	replacement := original + "-replacement"
	if err := os.Mkdir(replacement, 0o700); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(replacement, "do-not-delete"), []byte("replacement"), 0o600); err != nil {
		return 0, err
	}
	operations.replacementPath = replacement
	operations.admittedPath = original
	operations.directoryReplaced = true
	return 0, errors.New("injected identity failure")
}

func (operations *replacementOnAdmitStageOperations) ValidateDirectoryIdentity(
	ctx context.Context,
	parent macStageDirectory,
	directory macStageDirectory,
	name string,
) error {
	operations.directoryIdentityValidations++
	if operations.directoryReplaced {
		return errors.New("injected renamed directory replacement")
	}
	return operations.macStageOperations.ValidateDirectoryIdentity(ctx, parent, directory, name)
}

func (operations *replacementOnAdmitStageOperations) RemoveFile(
	ctx context.Context,
	directory macStageDirectory,
	file macStageFile,
	name string,
) error {
	operations.removeFileCalls++
	return operations.macStageOperations.RemoveFile(ctx, directory, file, name)
}

func (operations *replacementOnAdmitStageOperations) RemoveDirectory(
	ctx context.Context,
	parent macStageDirectory,
	directory macStageDirectory,
	name string,
) error {
	operations.removeDirectoryCalls++
	return operations.macStageOperations.RemoveDirectory(ctx, parent, directory, name)
}

func macTransactionClient(release MacRelease, payload []byte, digest string) (*http.Client, *[]string) {
	requested := &[]string{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*requested = append(*requested, request.URL.String())
		var body io.Reader
		switch request.URL.String() {
		case StablePackagesURL:
			body = strings.NewReader(`<a href="Tailscale-1.100.1-macos.pkg">download</a>`)
		case release.ChecksumURL:
			body = strings.NewReader(digest + "\n")
		case release.PKGURL:
			body = bytes.NewReader(payload)
		default:
			return nil, errors.New("unexpected request")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(body), Request: request, ContentLength: -1}, nil
	})}
	return client, requested
}

type countingPathPhaseGuard struct {
	revalidations int
}

func (guard *countingPathPhaseGuard) Path() string { return "/fixture/admitted.pkg" }
func (guard *countingPathPhaseGuard) Revalidate(context.Context) error {
	guard.revalidations++
	return nil
}
