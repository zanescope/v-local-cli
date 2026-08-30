package shadowkeychain

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	testAccount    = "aabbccddeeff0011"
	testGeneration = "00112233445566778899aabbccddeeff"
)

type memoryItemStore struct {
	items   map[string][]byte
	failPut string
}

func (value *memoryItemStore) Put(item string, payload []byte) error {
	if item == value.failPut {
		return errors.New("fixture put failure")
	}
	if _, found := value.items[item]; found {
		return errors.New("fixture item collision")
	}
	value.items[item] = append([]byte(nil), payload...)
	return nil
}

func (value *memoryItemStore) Get(item string) ([]byte, bool, error) {
	payload, found := value.items[item]
	return append([]byte(nil), payload...), found, nil
}

func (value *memoryItemStore) Delete(item string) error {
	if payload := value.items[item]; payload != nil {
		clearBytes(payload)
	}
	delete(value.items, item)
	return nil
}

func keychainFixture() *memoryItemStore {
	return &memoryItemStore{items: map[string][]byte{}}
}

func TestGenerationStoresOnlyMinimalCredentialChunksAndStructuralManifest(t *testing.T) {
	store := keychainFixture()
	credential := []byte(`{"database_keys":{"message.db":"` + strings.Repeat("a", 64) + `"}}`)
	request := helperRequest{
		Version: helperVersion, Operation: "put", AccountBindingID: testAccount,
		GenerationID: testGeneration, Credential: append([]byte(nil), credential...),
	}
	response := executeHelperRequest(store, request)
	clearBytes(request.Credential)
	if response.Status != "ok" {
		t.Fatalf("put failed: %+v", response)
	}
	manifestPayload := store.items[manifestItem(testAccount, testGeneration)]
	if bytes.Contains(manifestPayload, credential) || bytes.Contains(manifestPayload, []byte(strings.Repeat("a", 64))) {
		t.Fatal("Keychain manifest retained credential or a secret-derived value")
	}
	var manifest keychainManifest
	if err := json.Unmarshal(manifestPayload, &manifest); err != nil || manifest.Chunks != 1 || manifest.Bytes != len(credential) {
		t.Fatalf("invalid structural manifest: %+v err=%v", manifest, err)
	}
	loaded, found, err := getGeneration(store, testAccount, testGeneration)
	if err != nil || !found || !bytes.Equal(loaded, credential) {
		t.Fatalf("minimal credential round trip failed: found=%v err=%v", found, err)
	}
	clearBytes(loaded)
	if err := deleteGeneration(store, testAccount, testGeneration); err != nil || len(store.items) != 0 {
		t.Fatalf("exact generation residue remains: items=%d err=%v", len(store.items), err)
	}
}

func TestInterruptedPutLeavesNoAuthoritativeManifestAndExactDeleteRecovers(t *testing.T) {
	store := keychainFixture()
	store.failPut = chunkItem(testAccount, testGeneration, 1)
	credential := bytes.Repeat([]byte("s"), chunkBytes+1)
	request := helperRequest{
		Version: helperVersion, Operation: "put", AccountBindingID: testAccount,
		GenerationID: testGeneration, Credential: credential,
	}
	if response := executeHelperRequest(store, request); response.Status != "error" {
		t.Fatalf("partial Keychain put unexpectedly succeeded: %+v", response)
	}
	if len(store.items) != 0 {
		t.Fatalf("best-effort cleanup left %d exact items", len(store.items))
	}
	if _, found, err := getGeneration(store, testAccount, testGeneration); err != nil || found {
		t.Fatalf("partial generation became readable: found=%v err=%v", found, err)
	}
}

func TestGenerationAbsenceRejectsOrphanChunksWithoutManifest(t *testing.T) {
	store := keychainFixture()
	store.items[chunkItem(testAccount, testGeneration, 0)] = []byte("orphan")
	credential, found, err := getGeneration(store, testAccount, testGeneration)
	clearBytes(credential)
	if err == nil || found {
		t.Fatalf("orphan chunk was reported as an absent generation: found=%v err=%v", found, err)
	}
}

func TestPutNeverDeletesOrOverwritesPreexistingGenerationItems(t *testing.T) {
	for name, item := range map[string]string{
		"manifest": manifestItem(testAccount, testGeneration),
		"chunk":    chunkItem(testAccount, testGeneration, 0),
	} {
		t.Run(name, func(t *testing.T) {
			store := keychainFixture()
			store.items[item] = []byte("unowned")
			request := helperRequest{
				Version: helperVersion, Operation: "put", AccountBindingID: testAccount,
				GenerationID: testGeneration, Credential: []byte("minimal-secret"),
			}
			if response := executeHelperRequest(store, request); response.Status != "error" {
				t.Fatalf("preexisting generation item was accepted: %+v", response)
			}
			if len(store.items) != 1 || string(store.items[item]) != "unowned" {
				t.Fatal("preexisting generation item was deleted or overwritten")
			}
		})
	}
}

func TestHelperResponseShapeIsOperationBound(t *testing.T) {
	validGet := helperResponse{Version: helperVersion, Status: "ok", Found: true, Credential: []byte("secret")}
	if err := validGet.validateFor("get"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		operation string
		response  helperResponse
	}{
		{"put credential", "put", validGet},
		{"delete found", "delete", helperResponse{Version: helperVersion, Status: "ok", Found: true}},
		{"get unbound credential", "get", helperResponse{Version: helperVersion, Status: "ok", Credential: []byte("secret")}},
		{"get empty found", "get", helperResponse{Version: helperVersion, Status: "ok", Found: true}},
		{"success error code", "put", helperResponse{Version: helperVersion, Status: "ok", ErrorCode: "unexpected"}},
		{"unbounded failure", "put", helperResponse{Version: helperVersion, Status: "error", ErrorCode: "platform text"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			defer clearBytes(test.response.Credential)
			if err := test.response.validateFor(test.operation); err == nil {
				t.Fatal("malformed helper response was accepted")
			}
		})
	}
}

func TestHelperWireIsStrictBoundedAndDoesNotEchoPlatformErrors(t *testing.T) {
	store := keychainFixture()
	request := helperRequest{
		Version: helperVersion, Operation: "put", AccountBindingID: testAccount,
		GenerationID: testGeneration, Credential: []byte("minimal-secret"),
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := runHelperWithStore(bytes.NewReader(payload), &output, store); code != 0 {
		t.Fatalf("helper code=%d", code)
	}
	if bytes.Contains(output.Bytes(), request.Credential) {
		t.Fatal("put helper response echoed credential input")
	}
	unknown := append([]byte(`{"unknown":true,`), payload[1:]...)
	if code := runHelperWithStore(bytes.NewReader(unknown), &output, store); code != 2 {
		t.Fatalf("unknown helper field code=%d", code)
	}
}
