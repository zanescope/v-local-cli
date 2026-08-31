package shadowbuildset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const goldenBuildSetDigest = "6b63d4f976c936d986d9dc23d2992f7e83a70e3bbbb10c178bbfc20aacc8d698"

func goldenPayload(t *testing.T) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "testdata", "shadow-build-set-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func goldenManifest(t *testing.T) Manifest {
	t.Helper()
	var value Manifest
	if err := DecodeStrict(goldenPayload(t), &value); err != nil || value.Validate() != nil {
		t.Fatalf("golden build-set manifest is invalid: %v", err)
	}
	return value
}

func TestGoldenBuildSetManifestIsCanonicalAndContractBound(t *testing.T) {
	payload := goldenPayload(t)
	value := goldenManifest(t)
	canonical, digest, err := Canonical(value)
	if err != nil || !bytes.Equal(canonical, payload) || digest != goldenBuildSetDigest {
		t.Fatalf("golden build-set canonical binding failed: digest=%s err=%v", digest, err)
	}
	contract, err := os.ReadFile(filepath.Join("..", "..", "testdata", "shadow-contract-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contract)
	if actual := hex.EncodeToString(sum[:]); actual != value.ContractVectorsDigest {
		t.Fatalf("build-set does not bind the canonical contract vectors: %s", actual)
	}
}

func buildDirectory(t *testing.T) (string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "build-set")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	for index, spec := range artifactSpecs {
		payload := []byte(strings.Repeat(string(rune('a'+index)), index+1))
		path := filepath.Join(root, spec.Leaf)
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, os.FileMode(spec.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	entitlement := []byte("<plist><dict><key>synthetic</key><true/></dict></plist>\n")
	entitlementSum := sha256.Sum256(entitlement)
	entitlementDigest := hex.EncodeToString(entitlementSum[:])
	entitlementPath := filepath.Join(root, entitlementLeafPrefix+entitlementDigest+entitlementLeafSuffix)
	if err := os.WriteFile(entitlementPath, entitlement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(entitlementPath, 0o444); err != nil {
		t.Fatal(err)
	}
	artifacts, err := InspectArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Assemble(RouteProductionCapable, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	payload, digest, err := Canonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, ManifestLeaf)
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifestPath, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	return root, digest
}

func TestLoadVerifiesExactImmutableDirectoryAndDetectsDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("frozen build-set POSIX mode verification is a macOS gate")
	}
	root, expected := buildDirectory(t)
	_, digest, err := Load(root)
	if err != nil || digest != expected {
		t.Fatalf("exact build-set failed verification: digest=%s err=%v", digest, err)
	}
	payload, err := LoadArtifact(root, digest, "source_manifest")
	if err != nil || len(payload) == 0 {
		t.Fatalf("bound artifact read failed: bytes=%d err=%v", len(payload), err)
	}
	if _, err := LoadArtifact(root, strings.Repeat("f", 64), "source_manifest"); err == nil {
		t.Fatal("bound artifact read accepted a different build-set digest")
	}
	providerPath := filepath.Join(root, "v-local-key-provider")
	if err := os.Chmod(providerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(providerPath, []byte("drift"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(root); err == nil {
		t.Fatal("build-set verifier accepted artifact drift")
	}
}

func TestLoadRejectsHardLinkedArtifact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("frozen build-set hard-link identity is a macOS gate")
	}
	root, _ := buildDirectory(t)
	artifact := filepath.Join(root, "v-local-key-provider")
	if err := os.Link(artifact, root+".provider-hardlink"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(root); err == nil {
		t.Fatal("build-set verifier accepted a hard-linked artifact")
	}
}

func TestAssembleRejectsIncompleteOrAmbiguousArtifactSet(t *testing.T) {
	value := goldenManifest(t)
	if _, err := Assemble(value.RouteMode, value.Artifacts[:len(value.Artifacts)-1]); err == nil {
		t.Fatal("build-set assembly accepted a missing artifact")
	}
	duplicated := append([]Artifact(nil), value.Artifacts...)
	duplicated[len(duplicated)-1] = duplicated[0]
	if _, err := Assemble(value.RouteMode, duplicated); err == nil {
		t.Fatal("build-set assembly accepted duplicated artifact identity")
	}
}
