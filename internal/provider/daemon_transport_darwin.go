//go:build darwin

package provider

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const darwinDaemonTransport = "darwin_unix"

func validateAcquisitionEndpointTransport(endpoint acquisitionDaemonEndpoint) error {
	clientPath, err := os.Executable()
	if err != nil {
		return err
	}
	clientPath, err = filepath.EvalSymlinks(clientPath)
	if err != nil || !sameExecutablePath(endpoint.ClientPath, clientPath) {
		return errors.New("daemon endpoint is not bound to this CLI executable")
	}
	if endpoint.Transport == "tcp4-development" {
		if releaseBuild() {
			return errors.New("release CLI refuses the development TCP daemon transport")
		}
		return nil
	}
	if endpoint.Transport != darwinDaemonTransport || !filepath.IsAbs(endpoint.Address) ||
		!strings.HasPrefix(filepath.Base(endpoint.Address), ".v-local-key-provider-") ||
		!strings.HasSuffix(endpoint.Address, ".sock") {
		return errors.New("daemon Unix socket address is invalid")
	}
	return nil
}

func unixPeerPID(connection *net.UnixConn) (int, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	pid := 0
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		pid, socketErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return 0, err
	}
	if socketErr != nil || pid <= 0 {
		return 0, errors.New("daemon Unix peer PID is unavailable")
	}
	return pid, nil
}

func dialAcquisitionDaemonEndpoint(parent context.Context, endpoint acquisitionDaemonEndpoint) (net.Conn, error) {
	if endpoint.Transport == "tcp4-development" && !releaseBuild() {
		dialer := net.Dialer{Timeout: 2 * time.Second}
		return dialer.DialContext(parent, "tcp4", endpoint.Address)
	}
	if endpoint.Transport != darwinDaemonTransport {
		return nil, errors.New("unsupported acquisition daemon transport")
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	connection, err := dialer.DialContext(parent, "unix", endpoint.Address)
	if err != nil {
		return nil, err
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, errors.New("daemon connection is not a Unix-domain socket")
	}
	pid, err := unixPeerPID(unixConnection)
	if err != nil || pid != endpoint.PID {
		_ = connection.Close()
		return nil, errors.New("daemon Unix peer PID does not match the trusted endpoint")
	}
	return connection, nil
}
