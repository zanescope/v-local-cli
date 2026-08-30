package shadowsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	shadowinventory "github.com/zanescope/v-local-cli/internal/shadowinventory"
)

type CodeIdentity struct {
	Identifier  string
	Team        string
	Requirement string
}

type Inspector struct {
	VerifyStrict func(context.Context, string) error
	CodeIdentity func(context.Context, string) (CodeIdentity, error)
	PlistString  func(context.Context, string, string) (string, error)
	Inventory    func(context.Context, string) ([]shadowinventory.Entry, string, error)
}

func pathDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func normalizeReferences(values []RewriteReference) ([]RewriteReference, error) {
	result := append([]RewriteReference(nil), values...)
	sort.Slice(result, func(left, right int) bool {
		return result[left].Path+"\x00"+result[left].Key < result[right].Path+"\x00"+result[right].Key
	})
	for index, value := range result {
		if !safeRelative(value.Path) || strings.TrimSpace(value.Key) == "" {
			return nil, errors.New("source rewrite reference is invalid")
		}
		if index > 0 && result[index-1] == value {
			return nil, errors.New("source rewrite reference is duplicated")
		}
	}
	return result, nil
}

func exactReadTarget(root, relative string) (string, error) {
	if !safeRelative(relative) {
		return "", errors.New("source read target is invalid")
	}
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	if !shadowinventory.WithinRoot(root, target) {
		return "", errors.New("source read target escaped its root")
	}
	current := root
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("source read target contains a linked or missing path")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", errors.New("source read target parent is not a directory")
		}
	}
	return target, nil
}

func (value Inspector) normalized() Inspector {
	if value.VerifyStrict == nil {
		value.VerifyStrict = systemVerifyStrict
	}
	if value.CodeIdentity == nil {
		value.CodeIdentity = systemCodeIdentity
	}
	if value.PlistString == nil {
		value.PlistString = systemPlistString
	}
	if value.Inventory == nil {
		value.Inventory = shadowinventory.ScanContext
	}
	return value
}

func (value Inspector) Inspect(ctx context.Context, sourcePath string, references []RewriteReference) (Snapshot, error) {
	value = value.normalized()
	if ctx == nil || filepath.Base(sourcePath) != "WeChat.app" {
		return Snapshot{}, errors.New("source qualification input is invalid")
	}
	root, err := shadowinventory.TrustedRoot(sourcePath)
	if err != nil || root != filepath.Clean(sourcePath) {
		return Snapshot{}, errors.New("source qualification path is not canonical")
	}
	references, err = normalizeReferences(references)
	if err != nil {
		return Snapshot{}, err
	}
	before, err := sourceIdentity(root)
	if err != nil {
		return Snapshot{}, err
	}
	if err := value.VerifyStrict(ctx, root); err != nil {
		return Snapshot{}, errors.New("source strict trust verification failed")
	}
	code, err := value.CodeIdentity(ctx, root)
	if err != nil || strings.TrimSpace(code.Identifier) == "" || strings.TrimSpace(code.Team) == "" || strings.TrimSpace(code.Requirement) == "" {
		return Snapshot{}, errors.New("source code identity is unavailable")
	}
	infoPath, err := exactReadTarget(root, "Contents/Info.plist")
	if err != nil {
		return Snapshot{}, err
	}
	version, err := value.PlistString(ctx, infoPath, "CFBundleShortVersionString")
	if err != nil || strings.TrimSpace(version) == "" {
		return Snapshot{}, errors.New("source version is unavailable")
	}
	build, err := value.PlistString(ctx, infoPath, "CFBundleVersion")
	if err != nil || strings.TrimSpace(build) == "" {
		return Snapshot{}, errors.New("source build is unavailable")
	}
	rewriteInputs := make([]RewriteInput, 0, len(references))
	for _, reference := range references {
		target, err := exactReadTarget(root, reference.Path)
		if err != nil {
			return Snapshot{}, err
		}
		expected, err := value.PlistString(ctx, target, reference.Key)
		if err != nil || strings.TrimSpace(expected) == "" {
			return Snapshot{}, errors.New("source rewrite input is unavailable")
		}
		rewriteInputs = append(rewriteInputs, RewriteInput{Path: reference.Path, Key: reference.Key, Expected: expected})
	}
	entries, inventoryDigest, err := value.Inventory(ctx, root)
	if err != nil {
		return Snapshot{}, err
	}
	if err := value.VerifyStrict(ctx, root); err != nil {
		return Snapshot{}, errors.New("source strict trust drifted during inventory")
	}
	after, err := sourceIdentity(root)
	if err != nil || before != after {
		return Snapshot{}, errors.New("source identity drifted during qualification")
	}
	requirement := sha256.Sum256([]byte(strings.TrimSpace(code.Requirement)))
	result := Snapshot{
		SourceLeaf: "WeChat.app", SourcePathDigest: pathDigest(root), SourceVersion: strings.TrimSpace(version),
		SourceBuild: strings.TrimSpace(build), RootIdentifier: strings.TrimSpace(code.Identifier),
		TeamIdentifier: strings.TrimSpace(code.Team), RequirementDigest: hex.EncodeToString(requirement[:]),
		InventoryDigest: inventoryDigest, InventoryEntries: len(entries), Identity: before,
		RewriteInputs: rewriteInputs,
	}
	if err := result.Validate(); err != nil {
		return Snapshot{}, err
	}
	return result, nil
}

func (value Inspector) Qualify(ctx context.Context, sourcePath string, manifest Manifest) (Qualification, error) {
	if err := manifest.Validate(); err != nil {
		return Qualification{}, err
	}
	references := make([]RewriteReference, len(manifest.RewriteInputs))
	for index, input := range manifest.RewriteInputs {
		references[index] = RewriteReference{Path: input.Path, Key: input.Key}
	}
	snapshot, err := value.Inspect(ctx, sourcePath, references)
	if err != nil {
		return Qualification{}, err
	}
	if snapshot.SourceLeaf != manifest.SourceLeaf || snapshot.SourcePathDigest != manifest.SourcePathDigest ||
		snapshot.SourceVersion != manifest.SourceVersion || snapshot.SourceBuild != manifest.SourceBuild ||
		snapshot.RootIdentifier != manifest.RootIdentifier || snapshot.TeamIdentifier != manifest.TeamIdentifier ||
		snapshot.RequirementDigest != manifest.RequirementDigest || snapshot.InventoryDigest != manifest.InventoryDigest ||
		snapshot.InventoryEntries != manifest.InventoryEntries || snapshot.Identity.UID != manifest.ExpectedUID ||
		snapshot.Identity.Mode != manifest.ExpectedMode || snapshot.Identity.LinkCount != manifest.ExpectedLinkCount ||
		len(snapshot.RewriteInputs) != len(manifest.RewriteInputs) {
		return Qualification{}, errors.New("source qualification drifted from the frozen manifest")
	}
	for index := range snapshot.RewriteInputs {
		if snapshot.RewriteInputs[index] != manifest.RewriteInputs[index] {
			return Qualification{}, errors.New("source rewrite input drifted from the frozen manifest")
		}
	}
	_, manifestDigest, err := CanonicalManifest(manifest)
	if err != nil {
		return Qualification{}, err
	}
	digest, err := qualificationDigest(manifestDigest, snapshot)
	if err != nil {
		return Qualification{}, err
	}
	return Qualification{ManifestDigest: manifestDigest, QualificationDigest: digest, Snapshot: snapshot}, nil
}
