package mackeychainharness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type validatedSigningContext struct {
	repositoryRoot        string
	profilePath           string
	workspace             string
	entitlementsPath      string
	applicationIdentifier string
	teamIdentifier        string
	identity              signingIdentity
}

func prepareSigningContext(ctx context.Context, runner Runner, config Config) (validatedSigningContext, func(), error) {
	if runner == nil {
		return validatedSigningContext{}, func() {}, errors.New("macOS Keychain harness command runner is required")
	}
	if !strings.HasPrefix(config.Identity, "Developer ID Application: ") {
		return validatedSigningContext{}, func() {}, errors.New("operator-supplied signing identity must be a Developer ID Application identity")
	}
	repositoryRoot, err := requireAbsoluteDirectory(config.RepositoryRoot, "repository root")
	if err != nil {
		return validatedSigningContext{}, func() {}, err
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot, "go.mod")); err != nil {
		return validatedSigningContext{}, func() {}, fmt.Errorf("repository root does not contain go.mod: %w", err)
	}
	profilePath, err := requireAbsoluteFile(config.ProfilePath, "Developer ID distribution provisioning profile")
	if err != nil {
		return validatedSigningContext{}, func() {}, err
	}

	workspace, removeWorkspace, err := prepareWorkspace(config.Workspace)
	if err != nil {
		return validatedSigningContext{}, func() {}, err
	}
	cleanup := func() {}
	if removeWorkspace {
		cleanup = func() { _ = os.RemoveAll(workspace) }
	}
	prepared := false
	defer func() {
		if !prepared {
			cleanup()
		}
	}()

	profilePlist := filepath.Join(workspace, "decoded-profile.plist")
	if _, err := runCommand(ctx, runner, Command{
		Name: "security",
		Args: []string{"cms", "-D", "-i", profilePath, "-o", profilePlist},
	}); err != nil {
		return validatedSigningContext{}, func() {}, fmt.Errorf("decode provisioning profile: %w", err)
	}
	applicationIdentifier, err := plistValue(ctx, runner, profilePlist, "Print :Entitlements:com.apple.application-identifier")
	if err != nil {
		return validatedSigningContext{}, func() {}, fmt.Errorf("read provisioned application identifier: %w", err)
	}
	teamIdentifier, err := plistValue(ctx, runner, profilePlist, "Print :Entitlements:com.apple.developer.team-identifier")
	if err != nil {
		return validatedSigningContext{}, func() {}, fmt.Errorf("read provisioned team identifier: %w", err)
	}
	profileAccessGroup, err := plistValue(ctx, runner, profilePlist, "Print :Entitlements:keychain-access-groups:0")
	if err != nil {
		return validatedSigningContext{}, func() {}, fmt.Errorf("read provisioned Keychain access group: %w", err)
	}
	getTaskAllow, err := plistValue(ctx, runner, profilePlist, "Print :Entitlements:com.apple.security.get-task-allow")
	if err != nil {
		return validatedSigningContext{}, func() {}, fmt.Errorf("read provisioning distribution entitlement: %w", err)
	}
	provisionsAllDevices, err := plistValue(ctx, runner, profilePlist, "Print :ProvisionsAllDevices")
	if err != nil {
		return validatedSigningContext{}, func() {}, fmt.Errorf("read provisioning distribution scope: %w", err)
	}
	if err := validateProvisionedIdentity(
		applicationIdentifier,
		teamIdentifier,
		profileAccessGroup,
		getTaskAllow,
		provisionsAllDevices,
		config.Identity,
	); err != nil {
		return validatedSigningContext{}, func() {}, err
	}
	profileCertificates, err := loadProfileCertificates(ctx, runner, profilePlist)
	if err != nil {
		return validatedSigningContext{}, func() {}, fmt.Errorf("read provisioned developer certificates: %w", err)
	}
	identity, err := resolveSigningIdentity(ctx, runner, config.Identity, teamIdentifier, profileCertificates, time.Now())
	if err != nil {
		return validatedSigningContext{}, func() {}, err
	}

	entitlementsPath := filepath.Join(workspace, "controller.entitlements.plist")
	if err := os.WriteFile(entitlementsPath, []byte(entitlementsPlist(applicationIdentifier, teamIdentifier)), 0o600); err != nil {
		return validatedSigningContext{}, func() {}, fmt.Errorf("write exact signed entitlements: %w", err)
	}
	prepared = true
	return validatedSigningContext{
		repositoryRoot: repositoryRoot, profilePath: profilePath, workspace: workspace,
		entitlementsPath: entitlementsPath, applicationIdentifier: applicationIdentifier,
		teamIdentifier: teamIdentifier, identity: identity,
	}, cleanup, nil
}
