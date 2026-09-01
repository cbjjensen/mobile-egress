package tailscale

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	fixedTailscaleBundlePath     = "/Applications/Tailscale.app"
	fixedTailscaleExecutablePath = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"

	maximumIdentityTrustOutput = 4 << 20

	tailscaleAppRequirement = `=(anchor apple generic and identifier "io.tailscale.ipn.macsys" and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = "W5364U7YZB") or (anchor apple generic and identifier "io.tailscale.ipn.macos" and certificate leaf[field.1.2.840.113635.100.6.1.9] exists and certificate leaf[subject.OU] = "W5364U7YZB")`
)

var (
	errTailscaleAppVerification = errors.New("Tailscale application verification failed")
	errTailscaleAppCleanup      = errors.New("Tailscale application verification cleanup failed")
	errIdentityTrustOutput      = errors.New("Tailscale application trust output limit exceeded")
)

func newIdentityTrustEnvironment() []string {
	return []string{
		"LC_ALL=C",
		"LANG=C",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
	}
}

type DarwinVariant uint8

const (
	DarwinStandalone DarwinVariant = iota + 1
	DarwinAppStore
)

type DarwinInstallation struct {
	BundlePath string
	Executable string
	BundleID   string
	Variant    DarwinVariant
	guard      appExecutionGuard
}

type codeIdentity struct {
	BundleID string
	TeamID   string
}

type appExecutionGuard interface {
	Revalidate(context.Context) error
	BundlePath() string
	ExecutablePath() string
	Close() error
}

type verifiedDarwinApp struct {
	Identity codeIdentity
	Guard    appExecutionGuard
}

type darwinBundleVerifier func(
	context.Context,
	string,
	string,
	string,
) (verifiedDarwinApp, error)

func parseCodeSignIdentity(output []byte) (codeIdentity, error) {
	if len(output) == 0 || len(output) > maximumIdentityTrustOutput || bytes.IndexByte(output, 0) >= 0 || !utf8.Valid(output) {
		return codeIdentity{}, errTailscaleAppVerification
	}
	for index, value := range output {
		if value == '\r' && (index+1 >= len(output) || output[index+1] != '\n') {
			return codeIdentity{}, errTailscaleAppVerification
		}
	}
	normalized := strings.ReplaceAll(string(output), "\r\n", "\n")
	var identity codeIdentity
	identifierCount := 0
	teamCount := 0
	for _, line := range strings.Split(normalized, "\n") {
		switch {
		case strings.HasPrefix(line, "Identifier="):
			identifierCount++
			identity.BundleID = strings.TrimPrefix(line, "Identifier=")
		case strings.HasPrefix(line, "TeamIdentifier="):
			teamCount++
			identity.TeamID = strings.TrimPrefix(line, "TeamIdentifier=")
		case strings.Contains(strings.ToLower(line), "identifier"):
			return codeIdentity{}, errTailscaleAppVerification
		}
	}
	if identifierCount != 1 || teamCount != 1 ||
		identity.BundleID == "" || strings.TrimSpace(identity.BundleID) != identity.BundleID ||
		identity.TeamID == "" || identity.TeamID == "not set" || strings.TrimSpace(identity.TeamID) != identity.TeamID {
		return codeIdentity{}, errTailscaleAppVerification
	}
	return identity, nil
}

func findDarwinInstallation(ctx context.Context, verifier darwinBundleVerifier) (DarwinInstallation, error) {
	if verifier == nil {
		return DarwinInstallation{}, errTailscaleAppVerification
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return DarwinInstallation{}, errTailscaleAppVerification
	}
	verified, verifyErr := verifier(
		ctx,
		fixedTailscaleBundlePath,
		fixedTailscaleExecutablePath,
		tailscaleAppRequirement,
	)
	verifierCleanup := errors.Is(verifyErr, errTailscaleAppCleanup)
	closeRejectedVerifierResult := func() (DarwinInstallation, error) {
		if verified.Guard != nil && verified.Guard.Close() != nil {
			return DarwinInstallation{}, errTailscaleAppCleanup
		}
		if verifierCleanup {
			return DarwinInstallation{}, errTailscaleAppCleanup
		}
		return DarwinInstallation{}, errTailscaleAppVerification
	}
	if ctx.Err() != nil {
		return closeRejectedVerifierResult()
	}
	if verifyErr != nil {
		return closeRejectedVerifierResult()
	}
	if verified.Guard == nil {
		return DarwinInstallation{}, errTailscaleAppVerification
	}
	reject := func() (DarwinInstallation, error) {
		if verified.Guard.Close() != nil {
			return DarwinInstallation{}, errTailscaleAppCleanup
		}
		return DarwinInstallation{}, errTailscaleAppVerification
	}
	if verified.Identity.TeamID != "W5364U7YZB" ||
		verified.Guard.BundlePath() != fixedTailscaleBundlePath ||
		verified.Guard.ExecutablePath() != fixedTailscaleExecutablePath {
		return reject()
	}
	var variant DarwinVariant
	switch verified.Identity.BundleID {
	case "io.tailscale.ipn.macsys":
		variant = DarwinStandalone
	case "io.tailscale.ipn.macos":
		variant = DarwinAppStore
	default:
		return reject()
	}
	if verified.Guard.Revalidate(ctx) != nil || ctx.Err() != nil {
		return reject()
	}
	return DarwinInstallation{
		BundlePath: fixedTailscaleBundlePath,
		Executable: fixedTailscaleExecutablePath,
		BundleID:   verified.Identity.BundleID,
		Variant:    variant,
		guard:      verified.Guard,
	}, nil
}

type identityPathKind uint8

const (
	identityPathDirectory identityPathKind = iota + 1
	identityPathRegular
	identityPathOther
)

type identityTimestamp struct {
	Seconds     int64
	Nanoseconds int64
}

type identityPathObservation struct {
	Path        string
	Present     bool
	ExactCase   bool
	SymlinkFree bool
	Kind        identityPathKind
	Executable  bool

	Device     uint64
	Inode      uint64
	Generation uint64
	UID        uint32
	GID        uint32
	Mode       uint32
	LinkCount  uint64
	DeviceType uint64
	Size       int64
	Flags      uint32
	BirthTime  identityTimestamp
	ChangeTime identityTimestamp
	ModifyTime identityTimestamp
	Digest     [32]byte
}

type identityAppObservation struct {
	Bundle     identityPathObservation
	Executable identityPathObservation
}

func validateIdentityAppObservation(captured, current identityAppObservation) error {
	if !validIdentityAppObservation(captured) || !validIdentityAppObservation(current) || captured != current {
		return errTailscaleAppVerification
	}
	return nil
}

func validIdentityAppObservation(observation identityAppObservation) bool {
	bundle := observation.Bundle
	executable := observation.Executable
	return bundle.Path != "" && executable.Path != "" &&
		bundle.Present && bundle.ExactCase && bundle.SymlinkFree && bundle.Kind == identityPathDirectory &&
		bundle.Mode&0o170000 == 0o040000 && bundle.LinkCount > 0 &&
		executable.Present && executable.ExactCase && executable.SymlinkFree &&
		executable.Kind == identityPathRegular && executable.Executable && executable.Mode&0o170000 == 0o100000 &&
		executable.Mode&0o111 != 0 && executable.LinkCount == 1 && executable.Size > 0 && executable.Digest != [32]byte{}
}

type identityAppPathState interface {
	Observe(context.Context) (identityAppObservation, error)
	CloseExecutable() error
	CloseBundle() error
}

type identityAppPathOpener func(
	context.Context,
	string,
	string,
) (identityAppPathState, identityAppObservation, error)

type identityAppGuard struct {
	bundlePath     string
	executablePath string
	state          identityAppPathState
	captured       identityAppObservation

	useMu       sync.RWMutex
	closeOnce   sync.Once
	closing     bool
	closed      bool
	closeResult error
}

func newIdentityAppExecutionGuard(
	ctx context.Context,
	bundlePath string,
	executablePath string,
	opener identityAppPathOpener,
) (appExecutionGuard, error) {
	if opener == nil {
		return nil, errTailscaleAppVerification
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, errTailscaleAppVerification
	}
	state, captured, openErr := opener(ctx, bundlePath, executablePath)
	return newIdentityAppExecutionGuardFromState(ctx, bundlePath, executablePath, state, captured, openErr)
}

func newIdentityAppExecutionGuardFromState(
	ctx context.Context,
	bundlePath string,
	executablePath string,
	state identityAppPathState,
	captured identityAppObservation,
	openErr error,
) (appExecutionGuard, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if state == nil {
		return nil, errTailscaleAppVerification
	}
	guard := &identityAppGuard{
		bundlePath: bundlePath, executablePath: executablePath,
		state: state, captured: captured,
	}
	fail := func() (appExecutionGuard, error) {
		if guard.Close() != nil {
			return nil, errTailscaleAppCleanup
		}
		return nil, errTailscaleAppVerification
	}
	if openErr != nil || ctx.Err() != nil || bundlePath == "" || executablePath == "" ||
		captured.Bundle.Path != bundlePath || captured.Executable.Path != executablePath ||
		validateIdentityAppObservation(captured, captured) != nil {
		return fail()
	}
	current, err := state.Observe(ctx)
	if err != nil || ctx.Err() != nil || validateIdentityAppObservation(captured, current) != nil {
		return fail()
	}
	return guard, nil
}

func (guard *identityAppGuard) Revalidate(ctx context.Context) error {
	if guard == nil {
		return errTailscaleAppVerification
	}
	guard.useMu.RLock()
	defer guard.useMu.RUnlock()
	if guard.closing || guard.closed || guard.state == nil {
		return errTailscaleAppVerification
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return errTailscaleAppVerification
	}
	current, err := guard.state.Observe(ctx)
	if err != nil || ctx.Err() != nil || validateIdentityAppObservation(guard.captured, current) != nil {
		return errTailscaleAppVerification
	}
	return nil
}

func (guard *identityAppGuard) BundlePath() string {
	if guard == nil {
		return ""
	}
	guard.useMu.RLock()
	defer guard.useMu.RUnlock()
	if guard.closing || guard.closed {
		return ""
	}
	return guard.bundlePath
}

func (guard *identityAppGuard) ExecutablePath() string {
	if guard == nil {
		return ""
	}
	guard.useMu.RLock()
	defer guard.useMu.RUnlock()
	if guard.closing || guard.closed {
		return ""
	}
	return guard.executablePath
}

func (guard *identityAppGuard) Close() error {
	if guard == nil {
		return errTailscaleAppCleanup
	}
	guard.closeOnce.Do(func() {
		guard.useMu.Lock()
		guard.closing = true
		guard.useMu.Unlock()

		executableErr := guard.state.CloseExecutable()
		bundleErr := guard.state.CloseBundle()

		guard.useMu.Lock()
		guard.closed = true
		if executableErr != nil || bundleErr != nil {
			guard.closeResult = errTailscaleAppCleanup
		}
		guard.useMu.Unlock()
	})
	guard.useMu.RLock()
	defer guard.useMu.RUnlock()
	return guard.closeResult
}

type identityTrustCommandRunner interface {
	Run(context.Context, string, []string, []string, int) ([]byte, error)
}

func verifyDarwinAppWithDependencies(
	ctx context.Context,
	bundlePath string,
	executablePath string,
	requirement string,
	opener identityAppPathOpener,
	runner identityTrustCommandRunner,
) (verifiedDarwinApp, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if bundlePath != fixedTailscaleBundlePath || executablePath != fixedTailscaleExecutablePath ||
		requirement != tailscaleAppRequirement || opener == nil || runner == nil {
		return verifiedDarwinApp{}, errTailscaleAppVerification
	}
	guard, err := newIdentityAppExecutionGuard(ctx, bundlePath, executablePath, opener)
	if err != nil {
		return verifiedDarwinApp{}, err
	}
	fail := func() (verifiedDarwinApp, error) {
		if guard.Close() != nil {
			return verifiedDarwinApp{}, errTailscaleAppCleanup
		}
		return verifiedDarwinApp{}, errTailscaleAppVerification
	}

	if _, err := runIdentityTrustPhase(ctx, guard, runner, "/usr/bin/codesign", []string{
		"--verify", "--deep", "--strict", "--verbose=4", "-R", requirement, bundlePath,
	}); err != nil {
		return fail()
	}
	if _, err := runIdentityTrustPhase(ctx, guard, runner, "/usr/sbin/spctl", []string{
		"--assess", "--type", "execute", bundlePath,
	}); err != nil {
		return fail()
	}
	display, err := runIdentityTrustPhase(ctx, guard, runner, "/usr/bin/codesign", []string{
		"--display", "--verbose=4", bundlePath,
	})
	if err != nil || len(display) > maximumIdentityTrustOutput {
		return fail()
	}
	identity, err := parseCodeSignIdentity(display)
	if err != nil || guard.Revalidate(ctx) != nil || ctx.Err() != nil {
		return fail()
	}
	return verifiedDarwinApp{Identity: identity, Guard: guard}, nil
}

func runIdentityTrustPhase(
	ctx context.Context,
	guard appExecutionGuard,
	runner identityTrustCommandRunner,
	path string,
	args []string,
) ([]byte, error) {
	if guard == nil || runner == nil || guard.Revalidate(ctx) != nil {
		return nil, errTailscaleAppVerification
	}
	output, runErr := runner.Run(
		ctx,
		path,
		append([]string(nil), args...),
		newIdentityTrustEnvironment(),
		maximumIdentityTrustOutput,
	)
	revalidateErr := guard.Revalidate(ctx)
	if runErr != nil || revalidateErr != nil || len(output) > maximumIdentityTrustOutput {
		return nil, errTailscaleAppVerification
	}
	return output, nil
}
