package app

import (
	"strings"
	"testing"

	contract "github.com/zanescope/v-local-cli/internal/shadowcontract"
	ownermodel "github.com/zanescope/v-local-cli/internal/shadowowner"
	qualificationmodel "github.com/zanescope/v-local-cli/internal/shadowqualification"
)

func matchingQualification() (qualificationmodel.Result, contract.Result) {
	local := qualificationmodel.Result{
		Binding: ownermodel.Binding{
			BuildSetDigest: strings.Repeat("2", 64), SourceQualificationDigest: strings.Repeat("3", 64),
			CleanupRoute: contract.CleanupRouteDirect, AccountBindingID: strings.Repeat("4", 32),
			OptionsDigest: strings.Repeat("5", 64),
		},
		SourceVersion: "4.1.11", SourceBuild: "269136",
	}
	qualification := contract.Qualification{
		Version: contract.Version, BuildSetDigest: local.Binding.BuildSetDigest,
		SourceQualificationDigest: local.Binding.SourceQualificationDigest, CleanupRoute: local.Binding.CleanupRoute,
		AccountBindingID: local.Binding.AccountBindingID, OptionsDigest: local.Binding.OptionsDigest,
		SourceVersion: local.SourceVersion, SourceBuild: local.SourceBuild, ProductionRouteEnabled: false,
	}
	remote := contract.Result{
		Version: contract.Version, RequestID: strings.Repeat("1", 32), Status: "qualified",
		ErrorCode: contract.ErrorNone, Qualification: &qualification,
	}
	return local, remote
}

func TestQualificationPairMustMatchEveryBindingAndRemainDisabled(t *testing.T) {
	local, remote := matchingQualification()
	if err := exactQualificationMatch(local, remote); err != nil {
		t.Fatal(err)
	}
	remote.Qualification.ProductionRouteEnabled = true
	if err := exactQualificationMatch(local, remote); err == nil {
		t.Fatal("qualification-only command accepted an enabled production route")
	}
	remote.Qualification.ProductionRouteEnabled = false
	remote.Qualification.SourceQualificationDigest = strings.Repeat("f", 64)
	if err := exactQualificationMatch(local, remote); err == nil {
		t.Fatal("qualification-only command accepted a cross-process digest mismatch")
	}
}
