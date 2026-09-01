package tailscale

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	errMacStage          = errors.New("Tailscale macOS PKG staging failed")
	errMacCleanupPending = errors.New("Tailscale installer cleanup is still in progress")
)

const macStageDirectoryPrefix = "mobile-egress-tailscale-"

// These handles expose no path-based mutation. The platform adapter retains
// their descriptors and is the sole authority for child operations.
type macStageDirectory interface {
	Path() string
	Close() error
}

type macStageFile interface {
	io.Writer
	io.ReaderAt
	Sync() error
	Close() error
}

type macStageOperations interface {
	ResolveTemporaryBase(context.Context) (string, error)
	OpenParent(context.Context, string) (macStageDirectory, error)
	CreateDirectory(context.Context, macStageDirectory, string, fs.FileMode) (macStageDirectory, error)
	CreateFile(context.Context, macStageDirectory, string, fs.FileMode) (macStageFile, error)
	AdmitFile(context.Context, macStageDirectory, macStageFile, string) (int64, error)
	AdmitDirectory(context.Context, macStageDirectory, macStageDirectory, string) error
	ValidateParent(context.Context, macStageDirectory) error
	ValidateDirectoryIdentity(context.Context, macStageDirectory, macStageDirectory, string) error
	ValidateDirectory(context.Context, macStageDirectory, macStageDirectory, string) error
	ValidateFile(context.Context, macStageDirectory, macStageFile, string) (int64, error)
	ReadDirectory(context.Context, macStageDirectory) ([]string, error)
	RemoveFile(context.Context, macStageDirectory, macStageFile, string) error
	RemoveDirectory(context.Context, macStageDirectory, macStageDirectory, string) error
}

type stagedMacPKG struct {
	directoryName string
	basename      string
	path          string
	operations    macStageOperations
	parent        macStageDirectory
	directory     macStageDirectory
	file          macStageFile
	size          int64
	digest        string

	useMu          sync.RWMutex
	removeOnce     sync.Once
	removeResult   error
	ready          bool
	cleanupStarted bool
	closed         bool
}

func resolveAndStageMacPKG(ctx context.Context, client *http.Client) (MacRelease, *stagedMacPKG, error) {
	return resolveAndStageMacPKGCore(ctx, client, func(
		stageContext context.Context,
		stageClient *http.Client,
		release MacRelease,
		digest string,
	) (*stagedMacPKG, error) {
		return stageMacPKG(stageContext, stageClient, release, digest)
	})
}

func resolveAndStageMacPKGWithOperations(
	ctx context.Context,
	client *http.Client,
	operations macStageOperations,
	directoryName string,
) (MacRelease, *stagedMacPKG, error) {
	if operations == nil || !validMacStageDirectoryName(directoryName) {
		return MacRelease{}, nil, errMacStage
	}
	return resolveAndStageMacPKGCore(ctx, client, func(
		stageContext context.Context,
		stageClient *http.Client,
		release MacRelease,
		digest string,
	) (*stagedMacPKG, error) {
		return stageMacPKGWithOperations(stageContext, stageClient, release, digest, operations, directoryName)
	})
}

func resolveAndStageMacPKGCore(
	ctx context.Context,
	client *http.Client,
	stage func(context.Context, *http.Client, MacRelease, string) (*stagedMacPKG, error),
) (MacRelease, *stagedMacPKG, error) {
	if stage == nil {
		return MacRelease{}, nil, errMacStage
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	page, err := downloadExactMacSmall(ctx, client, StablePackagesURL, "", maximumMacPackagePageBytes)
	if err != nil {
		return MacRelease{}, nil, errMacStage
	}
	release, err := ParseStableMacPackagePage(page)
	if err != nil {
		return MacRelease{}, nil, errMacStage
	}
	checksumBasename := path.Base(release.ChecksumURL)
	checksum, err := downloadExactMacSmall(ctx, client, release.ChecksumURL, checksumBasename, maximumMacChecksumBytes)
	if err != nil {
		return MacRelease{}, nil, errMacStage
	}
	digest, err := parseMacSHA256(checksum)
	if err != nil {
		return MacRelease{}, nil, errMacStage
	}
	stagedPackage, err := stage(ctx, client, release, digest)
	if err != nil {
		return MacRelease{}, stagedPackage, err
	}
	return release, stagedPackage, nil
}

func stageMacPKG(ctx context.Context, client *http.Client, release MacRelease, expectedDigest string) (*stagedMacPKG, error) {
	operations, err := newPlatformMacStageOperations()
	if err != nil {
		return nil, errMacStage
	}
	directoryName, err := newMacStageDirectoryName()
	if err != nil {
		return nil, errMacStage
	}
	return stageMacPKGWithOperations(ctx, client, release, expectedDigest, operations, directoryName)
}

func stageMacPKGWithOperations(
	ctx context.Context,
	client *http.Client,
	release MacRelease,
	expectedDigest string,
	operations macStageOperations,
	directoryName string,
) (*stagedMacPKG, error) {
	parsed, err := url.Parse(release.PKGURL)
	if err != nil {
		return nil, errMacStage
	}
	basename := path.Base(parsed.Path)
	if operations == nil || !validMacStageDirectoryName(directoryName) ||
		validateMacReleaseURL(release.PKGURL, basename) != nil ||
		release.ChecksumURL != release.PKGURL+".sha256" {
		return nil, errMacStage
	}

	stage := &stagedMacPKG{
		directoryName: directoryName,
		basename:      basename,
		operations:    operations,
		digest:        expectedDigest,
	}
	fail := func() (*stagedMacPKG, error) {
		if cleanupErr := stage.cleanupAfterFailure(); cleanupErr != nil {
			return stage, errMacCleanupPending
		}
		return nil, errMacStage
	}

	temporaryBase, err := operations.ResolveTemporaryBase(ctx)
	if err != nil || temporaryBase == "" || !filepath.IsAbs(temporaryBase) {
		return fail()
	}
	stage.path = filepath.Join(temporaryBase, directoryName, basename)
	stage.parent, err = operations.OpenParent(ctx, temporaryBase)
	if err != nil || stage.parent == nil {
		return fail()
	}
	stage.directory, err = operations.CreateDirectory(ctx, stage.parent, directoryName, 0o700)
	if err != nil || stage.directory == nil {
		return fail()
	}
	stage.file, err = operations.CreateFile(ctx, stage.directory, basename, 0o600)
	if err != nil || stage.file == nil {
		return fail()
	}
	if err := downloadPKG(ctx, client, release.PKGURL, stage.file, expectedDigest); err != nil {
		return fail()
	}
	stage.size, err = operations.AdmitFile(ctx, stage.directory, stage.file, basename)
	if err != nil || stage.size <= 0 {
		return fail()
	}
	digest, err := hashOpenStageFile(stage.file, stage.size)
	if err != nil || digest != expectedDigest {
		return fail()
	}
	stage.digest = digest
	if err := operations.AdmitDirectory(ctx, stage.parent, stage.directory, directoryName); err != nil {
		return fail()
	}
	stage.ready = true
	if err := stage.Revalidate(ctx); err != nil {
		return fail()
	}
	return stage, nil
}

func newMacStageDirectoryName() (string, error) {
	var random [16]byte
	if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
		return "", err
	}
	return macStageDirectoryPrefix + hex.EncodeToString(random[:]), nil
}

func validMacStageDirectoryName(name string) bool {
	if !strings.HasPrefix(name, macStageDirectoryPrefix) || len(name) != len(macStageDirectoryPrefix)+32 || filepath.Base(name) != name {
		return false
	}
	_, err := hex.DecodeString(name[len(macStageDirectoryPrefix):])
	return err == nil
}

func validStageChildName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}

func hasExactStageEntry(entries []string, name string) bool {
	for _, entry := range entries {
		if entry == name {
			return true
		}
	}
	return false
}

func (stage *stagedMacPKG) Path() string {
	if stage == nil {
		return ""
	}
	stage.useMu.RLock()
	defer stage.useMu.RUnlock()
	if !stage.ready || stage.cleanupStarted || stage.closed {
		return ""
	}
	return stage.path
}

func (stage *stagedMacPKG) Revalidate(ctx context.Context) error {
	if stage == nil {
		return errMacStage
	}
	stage.useMu.RLock()
	defer stage.useMu.RUnlock()
	if !stage.ready || stage.cleanupStarted || stage.closed {
		return errMacStage
	}
	return stage.revalidateLocked(ctx)
}

func (stage *stagedMacPKG) revalidateLocked(ctx context.Context) error {
	if stage.operations == nil || stage.parent == nil || stage.directory == nil || stage.file == nil {
		return errMacStage
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return errMacStage
	}
	if stage.operations.ValidateParent(ctx, stage.parent) != nil ||
		stage.operations.ValidateDirectory(ctx, stage.parent, stage.directory, stage.directoryName) != nil {
		return errMacStage
	}
	entries, err := stage.operations.ReadDirectory(ctx, stage.directory)
	if err != nil {
		return errMacStage
	}
	sort.Strings(entries)
	if len(entries) != 1 || entries[0] != stage.basename {
		return errMacStage
	}
	size, err := stage.operations.ValidateFile(ctx, stage.directory, stage.file, stage.basename)
	if err != nil || size != stage.size {
		return errMacStage
	}
	digest, err := hashOpenStageFile(stage.file, size)
	if err != nil || digest != stage.digest {
		return errMacStage
	}
	if sizeAfter, err := stage.operations.ValidateFile(ctx, stage.directory, stage.file, stage.basename); err != nil || sizeAfter != stage.size {
		return errMacStage
	}
	if stage.operations.ValidateDirectory(ctx, stage.parent, stage.directory, stage.directoryName) != nil ||
		stage.operations.ValidateParent(ctx, stage.parent) != nil {
		return errMacStage
	}
	return nil
}

func hashOpenStageFile(file macStageFile, size int64) (string, error) {
	if file == nil || size < 0 {
		return "", errMacStage
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(file, 0, size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (stage *stagedMacPKG) RemoveAfterQuiescence() error {
	if stage == nil {
		return errMacStage
	}
	stage.removeOnce.Do(func() {
		stage.useMu.Lock()
		defer stage.useMu.Unlock()
		if !stage.ready || stage.closed || stage.cleanupStarted || stage.revalidateLocked(context.Background()) != nil {
			stage.ready = false
			stage.cleanupStarted = true
			stage.removeResult = errMacCleanupPending
			return
		}
		stage.ready = false
		stage.cleanupStarted = true
		stage.removeResult = stage.removeResourcesLocked()
	})
	return stage.removeResult
}

func (stage *stagedMacPKG) cleanupAfterFailure() error {
	if stage == nil {
		return errMacCleanupPending
	}
	stage.removeOnce.Do(func() {
		stage.useMu.Lock()
		defer stage.useMu.Unlock()
		stage.ready = false
		stage.cleanupStarted = true
		stage.removeResult = stage.removeResourcesLocked()
	})
	return stage.removeResult
}

func (stage *stagedMacPKG) removeResourcesLocked() error {
	if stage.operations == nil {
		return errMacCleanupPending
	}
	ctx := context.Background()
	if stage.parent != nil && stage.operations.ValidateParent(ctx, stage.parent) != nil {
		return errMacCleanupPending
	}
	if stage.directory != nil {
		if stage.parent == nil || stage.operations.ValidateDirectoryIdentity(ctx, stage.parent, stage.directory, stage.directoryName) != nil {
			return errMacCleanupPending
		}
		if stage.file != nil {
			if err := stage.operations.RemoveFile(ctx, stage.directory, stage.file, stage.basename); err != nil {
				return errMacCleanupPending
			}
			stage.file = nil
		}
		entries, err := stage.operations.ReadDirectory(ctx, stage.directory)
		if err != nil || len(entries) != 0 {
			return errMacCleanupPending
		}
		if err := stage.operations.RemoveDirectory(ctx, stage.parent, stage.directory, stage.directoryName); err != nil {
			return errMacCleanupPending
		}
		stage.directory = nil
	}
	if stage.parent != nil {
		if err := stage.parent.Close(); err != nil {
			return errMacCleanupPending
		}
		stage.parent = nil
	}
	stage.closed = true
	return nil
}

type stagedPathGuard interface {
	Path() string
	Revalidate(context.Context) error
}

func runGuardedPathPhase(ctx context.Context, guard stagedPathGuard, consumer func(string) error) error {
	if guard == nil || consumer == nil || guard.Revalidate(ctx) != nil {
		return errMacStage
	}
	consumerErr := consumer(guard.Path())
	if guard.Revalidate(ctx) != nil || consumerErr != nil {
		return errMacStage
	}
	return nil
}
