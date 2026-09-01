package adminservice

import (
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"strings"
)

var darwinIntegrationProtectedPaths = []string{
	"/Library/Application Support/ZFNF Mobile Egress",
	"/var/run/com.cbjjensen.mobile-egress.relay.sock",
	"/private/var/run/com.cbjjensen.mobile-egress.relay.sock",
	"/var/run/com.cbjjensen.mobile-egress.relay.lock",
	"/private/var/run/com.cbjjensen.mobile-egress.relay.lock",
}

func darwinIntegrationPathRefused(spelled, canonical string) bool {
	spelled = pathpkg.Clean(spelled)
	canonical = pathpkg.Clean(canonical)
	if !pathpkg.IsAbs(spelled) || !pathpkg.IsAbs(canonical) {
		return true
	}
	for _, protected := range darwinIntegrationProtectedPaths {
		if darwinPathWithinFold(spelled, protected) || darwinPathWithinFold(canonical, protected) {
			return true
		}
	}
	return false
}

func darwinPathWithinFold(candidate, parent string) bool {
	candidate = pathpkg.Clean(candidate)
	parent = pathpkg.Clean(parent)
	return strings.EqualFold(candidate, parent) ||
		strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(parent)+"/")
}

type darwinTestRootFactory struct {
	canonicalize func(string) (string, error)
	create       func() (string, error)
}

func (factory darwinTestRootFactory) Create(existingBase string) (darwinTestRoot, error) {
	if factory.canonicalize == nil || factory.create == nil || existingBase == "" {
		return darwinTestRoot{}, errors.New("invalid Darwin integration root factory")
	}
	baseSpelling := pathpkg.Clean(existingBase)
	canonicalBase, err := factory.canonicalize(baseSpelling)
	if err != nil {
		return darwinTestRoot{}, fmt.Errorf("canonicalize Darwin integration temp base before creation: %w", err)
	}
	canonicalBase = pathpkg.Clean(canonicalBase)
	if darwinIntegrationPathRefused(baseSpelling, canonicalBase) {
		return darwinTestRoot{}, errors.New("Darwin integration temp base is protected")
	}

	created, err := factory.create()
	if err != nil {
		return darwinTestRoot{}, fmt.Errorf("create Darwin integration fixture root: %w", err)
	}
	created = pathpkg.Clean(created)
	canonicalRoot, err := factory.canonicalize(created)
	if err != nil {
		return darwinTestRoot{}, fmt.Errorf("canonicalize created Darwin integration fixture root: %w", err)
	}
	canonicalRoot = pathpkg.Clean(canonicalRoot)
	canonicalBaseAfter, err := factory.canonicalize(baseSpelling)
	if err != nil {
		return darwinTestRoot{}, fmt.Errorf("revalidate Darwin integration temp base: %w", err)
	}
	canonicalBaseAfter = pathpkg.Clean(canonicalBaseAfter)
	if !strings.EqualFold(canonicalBaseAfter, canonicalBase) ||
		darwinIntegrationPathRefused(created, canonicalRoot) ||
		!darwinPathWithinFold(created, baseSpelling) || !darwinPathWithinFold(canonicalRoot, canonicalBase) ||
		strings.EqualFold(created, baseSpelling) || strings.EqualFold(canonicalRoot, canonicalBase) {
		return darwinTestRoot{}, errors.New("created Darwin integration fixture root escaped admitted temp base")
	}
	return darwinTestRoot{spelled: created, canonical: canonicalRoot}, nil
}

type darwinTestRoot struct {
	spelled   string
	canonical string
	identity  os.FileInfo
	acl       pathACLInspector
}

func (root darwinTestRoot) Contains(spelled, canonical string) bool {
	spelled = pathpkg.Clean(spelled)
	canonical = pathpkg.Clean(canonical)
	return root.spelled != "" && root.canonical != "" &&
		!darwinIntegrationPathRefused(spelled, canonical) &&
		darwinPathWithinFold(spelled, root.spelled) && darwinPathWithinFold(canonical, root.canonical)
}

func runDarwinAdmittedMutation(root darwinTestRoot, spelled, canonical string, mutate func() error) error {
	if mutate == nil {
		return errors.New("Darwin integration mutation callback is required")
	}
	if !root.Contains(spelled, canonical) {
		return errors.New("Darwin integration mutation escaped admitted root")
	}
	return mutate()
}
