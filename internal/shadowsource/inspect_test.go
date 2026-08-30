package shadowsource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	shadowinventory "github.com/zanescope/v-local-cli/internal/shadowinventory"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "WeChat.app")
	if err := os.MkdirAll(filepath.Join(root, "Contents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Contents", "Info.plist"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func fixtureInspector(strictCalls *int, inventoryDigest *string) Inspector {
	return Inspector{
		VerifyStrict: func(context.Context, string) error {
			*strictCalls++
			return nil
		},
		CodeIdentity: func(context.Context, string) (CodeIdentity, error) {
			return CodeIdentity{Identifier: "com.tencent.xinWeChat", Team: "TEAM", Requirement: "anchor trusted fixture"}, nil
		},
		PlistString: func(_ context.Context, _ string, key string) (string, error) {
			switch key {
			case "CFBundleShortVersionString":
				return "4.1.11", nil
			case "CFBundleVersion":
				return "269136", nil
			case "CFBundleIdentifier":
				return "com.tencent.xinWeChat", nil
			default:
				return "", os.ErrNotExist
			}
		},
		Inventory: func(context.Context, string) ([]shadowinventory.Entry, string, error) {
			return []shadowinventory.Entry{{Path: "Contents/Info.plist", Type: "file", Mode: 0o600, Size: 7, Digest: strings.Repeat("2", 64)}}, *inventoryDigest, nil
		},
	}
}

func TestIndependentInspectFreezeAndQualifyBindIdentityWithoutPath(t *testing.T) {
	root := fixtureRoot(t)
	strictCalls := 0
	inventoryDigest := strings.Repeat("1", 64)
	inspector := fixtureInspector(&strictCalls, &inventoryDigest)
	snapshot, err := inspector.Inspect(context.Background(), root, []RewriteReference{{
		Path: "Contents/Info.plist", Key: "CFBundleIdentifier",
	}})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Freeze(snapshot, strings.Repeat("3", 64))
	if err != nil {
		t.Fatal(err)
	}
	payload, manifestDigest, err := CanonicalManifest(manifest)
	if err != nil || strings.Contains(string(payload), root) {
		t.Fatalf("manifest persisted a source path or failed: digest=%q err=%v", manifestDigest, err)
	}
	decoded, err := DecodeManifest(payload)
	if err != nil || decoded.SourceBuild != manifest.SourceBuild {
		t.Fatalf("strict source manifest decode failed: %#v err=%v", decoded, err)
	}
	qualified, err := inspector.Qualify(context.Background(), root, manifest)
	if err != nil || qualified.ManifestDigest != manifestDigest || qualified.QualificationDigest == manifestDigest {
		t.Fatalf("qualification=%+v err=%v", qualified, err)
	}
	if strictCalls != 4 {
		t.Fatalf("strict trust was not checked before and after both observations: %d", strictCalls)
	}
}

func TestIndependentQualificationRejectsInventoryDriftAndUnknownManifestFields(t *testing.T) {
	root := fixtureRoot(t)
	strictCalls := 0
	inventoryDigest := strings.Repeat("1", 64)
	inspector := fixtureInspector(&strictCalls, &inventoryDigest)
	snapshot, err := inspector.Inspect(context.Background(), root, []RewriteReference{{
		Path: "Contents/Info.plist", Key: "CFBundleIdentifier",
	}})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Freeze(snapshot, strings.Repeat("3", 64))
	if err != nil {
		t.Fatal(err)
	}
	inventoryDigest = strings.Repeat("4", 64)
	if _, err := inspector.Qualify(context.Background(), root, manifest); err == nil {
		t.Fatal("independent source qualification accepted inventory drift")
	}
	if _, err := DecodeManifest([]byte(`{"version":1,"unknown":true}`)); err == nil {
		t.Fatal("independent source manifest decoder accepted unknown fields")
	}
}
