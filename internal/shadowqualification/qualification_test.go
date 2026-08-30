package shadowqualification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	accountmodel "github.com/zanescope/v-local-cli/internal/shadowaccount"
	buildsetmodel "github.com/zanescope/v-local-cli/internal/shadowbuildset"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	shadowinventory "github.com/zanescope/v-local-cli/internal/shadowinventory"
	sourcemodel "github.com/zanescope/v-local-cli/internal/shadowsource"
)

func writeArtifact(t *testing.T, root, leaf string, payload []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, leaf), payload, 0o600); err != nil || os.Chmod(filepath.Join(root, leaf), mode) != nil {
		t.Fatalf("could not write artifact %s: %v", leaf, err)
	}
}

func qualificationFixture(t *testing.T) (string, string, sourcemodel.Inspector, accountmodel.Record) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "WeChat.app")
	if err := os.MkdirAll(filepath.Join(source, "Contents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "Contents", "Info.plist"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspector := sourcemodel.Inspector{
		VerifyStrict: func(context.Context, string) error { return nil },
		CodeIdentity: func(context.Context, string) (sourcemodel.CodeIdentity, error) {
			return sourcemodel.CodeIdentity{Identifier: "com.example.source", Team: "TEAM", Requirement: "designated fixture"}, nil
		},
		PlistString: func(_ context.Context, _ string, key string) (string, error) {
			switch key {
			case "CFBundleShortVersionString":
				return "4.1.11", nil
			case "CFBundleVersion":
				return "269136", nil
			default:
				return "com.example.source", nil
			}
		},
		Inventory: shadowinventory.ScanContext,
	}
	snapshot, err := inspector.Inspect(context.Background(), source, []sourcemodel.RewriteReference{{
		Path: "Contents/Info.plist", Key: "CFBundleIdentifier",
	}})
	if err != nil {
		t.Fatal(err)
	}
	sourceManifest, err := sourcemodel.Freeze(snapshot, strings.Repeat("3", 64))
	if err != nil {
		t.Fatal(err)
	}
	sourcePayload, _, err := sourcemodel.CanonicalManifest(sourceManifest)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "build")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	writeArtifact(t, root, "v-local-cli", []byte("cli"), 0o555)
	writeArtifact(t, root, "shadow-contract-v1.json", []byte("contract"), 0o444)
	writeArtifact(t, root, "v-local-key-provider", []byte("provider"), 0o555)
	writeArtifact(t, root, "shadow-source-manifest-v1.json", sourcePayload, 0o444)
	writeArtifact(t, root, "v-local-shadow-supervisor", []byte("supervisor"), 0o555)
	writeArtifact(t, root, "shadow-transformation-manifest-v1.json", []byte("transform"), 0o444)
	entitlement := []byte("<plist><dict/></plist>\n")
	sum := sha256.Sum256(entitlement)
	writeArtifact(t, root, "shadow-entitlements-"+hex.EncodeToString(sum[:])+".plist", entitlement, 0o444)
	artifacts, err := buildsetmodel.InspectArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := buildsetmodel.Assemble(buildsetmodel.RouteProductionCapable, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	payload, _, err := buildsetmodel.Canonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeArtifact(t, root, buildsetmodel.ManifestLeaf, payload, 0o444)
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	account := accountmodel.Record{
		UID: 1, Home: base, HomeDevice: 1, HomeInode: 1, BindingID: "aabbccddeeff0011aabbccddeeff0011",
		SecurityRoot:   filepath.Join(base, "Library", "Application Support", "v-local", "shadow-runtime"),
		ContainersRoot: filepath.Join(base, "Library", "Containers"),
	}
	return root, source, inspector, account
}

func TestCLIQualificationIndependentlyBindsCurrentFrozenBuildSourceAndAccount(t *testing.T) {
	root, source, inspector, account := qualificationFixture(t)
	runtime := Runtime{
		Executable:        func() (string, error) { return filepath.Join(root, "v-local-cli"), nil },
		ResolveAccount:    func() (accountmodel.Record, error) { return account, nil },
		RevalidateAccount: func(accountmodel.Record) error { return nil }, Inspector: inspector,
	}
	options := strings.Repeat("4", 64)
	result, err := runtime.Qualify(context.Background(), source, options)
	if err != nil {
		t.Fatal(err)
	}
	if result.BuildRoot != root || result.ProviderPath != filepath.Join(root, "v-local-key-provider") ||
		result.Binding.BuildSetDigest == "" || result.Binding.SourceQualificationDigest == "" ||
		result.Binding.CleanupRoute != contract.CleanupRouteDirect || result.Binding.AccountBindingID != account.BindingID ||
		result.Binding.OptionsDigest != options || result.SourceVersion != "4.1.11" || result.SourceBuild != "269136" {
		t.Fatalf("unexpected independent qualification: %#v", result)
	}
}

func TestCLIQualificationRejectsNonFrozenExecutableAndSourceDrift(t *testing.T) {
	root, source, inspector, account := qualificationFixture(t)
	runtime := Runtime{
		Executable:        func() (string, error) { return filepath.Join(root, "renamed-cli"), nil },
		ResolveAccount:    func() (accountmodel.Record, error) { return account, nil },
		RevalidateAccount: func(accountmodel.Record) error { return nil }, Inspector: inspector,
	}
	if _, err := runtime.Qualify(context.Background(), source, strings.Repeat("4", 64)); err == nil {
		t.Fatal("qualification accepted a CLI outside the exact frozen role")
	}
	runtime.Executable = func() (string, error) { return filepath.Join(root, "v-local-cli"), nil }
	if err := os.WriteFile(filepath.Join(source, "drift"), []byte("drift"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Qualify(context.Background(), source, strings.Repeat("4", 64)); err == nil {
		t.Fatal("qualification accepted source inventory drift")
	}
}
