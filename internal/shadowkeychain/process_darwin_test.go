//go:build darwin

package shadowkeychain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func helperExecutableFixture(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "v-local-cli")
	source, err := os.Open("/usr/bin/grep")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o555)
	if err != nil {
		t.Fatal(err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(destination, hasher), source); err != nil {
		_ = destination.Close()
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatal(err)
	}
	return path, hex.EncodeToString(hasher.Sum(nil))
}

func TestHelperExecutableBindingRejectsPathReplacement(t *testing.T) {
	path, digest := helperExecutableFixture(t)
	keychain, err := NewHelperKeychain(path, digest)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := openHelperExecutable(keychain.path, keychain.binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := executable.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement helper fixture\n"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatal(err)
	}
	if executable, err := openHelperExecutable(keychain.path, keychain.binding); err == nil {
		_ = executable.Close()
		t.Fatal("replacement helper executable was accepted")
	}
}

func TestNewHelperKeychainRequiresExpectedBuildArtifactDigest(t *testing.T) {
	path, _ := helperExecutableFixture(t)
	if _, err := NewHelperKeychain(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("helper executable outside the expected build artifact was accepted")
	}
}

func TestRunHelperProcessVerifiesRunningCodeBeforeInput(t *testing.T) {
	codeHash, err := staticHelperCodeHash("/usr/bin/grep")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runHelperProcess(ctx, "/usr/bin/grep", codeHash, strings.NewReader(HelperCommand+"\n"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != HelperCommand+"\n" || stderr.Len() != 0 {
		t.Fatal("descriptor execution produced unexpected output")
	}
}

type observedReader struct {
	reads int
}

func (value *observedReader) Read(payload []byte) (int, error) {
	value.reads++
	return copy(payload, []byte("must-not-be-sent\n")), io.EOF
}

func TestRunHelperProcessRejectsWrongRunningIdentityBeforeInput(t *testing.T) {
	codeHash, err := staticHelperCodeHash("/usr/bin/grep")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	input := &observedReader{}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runHelperProcess(ctx, "/usr/bin/grep", strings.Repeat("0", len(codeHash)), input, &stdout, &stderr); err == nil {
		t.Fatal("wrong running code identity was accepted")
	}
	if input.reads != 0 {
		t.Fatal("helper input was read before running code identity verification")
	}
}

func TestStaticCodeHashBindsAnOpenedDescriptor(t *testing.T) {
	executable, err := os.Open("/usr/bin/grep")
	if err != nil {
		t.Fatal(err)
	}
	defer executable.Close()
	pathHash, err := staticHelperCodeHash("/usr/bin/grep")
	if err != nil {
		t.Fatal(err)
	}
	descriptorHash, err := staticHelperCodeHash("/dev/fd/" + strconv.FormatUint(uint64(executable.Fd()), 10))
	if err != nil {
		t.Fatal(err)
	}
	if descriptorHash != pathHash {
		t.Fatal("opened helper descriptor resolved to a different code identity")
	}
}
