//go:build darwin

package shadowkeychain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const maxHelperExecutableBytes = int64(128 * 1024 * 1024)

type helperExecutableBinding struct {
	Device    uint64
	Inode     uint64
	UID       uint32
	Mode      uint32
	LinkCount uint64
	Size      int64
	SHA256    string
	CodeHash  string
}

func validHelperDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validHelperCodeHash(value string) bool {
	if len(value) < 40 || len(value) > 128 || len(value)%2 != 0 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func helperBinding(stat unix.Stat_t, digest string) (helperExecutableBinding, error) {
	mode := uint32(stat.Mode) & 0o777
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || mode != 0o555 || stat.Nlink != 1 || stat.Size <= 0 ||
		stat.Size > maxHelperExecutableBytes || stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) ||
		!validHelperDigest(digest) {
		return helperExecutableBinding{}, errors.New("Shadow Keychain helper executable identity is invalid")
	}
	return helperExecutableBinding{
		Device: uint64(stat.Dev), Inode: uint64(stat.Ino), UID: stat.Uid, Mode: mode,
		LinkCount: uint64(stat.Nlink), Size: stat.Size, SHA256: digest,
	}, nil
}

func readHelperExecutable(path string) (*os.File, helperExecutableBinding, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, helperExecutableBinding{}, errors.New("Shadow Keychain helper executable cannot be opened")
	}
	file := os.NewFile(uintptr(fd), path)
	closeWithError := func(err error) (*os.File, helperExecutableBinding, error) {
		_ = file.Close()
		return nil, helperExecutableBinding{}, err
	}
	var before unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil {
		return closeWithError(errors.New("Shadow Keychain helper executable identity is unavailable"))
	}
	hasher := sha256.New()
	read, err := io.Copy(hasher, io.LimitReader(file, maxHelperExecutableBytes+1))
	if err != nil || read != before.Size || read > maxHelperExecutableBytes {
		return closeWithError(errors.New("Shadow Keychain helper executable content is invalid"))
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	bound, err := helperBinding(before, digest)
	if err != nil {
		return closeWithError(err)
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil {
		return closeWithError(errors.New("Shadow Keychain helper executable identity drifted during read"))
	}
	afterBinding, err := helperBinding(after, digest)
	if err != nil || afterBinding != bound {
		return closeWithError(errors.New("Shadow Keychain helper executable identity drifted during read"))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return closeWithError(errors.New("Shadow Keychain helper executable cannot be rewound"))
	}
	return file, bound, nil
}

func inspectHelperExecutable(path, expectedSHA256 string) (helperExecutableBinding, error) {
	if !validHelperDigest(expectedSHA256) {
		return helperExecutableBinding{}, errors.New("Shadow Keychain helper expected digest is invalid")
	}
	file, binding, err := readHelperExecutable(path)
	if err != nil {
		return helperExecutableBinding{}, err
	}
	if binding.SHA256 != expectedSHA256 {
		_ = file.Close()
		return helperExecutableBinding{}, errors.New("Shadow Keychain helper executable digest does not match the build set")
	}
	descriptorPath := "/dev/fd/" + strconv.FormatUint(uint64(file.Fd()), 10)
	// The code-directory hash is a runtime identity binding for the already
	// SHA-256-bound file. It does not replace the frozen build-set signing gate.
	codeHash, codeErr := staticHelperCodeHash(descriptorPath)
	closeErr := file.Close()
	if codeErr != nil || !validHelperCodeHash(codeHash) {
		return helperExecutableBinding{}, errors.New("Shadow Keychain helper code signature is invalid")
	}
	if closeErr != nil {
		return helperExecutableBinding{}, errors.New("Shadow Keychain helper executable close failed")
	}
	binding.CodeHash = codeHash
	return binding, nil
}

func openHelperExecutable(path string, expected helperExecutableBinding) (*os.File, error) {
	file, actual, err := readHelperExecutable(path)
	if err != nil {
		return nil, err
	}
	expected.CodeHash = ""
	if actual != expected {
		_ = file.Close()
		return nil, errors.New("Shadow Keychain helper executable binding changed")
	}
	return file, nil
}

func terminateHelper(command *exec.Cmd, stdin io.Closer) (error, error) {
	_ = stdin.Close()
	killErr := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	waitErr := command.Wait()
	return killErr, waitErr
}

func runHelperProcess(ctx context.Context, path, expectedCodeHash string, input io.Reader, stdout, stderr io.Writer) error {
	if ctx == nil || ctx.Err() != nil {
		return errors.New("Shadow Keychain helper context is unavailable")
	}
	if path == "" || !validHelperCodeHash(expectedCodeHash) || input == nil || stdout == nil || stderr == nil {
		return errors.New("Shadow Keychain helper executable is unavailable")
	}
	command := exec.Command(path, HelperCommand)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	// Do not start copying the request until Security.framework proves that the
	// running PID has the exact code-directory hash captured from the bound fd.
	actualCodeHash, identityErr := runningHelperCodeHash(command.Process.Pid)
	if identityErr != nil || actualCodeHash != expectedCodeHash || ctx.Err() != nil {
		_, _ = terminateHelper(command, stdin)
		return errors.New("Shadow Keychain helper running code identity is invalid")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	written := make(chan error, 1)
	go func() {
		_, writeErr := io.Copy(stdin, input)
		closeErr := stdin.Close()
		if writeErr == nil {
			writeErr = closeErr
		}
		written <- writeErr
	}()
	for {
		select {
		case writeErr := <-written:
			written = nil
			if writeErr != nil {
				killErr := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				waitErr := <-done
				if killErr != nil && !errors.Is(killErr, syscall.ESRCH) && waitErr == nil {
					return errors.New("Shadow Keychain helper input failure could not terminate its process group")
				}
				return errors.New("Shadow Keychain helper input could not be delivered")
			}
		case waitErr := <-done:
			if written != nil {
				if writeErr := <-written; writeErr != nil {
					return errors.New("Shadow Keychain helper exited before consuming its request")
				}
			}
			return waitErr
		case <-ctx.Done():
			killErr := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			waitErr := <-done
			if written != nil {
				<-written
			}
			if killErr != nil && !errors.Is(killErr, syscall.ESRCH) && waitErr == nil {
				return errors.New("Shadow Keychain helper deadline could not terminate its process group")
			}
			return ctx.Err()
		}
	}
}
