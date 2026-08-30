package shadowkeychain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
)

type HelperKeychain struct {
	path    string
	binding helperExecutableBinding
}

func NewHelperKeychain(path, expectedSHA256 string) (*HelperKeychain, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("Shadow Keychain helper path must be absolute")
	}
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, errors.New("Shadow Keychain helper path contains a symlink")
	}
	binding, err := inspectHelperExecutable(path, expectedSHA256)
	if err != nil {
		return nil, err
	}
	return &HelperKeychain{path: path, binding: binding}, nil
}

type cappedBuffer struct {
	payload []byte
	over    bool
}

func (value *cappedBuffer) Write(payload []byte) (int, error) {
	remaining := maxHelperWireBytes - len(value.payload)
	if remaining < len(payload) {
		value.over = true
	}
	if remaining > 0 {
		if remaining > len(payload) {
			remaining = len(payload)
		}
		value.payload = append(value.payload, payload[:remaining]...)
	}
	return len(payload), nil
}

func (value *cappedBuffer) clear() {
	clearBytes(value.payload)
	value.payload = nil
}

func (value *HelperKeychain) call(ctx context.Context, request helperRequest) (helperResponse, error) {
	if value == nil || ctx == nil || ctx.Err() != nil || request.validate() != nil {
		return helperResponse{}, errors.New("Shadow Keychain helper request is invalid")
	}
	payload, err := json.Marshal(request)
	if err != nil || len(payload) > maxHelperWireBytes-1 {
		clearBytes(payload)
		return helperResponse{}, errors.New("Shadow Keychain helper request encoding failed")
	}
	payload = append(payload, '\n')
	defer clearBytes(payload)
	stdout := &cappedBuffer{}
	stderr := &cappedBuffer{}
	defer stdout.clear()
	defer stderr.clear()
	resolved, err := filepath.EvalSymlinks(value.path)
	if err != nil || resolved != value.path {
		return helperResponse{}, errors.New("Shadow Keychain helper path identity drifted")
	}
	executable, err := openHelperExecutable(value.path, value.binding)
	if err != nil {
		return helperResponse{}, errors.New("Shadow Keychain helper executable identity drifted")
	}
	defer executable.Close()
	if err := runHelperProcess(ctx, value.path, value.binding.CodeHash, bytes.NewReader(payload), stdout, stderr); err != nil {
		return helperResponse{}, errors.New("Shadow Keychain helper execution was unsuccessful or uncertain")
	}
	current, err := openHelperExecutable(value.path, value.binding)
	if err != nil {
		return helperResponse{}, errors.New("Shadow Keychain helper executable drifted after execution")
	}
	if err := current.Close(); err != nil {
		return helperResponse{}, errors.New("Shadow Keychain helper executable post-check failed")
	}
	if stdout.over || len(stdout.payload) == 0 {
		return helperResponse{}, errors.New("Shadow Keychain helper response is empty or oversized")
	}
	var response helperResponse
	decoder := json.NewDecoder(bytes.NewReader(stdout.payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		response.validateFor(request.Operation) != nil {
		clearBytes(response.Credential)
		return helperResponse{}, errors.New("Shadow Keychain helper response is invalid")
	}
	if response.Status != "ok" {
		clearBytes(response.Credential)
		return helperResponse{}, errors.New("Shadow Keychain helper reported a bounded failure")
	}
	return response, nil
}

func (value *HelperKeychain) Put(ctx context.Context, accountID, generationID string, credential []byte) error {
	if len(credential) == 0 || len(credential) > maxCredentialBytes {
		return errors.New("minimal Shadow credential is outside the Keychain bound")
	}
	copyCredential := append([]byte(nil), credential...)
	defer clearBytes(copyCredential)
	response, err := value.call(ctx, helperRequest{
		Version: helperVersion, Operation: "put", AccountBindingID: accountID,
		GenerationID: generationID, Credential: copyCredential,
	})
	clearBytes(response.Credential)
	return err
}

func (value *HelperKeychain) Get(ctx context.Context, accountID, generationID string) ([]byte, bool, error) {
	response, err := value.call(ctx, helperRequest{
		Version: helperVersion, Operation: "get", AccountBindingID: accountID, GenerationID: generationID,
	})
	if err != nil {
		return nil, false, err
	}
	if !response.Found {
		clearBytes(response.Credential)
		return nil, false, nil
	}
	if len(response.Credential) == 0 || len(response.Credential) > maxCredentialBytes {
		clearBytes(response.Credential)
		return nil, false, errors.New("Shadow Keychain helper returned an invalid credential size")
	}
	return response.Credential, true, nil
}

func (value *HelperKeychain) Delete(ctx context.Context, accountID, generationID string) error {
	response, err := value.call(ctx, helperRequest{
		Version: helperVersion, Operation: "delete", AccountBindingID: accountID, GenerationID: generationID,
	})
	clearBytes(response.Credential)
	return err
}
