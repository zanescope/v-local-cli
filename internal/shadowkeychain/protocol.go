// Package shadowkeychain isolates non-cancellable macOS Keychain calls in a
// bounded helper process. Durable non-secret generation state remains owned by
// shadowpublish; this package stores only the minimal credential bytes plus a
// structural manifest inside Keychain.
package shadowkeychain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	HelperCommand      = "__shadow-keychain-helper"
	helperVersion      = 1
	maxCredentialBytes = 128 * 1024
	maxHelperWireBytes = 256 * 1024
	chunkBytes         = 3 * 1024
	maxChunks          = 43
	manifestSuffix     = "manifest"
)

type helperRequest struct {
	Version          int    `json:"version"`
	Operation        string `json:"operation"`
	AccountBindingID string `json:"account_binding_id"`
	GenerationID     string `json:"generation_id"`
	Credential       []byte `json:"credential,omitempty"`
}

type helperResponse struct {
	Version    int    `json:"version"`
	Status     string `json:"status"`
	Found      bool   `json:"found,omitempty"`
	Credential []byte `json:"credential,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

func (value helperResponse) validateFor(operation string) error {
	if value.Version != helperVersion || (value.Status != "ok" && value.Status != "error") {
		return errors.New("Shadow Keychain helper response status is invalid")
	}
	if value.Status == "error" {
		if value.Found || len(value.Credential) != 0 || value.ErrorCode != "keychain_operation_failed" {
			return errors.New("Shadow Keychain helper failure response is invalid")
		}
		return nil
	}
	if value.ErrorCode != "" {
		return errors.New("successful Shadow Keychain helper response carried an error")
	}
	switch operation {
	case "put", "delete":
		if value.Found || len(value.Credential) != 0 {
			return errors.New("Shadow Keychain mutation response carried credential data")
		}
	case "get":
		if value.Found != (len(value.Credential) > 0) || len(value.Credential) > maxCredentialBytes {
			return errors.New("Shadow Keychain read response shape is invalid")
		}
	default:
		return errors.New("Shadow Keychain helper response operation is invalid")
	}
	return nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func validLowerHex(value string, bytes int) bool {
	if len(value) != bytes*2 || value != strings.ToLower(value) {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func (value helperRequest) validate() error {
	if value.Version != helperVersion || (!validLowerHex(value.AccountBindingID, 8) && !validLowerHex(value.AccountBindingID, 16)) ||
		!validLowerHex(value.GenerationID, 16) {
		return errors.New("Shadow Keychain helper binding is invalid")
	}
	switch value.Operation {
	case "put":
		if len(value.Credential) == 0 || len(value.Credential) > maxCredentialBytes {
			return errors.New("Shadow Keychain helper credential is invalid")
		}
	case "get", "delete":
		if len(value.Credential) != 0 {
			return errors.New("Shadow Keychain read/delete carried credential input")
		}
	default:
		return errors.New("Shadow Keychain helper operation is invalid")
	}
	return nil
}

func itemPrefix(accountID, generationID string) string {
	return accountID + "." + generationID + "."
}

func manifestItem(accountID, generationID string) string {
	return itemPrefix(accountID, generationID) + manifestSuffix
}

func chunkItem(accountID, generationID string, index int) string {
	return itemPrefix(accountID, generationID) + fmt.Sprintf("chunk.%02d", index)
}

type itemStore interface {
	Put(string, []byte) error
	Get(string) ([]byte, bool, error)
	Delete(string) error
}

type keychainManifest struct {
	Version int `json:"version"`
	Chunks  int `json:"chunks"`
	Bytes   int `json:"bytes"`
}

func decodeManifest(payload []byte) (keychainManifest, error) {
	var manifest keychainManifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		manifest.Version != helperVersion || manifest.Chunks < 1 || manifest.Chunks > maxChunks ||
		manifest.Bytes < 1 || manifest.Bytes > maxCredentialBytes ||
		(manifest.Bytes+chunkBytes-1)/chunkBytes != manifest.Chunks {
		return keychainManifest{}, errors.New("Shadow Keychain manifest is invalid")
	}
	return manifest, nil
}

func deleteGeneration(store itemStore, accountID, generationID string) error {
	var failures []error
	if err := store.Delete(manifestItem(accountID, generationID)); err != nil {
		failures = append(failures, errors.New("manifest deletion failed"))
	}
	for index := 0; index < maxChunks; index++ {
		if err := store.Delete(chunkItem(accountID, generationID, index)); err != nil {
			failures = append(failures, errors.New("chunk deletion failed"))
		}
	}
	return errors.Join(failures...)
}

func generationAbsent(store itemStore, accountID, generationID string) (bool, error) {
	items := []string{manifestItem(accountID, generationID)}
	for index := 0; index < maxChunks; index++ {
		items = append(items, chunkItem(accountID, generationID, index))
	}
	for _, item := range items {
		payload, found, err := store.Get(item)
		inconsistent := found != (len(payload) > 0)
		clearBytes(payload)
		if err != nil || inconsistent {
			return false, errors.New("Shadow Keychain generation presence is uncertain")
		}
		if found {
			return false, nil
		}
	}
	return true, nil
}

func deleteWrittenChunks(store itemStore, accountID, generationID string, written int) error {
	var failures []error
	for index := 0; index < written; index++ {
		if err := store.Delete(chunkItem(accountID, generationID, index)); err != nil {
			failures = append(failures, errors.New("written chunk deletion failed"))
		}
	}
	return errors.Join(failures...)
}

func putGeneration(store itemStore, request helperRequest) error {
	absent, err := generationAbsent(store, request.AccountBindingID, request.GenerationID)
	if err != nil || !absent {
		return errors.New("Shadow Keychain generation already exists or is uncertain")
	}
	written := 0
	for offset := 0; offset < len(request.Credential); offset += chunkBytes {
		end := offset + chunkBytes
		if end > len(request.Credential) {
			end = len(request.Credential)
		}
		chunk := append([]byte(nil), request.Credential[offset:end]...)
		err := store.Put(chunkItem(request.AccountBindingID, request.GenerationID, written), chunk)
		clearBytes(chunk)
		if err != nil {
			_ = deleteWrittenChunks(store, request.AccountBindingID, request.GenerationID, written)
			return errors.New("Shadow Keychain chunk write failed")
		}
		written++
	}
	manifest, err := json.Marshal(keychainManifest{Version: helperVersion, Chunks: written, Bytes: len(request.Credential)})
	if err != nil {
		_ = deleteWrittenChunks(store, request.AccountBindingID, request.GenerationID, written)
		return errors.New("Shadow Keychain manifest encoding failed")
	}
	err = store.Put(manifestItem(request.AccountBindingID, request.GenerationID), manifest)
	clearBytes(manifest)
	if err != nil {
		_ = deleteWrittenChunks(store, request.AccountBindingID, request.GenerationID, written)
		return errors.New("Shadow Keychain manifest write failed")
	}
	return nil
}

func getGeneration(store itemStore, accountID, generationID string) ([]byte, bool, error) {
	manifestBytes, found, err := store.Get(manifestItem(accountID, generationID))
	if err != nil {
		clearBytes(manifestBytes)
		return nil, false, err
	}
	if !found {
		clearBytes(manifestBytes)
		for index := 0; index < maxChunks; index++ {
			chunk, chunkFound, err := store.Get(chunkItem(accountID, generationID, index))
			clearBytes(chunk)
			if err != nil {
				return nil, false, err
			}
			if chunkFound {
				return nil, false, errors.New("Shadow Keychain generation has chunks without a manifest")
			}
		}
		return nil, false, nil
	}
	manifest, err := decodeManifest(manifestBytes)
	clearBytes(manifestBytes)
	if err != nil {
		return nil, false, err
	}
	credential := make([]byte, 0, manifest.Bytes)
	for index := 0; index < manifest.Chunks; index++ {
		chunk, chunkFound, err := store.Get(chunkItem(accountID, generationID, index))
		expectedBytes := chunkBytes
		if index == manifest.Chunks-1 {
			expectedBytes = manifest.Bytes - chunkBytes*(manifest.Chunks-1)
		}
		if err != nil || !chunkFound || len(chunk) != expectedBytes {
			clearBytes(chunk)
			clearBytes(credential)
			return nil, false, errors.New("Shadow Keychain credential chunk is missing or invalid")
		}
		credential = append(credential, chunk...)
		clearBytes(chunk)
	}
	if len(credential) != manifest.Bytes {
		clearBytes(credential)
		return nil, false, errors.New("Shadow Keychain credential length drifted")
	}
	for index := manifest.Chunks; index < maxChunks; index++ {
		extra, extraFound, err := store.Get(chunkItem(accountID, generationID, index))
		clearBytes(extra)
		if err != nil || extraFound {
			clearBytes(credential)
			return nil, false, errors.New("Shadow Keychain generation has unexpected residue")
		}
	}
	return credential, true, nil
}

func executeHelperRequest(store itemStore, request helperRequest) helperResponse {
	response := helperResponse{Version: helperVersion, Status: "ok"}
	if request.validate() != nil || store == nil {
		response.Status = "error"
		response.ErrorCode = "invalid_request"
		return response
	}
	var err error
	switch request.Operation {
	case "put":
		err = putGeneration(store, request)
	case "get":
		response.Credential, response.Found, err = getGeneration(store, request.AccountBindingID, request.GenerationID)
	case "delete":
		err = deleteGeneration(store, request.AccountBindingID, request.GenerationID)
	}
	if err != nil {
		clearBytes(response.Credential)
		response.Credential = nil
		response.Found = false
		response.Status = "error"
		response.ErrorCode = "keychain_operation_failed"
	}
	return response
}

// RunHelper is the hidden process entry point. It emits only a bounded stable
// code on failure and never forwards platform error text.
func runHelperWithStore(input io.Reader, output io.Writer, store itemStore) int {
	payload, err := io.ReadAll(io.LimitReader(input, maxHelperWireBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maxHelperWireBytes {
		return 2
	}
	defer clearBytes(payload)
	var request helperRequest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || request.validate() != nil {
		clearBytes(request.Credential)
		return 2
	}
	defer clearBytes(request.Credential)
	if store == nil {
		return 3
	}
	response := executeHelperRequest(store, request)
	defer clearBytes(response.Credential)
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > maxHelperWireBytes-1 {
		clearBytes(encoded)
		return 3
	}
	encoded = append(encoded, '\n')
	_, err = output.Write(encoded)
	clearBytes(encoded)
	if err != nil {
		return 3
	}
	return 0
}

func RunHelper(_ context.Context, input io.Reader, output io.Writer) int {
	store, err := newPlatformStore()
	if err != nil {
		return 3
	}
	return runHelperWithStore(input, output, store)
}
