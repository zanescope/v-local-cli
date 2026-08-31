// Package shadowbuildset owns the canonical, secret-free identity of one
// immutable Shadow candidate directory.
package shadowbuildset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	Version                = "v-local-shadow-build-set/v1"
	ProtocolVersion        = "v-local-shadow-ephemeral/v1"
	ManifestLeaf           = "shadow-build-set-v1.json"
	CleanupRouteDirect     = "direct"
	RouteSyntheticOnly     = "synthetic_only"
	RouteProductionCapable = "production_capable"
	entitlementRolePrefix  = "entitlement_profile:"
	entitlementLeafPrefix  = "shadow-entitlements-"
	entitlementLeafSuffix  = ".plist"
	maxManifestBytes       = int64(1024 * 1024)
	maxArtifactBytes       = int64(512 * 1024 * 1024)
	maxArtifacts           = 1024
)

type artifactSpec struct {
	Role string
	Leaf string
	Mode uint32
}

var artifactSpecs = []artifactSpec{
	{Role: "cli", Leaf: "v-local-cli", Mode: 0o555},
	{Role: "contract_vectors", Leaf: "shadow-contract-v1.json", Mode: 0o444},
	{Role: "provider", Leaf: "v-local-key-provider", Mode: 0o555},
	{Role: "source_manifest", Leaf: "shadow-source-manifest-v1.json", Mode: 0o444},
	{Role: "supervisor", Leaf: "v-local-shadow-supervisor", Mode: 0o555},
	{Role: "transformation_manifest", Leaf: "shadow-transformation-manifest-v1.json", Mode: 0o444},
}

type Artifact struct {
	Role   string `json:"role"`
	Leaf   string `json:"leaf"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

type Manifest struct {
	Version                      string     `json:"version"`
	ProtocolVersion              string     `json:"protocol_version"`
	CleanupRoute                 string     `json:"cleanup_route"`
	RouteMode                    string     `json:"route_mode"`
	ContractVectorsDigest        string     `json:"contract_vectors_digest"`
	SourceManifestDigest         string     `json:"source_manifest_digest"`
	TransformationManifestDigest string     `json:"transformation_manifest_digest"`
	Artifacts                    []Artifact `json:"artifacts"`
}

func lowerHex(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func specForRole(role string) (artifactSpec, bool) {
	for _, spec := range artifactSpecs {
		if spec.Role == role {
			return spec, true
		}
	}
	return artifactSpec{}, false
}

func specForLeaf(leaf string) (artifactSpec, bool) {
	for _, spec := range artifactSpecs {
		if spec.Leaf == leaf {
			return spec, true
		}
	}
	return artifactSpec{}, false
}

func entitlementDigestFromRole(role string) (string, bool) {
	if !strings.HasPrefix(role, entitlementRolePrefix) {
		return "", false
	}
	digest := strings.TrimPrefix(role, entitlementRolePrefix)
	return digest, lowerHex(digest)
}

func entitlementDigestFromLeaf(leaf string) (string, bool) {
	if !strings.HasPrefix(leaf, entitlementLeafPrefix) || !strings.HasSuffix(leaf, entitlementLeafSuffix) {
		return "", false
	}
	digest := strings.TrimSuffix(strings.TrimPrefix(leaf, entitlementLeafPrefix), entitlementLeafSuffix)
	return digest, lowerHex(digest)
}

func (value Artifact) Validate() error {
	if value.Size <= 0 || value.Size > maxArtifactBytes || !lowerHex(value.SHA256) {
		return errors.New("Shadow build-set artifact is invalid")
	}
	if spec, found := specForRole(value.Role); found {
		if value.Leaf == spec.Leaf && value.Mode == spec.Mode {
			return nil
		}
		return errors.New("Shadow build-set fixed artifact identity is invalid")
	}
	digest, found := entitlementDigestFromRole(value.Role)
	if !found || value.SHA256 != digest || value.Mode != 0o444 ||
		value.Leaf != entitlementLeafPrefix+digest+entitlementLeafSuffix {
		return errors.New("Shadow build-set entitlement artifact is invalid")
	}
	return nil
}

func (value Manifest) artifactDigest(role string) string {
	for _, artifact := range value.Artifacts {
		if artifact.Role == role {
			return artifact.SHA256
		}
	}
	return ""
}

func (value Manifest) Validate() error {
	if value.Version != Version || value.ProtocolVersion != ProtocolVersion ||
		value.CleanupRoute != CleanupRouteDirect ||
		(value.RouteMode != RouteSyntheticOnly && value.RouteMode != RouteProductionCapable) ||
		!lowerHex(value.ContractVectorsDigest) || !lowerHex(value.SourceManifestDigest) ||
		!lowerHex(value.TransformationManifestDigest) || len(value.Artifacts) < len(artifactSpecs)+1 ||
		len(value.Artifacts) > maxArtifacts {
		return errors.New("Shadow build-set manifest header is invalid")
	}
	seenLeaves := map[string]bool{}
	seenRoles := map[string]bool{}
	entitlementCount := 0
	for index, artifact := range value.Artifacts {
		if err := artifact.Validate(); err != nil || seenLeaves[artifact.Leaf] || seenRoles[artifact.Role] ||
			index > 0 && value.Artifacts[index-1].Role >= artifact.Role {
			return errors.New("Shadow build-set artifacts are not the exact canonical set")
		}
		seenLeaves[artifact.Leaf] = true
		seenRoles[artifact.Role] = true
		if _, found := entitlementDigestFromRole(artifact.Role); found {
			entitlementCount++
		}
	}
	for _, spec := range artifactSpecs {
		if !seenRoles[spec.Role] {
			return errors.New("Shadow build-set lacks a required artifact")
		}
	}
	if entitlementCount == 0 {
		return errors.New("Shadow build-set lacks a frozen entitlement profile")
	}
	if value.ContractVectorsDigest != value.artifactDigest("contract_vectors") ||
		value.SourceManifestDigest != value.artifactDigest("source_manifest") ||
		value.TransformationManifestDigest != value.artifactDigest("transformation_manifest") {
		return errors.New("Shadow build-set manifest digest binding is inconsistent")
	}
	return nil
}

func DecodeStrict(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("Shadow build-set JSON contains trailing data")
	}
	return nil
}

func Canonical(value Manifest) ([]byte, string, error) {
	if err := value.Validate(); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	payload = append(payload, '\n')
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

// InspectArtifacts hashes the exact required files after their final modes are
// already in place. It does not freeze or rename the directory.
func InspectArtifacts(root string) ([]Artifact, error) {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) < len(artifactSpecs)+1 || len(entries) > maxArtifacts {
		return nil, errors.New("Shadow build-set staging directory is incomplete or oversized")
	}
	result := make([]Artifact, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == ManifestLeaf {
			return nil, errors.New("Shadow build-set staging directory already contains a manifest")
		}
		role := ""
		mode := uint32(0)
		if spec, found := specForLeaf(entry.Name()); found {
			role, mode = spec.Role, spec.Mode
		} else if digest, found := entitlementDigestFromLeaf(entry.Name()); found {
			role, mode = entitlementRolePrefix+digest, 0o444
		} else {
			return nil, errors.New("Shadow build-set staging directory has an unexpected artifact")
		}
		payload, err := readBoundFile(root, entry.Name(), mode, 0)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(payload)
		digest := hex.EncodeToString(sum[:])
		if expected, found := entitlementDigestFromRole(role); found && digest != expected {
			return nil, errors.New("Shadow build-set entitlement filename does not match its content")
		}
		result = append(result, Artifact{
			Role: role, Leaf: entry.Name(), SHA256: digest, Size: int64(len(payload)), Mode: mode,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Role < result[right].Role })
	if _, err := Assemble(RouteSyntheticOnly, result); err != nil {
		return nil, err
	}
	return result, nil
}

// Assemble canonicalizes artifact order and binds the three data manifests to
// their artifact hashes. Route capability is descriptive; promotion remains a
// separate runtime prerequisite.
func Assemble(routeMode string, artifacts []Artifact) (Manifest, error) {
	artifacts = append([]Artifact(nil), artifacts...)
	sort.Slice(artifacts, func(left, right int) bool { return artifacts[left].Role < artifacts[right].Role })
	result := Manifest{
		Version: Version, ProtocolVersion: ProtocolVersion, CleanupRoute: CleanupRouteDirect,
		RouteMode: routeMode, Artifacts: artifacts,
	}
	result.ContractVectorsDigest = result.artifactDigest("contract_vectors")
	result.SourceManifestDigest = result.artifactDigest("source_manifest")
	result.TransformationManifestDigest = result.artifactDigest("transformation_manifest")
	if err := result.Validate(); err != nil {
		return Manifest{}, err
	}
	return result, nil
}

func readBoundFile(root, leaf string, mode uint32, expectedSize int64) ([]byte, error) {
	path := filepath.Join(root, leaf)
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		uint32(before.Mode().Perm()) != mode || before.Size() <= 0 || before.Size() > maxArtifactBytes ||
		expectedSize > 0 && before.Size() != expectedSize {
		return nil, errors.New("Shadow build-set file metadata is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("Shadow build-set file cannot be opened")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !singleLinkArtifact(before) || !singleLinkArtifact(opened) {
		return nil, errors.New("Shadow build-set file identity drifted before read")
	}
	limit := maxArtifactBytes
	if leaf == ManifestLeaf {
		limit = maxManifestBytes
	}
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(payload)) != before.Size() || int64(len(payload)) > limit {
		return nil, errors.New("Shadow build-set file content is invalid")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || !singleLinkArtifact(after) ||
		after.Size() != opened.Size() || after.Mode() != opened.Mode() {
		return nil, errors.New("Shadow build-set file identity drifted during read")
	}
	pathAfter, err := os.Lstat(path)
	resolvedAfter, resolveErr := filepath.EvalSymlinks(path)
	if err != nil || resolveErr != nil || resolvedAfter != path || pathAfter.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(after, pathAfter) || !singleLinkArtifact(pathAfter) ||
		pathAfter.Size() != after.Size() || pathAfter.Mode() != after.Mode() {
		return nil, errors.New("Shadow build-set path identity drifted during read")
	}
	return payload, nil
}

// Load verifies the exact immutable directory and returns the manifest digest.
// The digest is the build-set identity carried by qualifications and attempts.
func Load(root string) (Manifest, string, error) {
	root = filepath.Clean(root)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return Manifest{}, "", errors.New("Shadow build-set root is not canonical")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o555 {
		return Manifest{}, "", errors.New("Shadow build-set root is not immutable")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) < len(artifactSpecs)+2 || len(entries) > maxArtifacts+1 {
		return Manifest{}, "", errors.New("Shadow build-set directory is incomplete or has extra entries")
	}
	manifestPayload, err := readBoundFile(root, ManifestLeaf, 0o444, 0)
	if err != nil {
		return Manifest{}, "", err
	}
	var manifest Manifest
	if err := DecodeStrict(manifestPayload, &manifest); err != nil || manifest.Validate() != nil {
		return Manifest{}, "", errors.New("Shadow build-set manifest is invalid")
	}
	if len(entries) != len(manifest.Artifacts)+1 {
		return Manifest{}, "", errors.New("Shadow build-set directory does not match its manifest")
	}
	expected := map[string]bool{ManifestLeaf: true}
	for _, artifact := range manifest.Artifacts {
		expected[artifact.Leaf] = true
	}
	for _, entry := range entries {
		if !expected[entry.Name()] {
			return Manifest{}, "", errors.New("Shadow build-set directory has an unexpected entry")
		}
	}
	canonical, digest, err := Canonical(manifest)
	if err != nil || !bytes.Equal(canonical, manifestPayload) {
		return Manifest{}, "", errors.New("Shadow build-set manifest is not canonical")
	}
	for _, artifact := range manifest.Artifacts {
		payload, err := readBoundFile(root, artifact.Leaf, artifact.Mode, artifact.Size)
		if err != nil {
			return Manifest{}, "", err
		}
		sum := sha256.Sum256(payload)
		if hex.EncodeToString(sum[:]) != artifact.SHA256 {
			return Manifest{}, "", errors.New("Shadow build-set artifact digest drifted")
		}
	}
	return manifest, digest, nil
}

// LoadArtifact revalidates the entire immutable build set before returning one
// exact role. Callers cannot carry a once-validated path across a later chmod,
// rename, manifest replacement, or artifact replacement window.
func LoadArtifact(root, expectedDigest, role string) ([]byte, error) {
	manifest, digest, err := Load(root)
	if err != nil || digest != expectedDigest || role == "" {
		return nil, errors.New("Shadow build-set binding drifted before artifact read")
	}
	var selected *Artifact
	for index := range manifest.Artifacts {
		if manifest.Artifacts[index].Role == role {
			selected = &manifest.Artifacts[index]
			break
		}
	}
	if selected == nil {
		return nil, errors.New("Shadow build set lacks the requested artifact role")
	}
	payload, err := readBoundFile(root, selected.Leaf, selected.Mode, selected.Size)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != selected.SHA256 {
		return nil, errors.New("Shadow build-set artifact digest drifted during bound read")
	}
	return payload, nil
}
