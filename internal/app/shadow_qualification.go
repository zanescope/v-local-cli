package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
	"github.com/zanescope/v-local-cli/internal/provider"
	clockmodel "github.com/zanescope/v-local-cli/internal/shadowclock"
	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	qualificationmodel "github.com/zanescope/v-local-cli/internal/shadowqualification"
)

const shadowQualificationVersion = "v-local-shadow-qualification/v1"

type shadowQualificationSummary struct {
	Version                string `json:"version"`
	Status                 string `json:"status"`
	BuildSetDigest         string `json:"build_set_digest"`
	SourceDigest           string `json:"source_qualification_digest"`
	SourceVersion          string `json:"source_version"`
	SourceBuild            string `json:"source_build"`
	CleanupRoute           string `json:"cleanup_route"`
	ProviderMatched        bool   `json:"provider_matched"`
	ProductionRouteEnabled bool   `json:"production_route_enabled"`
	ElapsedMS              int64  `json:"elapsed_ms"`
}

type shadowQualificationFailure struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	Stage   string `json:"stage"`
}

func qualificationRequestID() (string, error) {
	payload := make([]byte, 16)
	if _, err := rand.Read(payload); err != nil {
		return "", err
	}
	return hex.EncodeToString(payload), nil
}

func exactQualificationMatch(local qualificationmodel.Result, remote contract.Result) error {
	if remote.Validate() != nil || remote.Status != "qualified" || remote.Qualification == nil ||
		remote.Qualification.BuildSetDigest != local.Binding.BuildSetDigest ||
		remote.Qualification.SourceQualificationDigest != local.Binding.SourceQualificationDigest ||
		remote.Qualification.CleanupRoute != local.Binding.CleanupRoute ||
		remote.Qualification.AccountBindingID != local.Binding.AccountBindingID ||
		remote.Qualification.OptionsDigest != local.Binding.OptionsDigest ||
		remote.Qualification.SourceVersion != local.SourceVersion || remote.Qualification.SourceBuild != local.SourceBuild ||
		remote.Qualification.ProductionRouteEnabled {
		return errors.New("Provider qualification did not exactly match the independent CLI binding")
	}
	return nil
}

func writeQualificationFailure(writer io.Writer, stage string) int {
	_ = json.NewEncoder(writer).Encode(shadowQualificationFailure{
		Version: shadowQualificationVersion, Status: "failed", Stage: stage,
	})
	return 5
}

func runShadowQualificationCommand(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("__shadow-qualify", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	accountSelector := set.String("account", "", "exact local account selector")
	databaseOnly := set.Bool("database-only", false, "bind only the database scope")
	if err := set.Parse(args); err != nil || len(set.Args()) != 0 {
		_, _ = fmt.Fprintln(stderr, "v-local-cli: invalid Shadow qualification arguments")
		return 2
	}
	account, found, ambiguous := localplatform.Select(localplatform.Accounts(), *accountSelector)
	if !found || ambiguous {
		return writeQualificationFailure(stderr, "account_selection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	started := time.Now()
	runtime := qualificationmodel.Runtime{}
	discovery, err := runtime.Discover()
	if err != nil {
		return writeQualificationFailure(stderr, "build_discovery")
	}
	scopes := []string{"database", "media"}
	if *databaseOnly {
		scopes = []string{"database"}
	}
	client, err := provider.NewShadowClient(discovery.ProviderPath, account, scopes, "", clockmodel.System{})
	if err != nil || client.OptionsDigest() == "" {
		return writeQualificationFailure(stderr, "provider_client")
	}
	local, err := runtime.Qualify(ctx, "/Applications/WeChat.app", client.OptionsDigest())
	if err != nil || local.BuildRoot != discovery.BuildRoot || local.ProviderPath != discovery.ProviderPath ||
		local.Binding.BuildSetDigest != discovery.BuildDigest {
		return writeQualificationFailure(stderr, "cli_source_qualification")
	}
	requestID, err := qualificationRequestID()
	if err != nil {
		return writeQualificationFailure(stderr, "request_identity")
	}
	request := contract.Request{
		Version: contract.Version, Operation: "qualify", RequestID: requestID,
		BuildSetDigest: local.Binding.BuildSetDigest, SourceQualificationDigest: local.Binding.SourceQualificationDigest,
		CleanupRoute: local.Binding.CleanupRoute, AccountBindingID: local.Binding.AccountBindingID,
		OptionsDigest: local.Binding.OptionsDigest,
	}
	remote, err := client.Qualify(ctx, request)
	if err != nil {
		return writeQualificationFailure(stderr, "provider_source_qualification")
	}
	if err := exactQualificationMatch(local, remote); err != nil {
		return writeQualificationFailure(stderr, "cross_process_binding")
	}
	summary := shadowQualificationSummary{
		Version: shadowQualificationVersion, Status: "qualified", BuildSetDigest: local.Binding.BuildSetDigest,
		SourceDigest: local.Binding.SourceQualificationDigest, SourceVersion: local.SourceVersion,
		SourceBuild: local.SourceBuild, CleanupRoute: local.Binding.CleanupRoute, ProviderMatched: true,
		ProductionRouteEnabled: false, ElapsedMS: time.Since(started).Milliseconds(),
	}
	if err := json.NewEncoder(stdout).Encode(summary); err != nil {
		return writeQualificationFailure(stderr, "output")
	}
	return 0
}
