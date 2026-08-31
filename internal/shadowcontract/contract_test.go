package shadowcontract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const goldenDigest = "076179a56a4986e281225f20e338afcccb0a09c6fa192dda2999c53627da8fd2"

func goldenPayload(t *testing.T) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "testdata", "shadow-contract-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func goldenVectors(t *testing.T) GoldenVectors {
	t.Helper()
	payload := goldenPayload(t)
	var vectors GoldenVectors
	if err := DecodeStrict(payload, &vectors); err != nil {
		t.Fatalf("strict decode failed: %v", err)
	}
	if err := vectors.Validate(); err != nil {
		t.Fatalf("semantic validation failed: %v", err)
	}
	return vectors
}

func TestGoldenVectorsAreCanonicalAndDigestBound(t *testing.T) {
	payload := goldenPayload(t)
	vectors := goldenVectors(t)
	canonical, err := CanonicalJSON(vectors)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, canonical) {
		t.Fatal("golden Shadow contract is not canonical JSON")
	}
	if actual := Digest(payload); actual != goldenDigest {
		t.Fatalf("golden Shadow contract digest changed: got %s want %s", actual, goldenDigest)
	}
}

func TestGoldenVectorsContainNoDurableSecretOrAbsolutePathFields(t *testing.T) {
	var value any
	if err := json.Unmarshal(goldenPayload(t), &value); err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{
		"secret": true, "password": true, "database_keys": true, "image_keys": true,
		"catalog_id": true, "account_path": true, "source_path": true, "absolute_path": true,
	}
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for name, child := range typed {
				if forbidden[name] {
					t.Fatalf("golden Shadow contract contains forbidden durable field %q", name)
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
}

func TestStrictDecoderRejectsUnknownFields(t *testing.T) {
	payload := bytes.Replace(goldenPayload(t), []byte(`{"version":`), []byte(`{"unknown":true,"version":`), 1)
	var vectors GoldenVectors
	if err := DecodeStrict(payload, &vectors); err == nil {
		t.Fatal("unknown Shadow contract field was accepted")
	}
}

func TestDeadlineCannotResetAnyStage(t *testing.T) {
	value := NewDeadline(10)
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	value.CLIVerifyNS++
	if err := value.Validate(); err == nil {
		t.Fatal("independently reset Shadow stage deadline was accepted")
	}
}

func TestDeadlineRejectsUint64Overflow(t *testing.T) {
	value := NewDeadline(^uint64(0) - ReturnWindowNS + 1)
	if err := value.Validate(); err == nil {
		t.Fatal("overflowed absolute Shadow deadline was accepted")
	}
}

func TestChallengeOperationMustMatchExecutionRoute(t *testing.T) {
	vectors := goldenVectors(t)
	vectors.Challenge.Operation = "execute"
	if err := vectors.Validate(); err == nil {
		t.Fatal("synthetic execution request accepted a production-bound challenge")
	}
}

func TestReadyRequiresEveryCleanupFactAndResourceClass(t *testing.T) {
	vectors := goldenVectors(t)
	result := vectors.ReadyResult
	result.Receipt.Cleanup.SocketAbsent = false
	if err := result.Validate(); err == nil {
		t.Fatal("ready result accepted incomplete cleanup facts")
	}
	result = vectors.ReadyResult
	result.Receipt.Resources = result.Receipt.Resources[:len(result.Receipt.Resources)-1]
	if err := result.Validate(); err == nil {
		t.Fatal("ready result accepted a receipt without the supervisor binding")
	}
}

func TestCleanupReceiptRejectsCrossAttemptAndDuplicateBindings(t *testing.T) {
	vectors := goldenVectors(t)
	tests := map[string]func(*CleanupReceipt){
		"workspace":       func(value *CleanupReceipt) { value.Resources[0].Leaf = "attempt-other" },
		"duplicate_class": func(value *CleanupReceipt) { value.Resources[6] = value.Resources[3] },
		"process": func(value *CleanupReceipt) {
			value.Process.ExecutableLeaf = value.RootLeaf + "/WeChat.app/Contents/Resources/WeChat"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			receipt := vectors.CleanupReceipt
			receipt.Resources = append([]ResourceBinding(nil), vectors.CleanupReceipt.Resources...)
			process := *vectors.CleanupReceipt.Process
			receipt.Process = &process
			mutate(&receipt)
			if err := receipt.Validate(); err == nil {
				t.Fatal("invalid cleanup receipt binding was accepted")
			}
		})
	}
}

func TestGoldenVectorsRejectReceiptRequestBindingDrift(t *testing.T) {
	for name, mutate := range map[string]func(*CleanupReceipt){
		"account":   func(value *CleanupReceipt) { value.AccountBindingID = "0011223344556677" },
		"operation": func(value *CleanupReceipt) { value.Operation = "execute" },
	} {
		t.Run(name, func(t *testing.T) {
			vectors := goldenVectors(t)
			receipt := vectors.CleanupReceipt
			mutate(&receipt)
			vectors.CleanupReceipt = receipt
			readyReceipt := receipt
			vectors.ReadyResult.Receipt = &readyReceipt
			if err := vectors.Validate(); err == nil {
				t.Fatal("cleanup receipt drifted from its execution request")
			}
		})
	}
}

func TestResourceLinkCountDistinguishesDirectoriesFromFiles(t *testing.T) {
	directory := ResourceBinding{Kind: "container", Leaf: "container", Device: 1, Inode: 2, UID: 501, Mode: 0o700, LinkCount: 2}
	if err := directory.Validate(); err != nil {
		t.Fatalf("ordinary macOS directory link count was rejected: %v", err)
	}
	file := ResourceBinding{Kind: "hook", Leaf: "hook", Device: 1, Inode: 3, UID: 501, Mode: 0o600, LinkCount: 2}
	if err := file.Validate(); err == nil {
		t.Fatal("hard-linked file binding was accepted")
	}
}

func TestResourceLeafUsesHostIndependentWirePathSemantics(t *testing.T) {
	valid := ResourceBinding{
		Kind: "hook", Leaf: "attempt-0123456789abcdef0123456789abcdef/capture/hook.fifo",
		Device: 1, Inode: 2, UID: 501, Mode: 0o600, LinkCount: 1,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("slash-delimited wire leaf was rejected on %s: %v", runtime.GOOS, err)
	}
	for _, leaf := range []string{
		`C:/shadow/hook.fifo`, `C:\\shadow\\hook.fifo`, `attempt/../hook.fifo`,
		`attempt//hook.fifo`, `attempt/hook.fifo:stream`, `/attempt/hook.fifo`,
	} {
		invalid := valid
		invalid.Leaf = leaf
		if err := invalid.Validate(); err == nil {
			t.Errorf("host-dependent or non-canonical leaf %q was accepted", leaf)
		}
	}
}
