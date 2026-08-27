package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireCLIReleaseFragments(t *testing.T, relative string, fragments ...string) string {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), filepath.FromSlash(relative))
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Errorf("%s is missing release regression gate %q", relative, fragment)
		}
	}
	return text
}

func TestPhase5CLIReleaseKeepsAllArchitectureSigningAndPublishingGates(t *testing.T) {
	release := requireCLIReleaseFragments(t, ".github/workflows/release.yml",
		"persist-credentials: false",
		"arch: [amd64, arm64]",
		"notarytool submit",
		"submission_status",
		"notarytool log",
		"xcrun stapler staple",
		"xcrun stapler validate",
		"codesign --verify --strict",
		"spctl --assess --type execute",
		"spctl --assess --type open",
		"signature-manifest-windows-",
		"signature-manifest-darwin-",
		"release-checksums.txt",
		"actions/attest@",
		"npm stage publish",
	)
	for _, asset := range []string{
		"v-local-cli-windows-amd64.exe", "v-local-cli-windows-arm64.exe",
		"v-local-cli-darwin-amd64", "v-local-cli-darwin-arm64",
		"v-local-cli-linux-amd64", "v-local-cli-linux-arm64",
	} {
		if !strings.Contains(release, asset) {
			t.Errorf("signed CLI release does not bind required asset %q", asset)
		}
	}
	requireCLIReleaseFragments(t, "references/macos-acceptance.md",
		"Apple Silicon", "Intel", "Rosetta", "Keychain", "notarization",
	)
	requireCLIReleaseFragments(t, "scripts/build-windows-release.ps1",
		"Get-AuthenticodeSignature", "signtool", "verify", "TimeStamperCertificate",
		"build_mode = 'release'", "runtime_authenticode_required", "fixed_install_required",
		"provider.releaseSignerSHA256", "signer_thumbprint", "signer_certificate_sha256",
		"timestamp_signer_thumbprint", "signature-manifest.json",
	)
	requireCLIReleaseFragments(t, "scripts/build-macos-release.sh",
		"internal/provider.buildMode=release", "internal/provider.releaseTeamID=",
		"V_LOCAL_CLI_RELEASE_TEAM_ID", "--identifier com.zanescope.v-local-cli",
		"--options runtime", "--timestamp", "codesign --verify --strict", "TeamIdentifier=",
	)
	requireCLIReleaseFragments(t, "internal/provider/runtime_trust_windows.go",
		"WTD_REVOKE_NONE", "WTD_REVOCATION_CHECK_NONE", "WTD_CACHE_ONLY_URL_RETRIEVAL",
		"WTHelperProvDataFromStateData", "WTHelperGetProvSignerFromChain", "WTHelperGetProvCertFromChain",
		"releaseSignerSHA256", "Authenticode signer does not match the release identity",
	)
	requireCLIReleaseFragments(t, "internal/app/crash_hardening_windows.go",
		"WerGetFlags", "WerSetFlags", "werFaultReportingFlagNoHeap",
	)
	requireCLIReleaseFragments(t, "internal/provider/sensitive_memory_windows.go",
		"WerRegisterExcludedMemoryBlock", "WerUnregisterExcludedMemoryBlock",
	)
	requireCLIReleaseFragments(t, "internal/provider/runtime_trust_darwin.go",
		"com.zanescope.v-local-cli", "com.zanescope.v-local-key-provider",
		"com.zanescope.v-local-key-provider.helper", "anchor apple generic",
		"releaseTeamID", "expectedDarwinTeamID",
		"CLI is not signed by the release Developer ID team",
		"Provider is not signed by the release Developer ID team",
		"daemon PID is not running the advertised helper image",
	)
	requireCLIReleaseFragments(t, "internal/provider/status.go",
		"override_rejected", "fixedProviderInstallPath", "canonicalExecutable",
	)
	requireCLIReleaseFragments(t, "references/windows-key-provider-acceptance.md",
		"Windows x64", "Windows ARM64", "Credential Manager", "Config.Cipher", "missing-only",
	)
	candidate := requireCLIReleaseFragments(t, ".github/workflows/release-candidate.yml",
		"internal/provider.buildMode=candidate",
	)
	if strings.Contains(candidate, "internal/provider.buildMode=release") {
		t.Fatal("unsigned release-candidate assets must not enable release trust claims")
	}
	if strings.Contains(release, "for binary in dist/v-local-cli-*") {
		t.Fatal("signed release build-info must not parse DMG containers as Go binaries")
	}
}
