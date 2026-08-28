//go:build !windows && !darwin

package provider

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
)

func validateAcquisitionEndpointTransport(endpoint acquisitionDaemonEndpoint) error {
	clientPath, err := os.Executable()
	if err != nil {
		return err
	}
	clientPath, err = filepath.EvalSymlinks(clientPath)
	if err != nil || !sameExecutablePath(endpoint.ClientPath, clientPath) {
		return errors.New("daemon endpoint is not bound to this CLI executable")
	}
	if releaseBuild() || endpoint.Transport != "tcp4-development" {
		return errors.New("acquisition daemon transport is unsupported on this platform")
	}
	return nil
}

func dialAcquisitionDaemonEndpoint(parent context.Context, endpoint acquisitionDaemonEndpoint) (net.Conn, error) {
	if releaseBuild() || endpoint.Transport != "tcp4-development" {
		return nil, errors.New("unsupported acquisition daemon transport")
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	return dialer.DialContext(parent, "tcp4", endpoint.Address)
}
