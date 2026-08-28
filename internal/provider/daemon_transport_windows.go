//go:build windows

package provider

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const windowsDaemonTransport = "windows_named_pipe"

type clientPipeAddress string

func (value clientPipeAddress) Network() string { return windowsDaemonTransport }
func (value clientPipeAddress) String() string  { return string(value) }

type clientPipeConnection struct {
	file *os.File
	path string
}

func (value *clientPipeConnection) Read(data []byte) (int, error)  { return value.file.Read(data) }
func (value *clientPipeConnection) Write(data []byte) (int, error) { return value.file.Write(data) }
func (value *clientPipeConnection) Close() error                   { return value.file.Close() }
func (value *clientPipeConnection) LocalAddr() net.Addr            { return clientPipeAddress(value.path) }
func (value *clientPipeConnection) RemoteAddr() net.Addr           { return clientPipeAddress(value.path) }
func (value *clientPipeConnection) SetDeadline(deadline time.Time) error {
	return value.file.SetDeadline(deadline)
}
func (value *clientPipeConnection) SetReadDeadline(deadline time.Time) error {
	return value.file.SetReadDeadline(deadline)
}
func (value *clientPipeConnection) SetWriteDeadline(deadline time.Time) error {
	return value.file.SetWriteDeadline(deadline)
}

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
	prefix := `\\.\pipe\LOCAL\v-local-key-provider-`
	if endpoint.Transport != windowsDaemonTransport || !strings.HasPrefix(endpoint.Address, prefix) ||
		len(strings.TrimPrefix(endpoint.Address, prefix)) != 24 {
		return errors.New("daemon named pipe address is invalid")
	}
	return nil
}

func dialWindowsNamedPipe(parent context.Context, path string, expectedServerPID int) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	for {
		handle, openErr := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
			windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_ANONYMOUS, 0)
		if openErr == nil {
			var serverPID uint32
			if err := windows.GetNamedPipeServerProcessId(handle, &serverPID); err != nil || int(serverPID) != expectedServerPID {
				_ = windows.CloseHandle(handle)
				return nil, errors.New("named pipe server PID does not match the trusted endpoint")
			}
			file := os.NewFile(uintptr(handle), path)
			if file == nil {
				_ = windows.CloseHandle(handle)
				return nil, errors.New("named pipe handle could not be attached to the Go poller")
			}
			return &clientPipeConnection{file: file, path: path}, nil
		}
		if !errors.Is(openErr, windows.ERROR_PIPE_BUSY) && !errors.Is(openErr, windows.ERROR_FILE_NOT_FOUND) {
			return nil, openErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func dialAcquisitionDaemonEndpoint(parent context.Context, endpoint acquisitionDaemonEndpoint) (net.Conn, error) {
	if endpoint.Transport == "tcp4-development" && !releaseBuild() {
		dialer := net.Dialer{Timeout: 2 * time.Second}
		return dialer.DialContext(parent, "tcp4", endpoint.Address)
	}
	if endpoint.Transport != windowsDaemonTransport {
		return nil, errors.New("unsupported acquisition daemon transport")
	}
	return dialWindowsNamedPipe(parent, endpoint.Address, endpoint.PID)
}
