// Package shadowqualification composes the CLI-owned, read-only half of a
// production Shadow qualification. It does not issue approval or create any
// attempt resource.
package shadowqualification

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	accountmodel "github.com/zanescope/v-local-cli/internal/shadowaccount"
	buildsetmodel "github.com/zanescope/v-local-cli/internal/shadowbuildset"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	ownermodel "github.com/zanescope/v-local-cli/internal/shadowowner"
	sourcemodel "github.com/zanescope/v-local-cli/internal/shadowsource"
)

type Result struct {
	Binding       ownermodel.Binding
	BuildRoot     string
	ProviderPath  string
	Account       accountmodel.Record
	SourceVersion string
	SourceBuild   string
}

type Discovery struct {
	BuildRoot    string
	BuildDigest  string
	ProviderPath string
	Account      accountmodel.Record
	Source       sourcemodel.Manifest
	SourceDigest string
}

type Runtime struct {
	Executable        func() (string, error)
	ResolveAccount    func() (accountmodel.Record, error)
	RevalidateAccount func(accountmodel.Record) error
	Inspector         sourcemodel.Inspector
}

func (value Runtime) normalized() Runtime {
	if value.Executable == nil {
		value.Executable = os.Executable
	}
	if value.ResolveAccount == nil {
		value.ResolveAccount = accountmodel.ResolveCurrent
	}
	if value.RevalidateAccount == nil {
		value.RevalidateAccount = accountmodel.Revalidate
	}
	return value
}

func lowerDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (value Runtime) Discover() (Discovery, error) {
	value = value.normalized()
	executable, err := value.Executable()
	if err != nil {
		return Discovery{}, errors.New("CLI executable identity is unavailable")
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil || filepath.Base(executable) != "v-local-cli" || filepath.Clean(executable) != executable {
		return Discovery{}, errors.New("CLI is not running from a canonical frozen build set")
	}
	root := filepath.Dir(executable)
	manifest, buildDigest, err := buildsetmodel.Load(root)
	if err != nil || manifest.RouteMode != buildsetmodel.RouteProductionCapable ||
		manifest.ProtocolVersion != contract.Version || manifest.CleanupRoute != contract.CleanupRouteDirect {
		return Discovery{}, errors.New("CLI frozen production build set is unavailable")
	}
	sourcePayload, err := buildsetmodel.LoadArtifact(root, buildDigest, "source_manifest")
	if err != nil {
		return Discovery{}, err
	}
	sourceManifest, err := sourcemodel.DecodeManifest(sourcePayload)
	if err != nil {
		return Discovery{}, err
	}
	_, sourceDigest, err := sourcemodel.CanonicalManifest(sourceManifest)
	if err != nil || sourceDigest != manifest.SourceManifestDigest {
		return Discovery{}, errors.New("CLI source manifest is not canonical or build-bound")
	}
	account, err := value.ResolveAccount()
	if err != nil || account.Validate() != nil || value.RevalidateAccount(account) != nil {
		return Discovery{}, errors.New("CLI account binding is unavailable or drifted")
	}
	providerPath := filepath.Join(root, "v-local-key-provider")
	providerInfo, err := os.Lstat(providerPath)
	if err != nil || !providerInfo.Mode().IsRegular() || providerInfo.Mode()&os.ModeSymlink != 0 {
		return Discovery{}, errors.New("frozen Provider executable is unavailable")
	}
	return Discovery{
		BuildRoot: root, BuildDigest: buildDigest, ProviderPath: providerPath, Account: account,
		Source: sourceManifest, SourceDigest: sourceDigest,
	}, nil
}

func (value Runtime) Qualify(ctx context.Context, sourcePath, optionsDigest string) (Result, error) {
	value = value.normalized()
	if ctx == nil || filepath.Clean(sourcePath) != sourcePath || filepath.Base(sourcePath) != "WeChat.app" ||
		!lowerDigest(optionsDigest) {
		return Result{}, errors.New("CLI Shadow qualification input is invalid")
	}
	discovery, err := value.Discover()
	if err != nil {
		return Result{}, err
	}
	qualification, err := value.Inspector.Qualify(ctx, sourcePath, discovery.Source)
	if err != nil || qualification.ManifestDigest != discovery.SourceDigest ||
		!lowerDigest(qualification.QualificationDigest) {
		return Result{}, errors.New("CLI source qualification drifted from the frozen build set")
	}
	return Result{
		Binding: ownermodel.Binding{
			BuildSetDigest: discovery.BuildDigest, SourceQualificationDigest: qualification.QualificationDigest,
			CleanupRoute: contract.CleanupRouteDirect, AccountBindingID: discovery.Account.BindingID, OptionsDigest: optionsDigest,
		},
		BuildRoot: discovery.BuildRoot, ProviderPath: discovery.ProviderPath, Account: discovery.Account,
		SourceVersion: qualification.Snapshot.SourceVersion, SourceBuild: qualification.Snapshot.SourceBuild,
	}, nil
}
