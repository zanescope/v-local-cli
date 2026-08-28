//go:build !darwin && !windows

package provider

import "errors"

func validateProviderExecutableTrust(_ string) (string, error) {
	if releaseBuild() {
		return "untrusted", errors.New("signed local Provider is not distributed on this platform")
	}
	return unverifiedBuildIntegrity(), nil
}

func validateProviderHelperTrust(_, _ string) (string, error) {
	return "not_applicable", nil
}

func validateAcquisitionDaemonProcessIdentity(_ acquisitionDaemonEndpoint, _ string) error {
	if releaseBuild() {
		return errors.New("acquisition daemon is not released on this platform")
	}
	return nil
}
