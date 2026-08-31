package shadowsource

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
)

const ManifestVersion = 1

type RewriteReference struct {
	Path string `json:"path"`
	Key  string `json:"key"`
}

type RewriteInput struct {
	Path     string `json:"path"`
	Key      string `json:"key"`
	Expected string `json:"expected"`
}

type Identity struct {
	Device    uint64 `json:"device"`
	Inode     uint64 `json:"inode"`
	UID       uint32 `json:"uid"`
	Mode      uint32 `json:"mode"`
	LinkCount uint64 `json:"link_count"`
}

type Snapshot struct {
	SourceLeaf        string         `json:"source_leaf"`
	SourcePathDigest  string         `json:"source_path_digest"`
	SourceVersion     string         `json:"source_version"`
	SourceBuild       string         `json:"source_build"`
	RootIdentifier    string         `json:"root_identifier"`
	TeamIdentifier    string         `json:"team_identifier"`
	RequirementDigest string         `json:"requirement_digest"`
	InventoryDigest   string         `json:"inventory_digest"`
	InventoryEntries  int            `json:"inventory_entries"`
	Identity          Identity       `json:"identity"`
	RewriteInputs     []RewriteInput `json:"rewrite_inputs"`
}

type Manifest struct {
	Version                      int            `json:"version"`
	SourceLeaf                   string         `json:"source_leaf"`
	SourcePathDigest             string         `json:"source_path_digest"`
	SourceVersion                string         `json:"source_version"`
	SourceBuild                  string         `json:"source_build"`
	RootIdentifier               string         `json:"root_identifier"`
	TeamIdentifier               string         `json:"team_identifier"`
	RequirementDigest            string         `json:"requirement_digest"`
	InventoryDigest              string         `json:"inventory_digest"`
	InventoryEntries             int            `json:"inventory_entries"`
	ExpectedUID                  uint32         `json:"expected_uid"`
	ExpectedMode                 uint32         `json:"expected_mode"`
	ExpectedLinkCount            uint64         `json:"expected_link_count"`
	TransformationManifestDigest string         `json:"transformation_manifest_digest"`
	RewriteInputs                []RewriteInput `json:"rewrite_inputs"`
}

type Qualification struct {
	ManifestDigest      string   `json:"manifest_digest"`
	QualificationDigest string   `json:"qualification_digest"`
	Snapshot            Snapshot `json:"snapshot"`
}

func lowerHex(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func safeRelative(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\\:") && !path.IsAbs(value) &&
		path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../")
}

func sortedRewriteInputs(values []RewriteInput) bool {
	for index, value := range values {
		if !safeRelative(value.Path) || strings.TrimSpace(value.Key) == "" || strings.TrimSpace(value.Expected) == "" {
			return false
		}
		if index > 0 {
			previous := values[index-1].Path + "\x00" + values[index-1].Key
			current := value.Path + "\x00" + value.Key
			if previous >= current {
				return false
			}
		}
	}
	return true
}

func (value Identity) validate() error {
	if value.Device == 0 || value.Inode == 0 || value.Mode == 0 || value.LinkCount == 0 {
		return errors.New("source identity is incomplete")
	}
	return nil
}

func (value Snapshot) Validate() error {
	if value.SourceLeaf != "WeChat.app" || !lowerHex(value.SourcePathDigest) ||
		strings.TrimSpace(value.SourceVersion) == "" || strings.TrimSpace(value.SourceBuild) == "" ||
		strings.TrimSpace(value.RootIdentifier) == "" || strings.TrimSpace(value.TeamIdentifier) == "" ||
		!lowerHex(value.RequirementDigest) || !lowerHex(value.InventoryDigest) || value.InventoryEntries <= 0 ||
		len(value.RewriteInputs) == 0 || !sortedRewriteInputs(value.RewriteInputs) {
		return errors.New("source snapshot is invalid")
	}
	return value.Identity.validate()
}

func (value Manifest) Validate() error {
	if value.Version != ManifestVersion || value.SourceLeaf != "WeChat.app" || !lowerHex(value.SourcePathDigest) ||
		strings.TrimSpace(value.SourceVersion) == "" || strings.TrimSpace(value.SourceBuild) == "" ||
		strings.TrimSpace(value.RootIdentifier) == "" || strings.TrimSpace(value.TeamIdentifier) == "" ||
		!lowerHex(value.RequirementDigest) || !lowerHex(value.InventoryDigest) || value.InventoryEntries <= 0 ||
		value.ExpectedMode == 0 || value.ExpectedLinkCount == 0 || !lowerHex(value.TransformationManifestDigest) ||
		len(value.RewriteInputs) == 0 || !sortedRewriteInputs(value.RewriteInputs) {
		return errors.New("source qualification manifest is invalid")
	}
	return nil
}

func sortRewriteInputs(values []RewriteInput) {
	sort.Slice(values, func(left, right int) bool {
		return values[left].Path+"\x00"+values[left].Key < values[right].Path+"\x00"+values[right].Key
	})
}

func Freeze(snapshot Snapshot, transformationManifestDigest string) (Manifest, error) {
	copy := snapshot
	copy.RewriteInputs = append([]RewriteInput(nil), snapshot.RewriteInputs...)
	sortRewriteInputs(copy.RewriteInputs)
	if err := copy.Validate(); err != nil || !lowerHex(transformationManifestDigest) {
		return Manifest{}, errors.New("source snapshot cannot be frozen")
	}
	manifest := Manifest{
		Version: ManifestVersion, SourceLeaf: copy.SourceLeaf, SourcePathDigest: copy.SourcePathDigest,
		SourceVersion: copy.SourceVersion, SourceBuild: copy.SourceBuild,
		RootIdentifier: copy.RootIdentifier, TeamIdentifier: copy.TeamIdentifier,
		RequirementDigest: copy.RequirementDigest, InventoryDigest: copy.InventoryDigest,
		InventoryEntries: copy.InventoryEntries, ExpectedUID: copy.Identity.UID,
		ExpectedMode: copy.Identity.Mode, ExpectedLinkCount: copy.Identity.LinkCount,
		TransformationManifestDigest: transformationManifestDigest,
		RewriteInputs:                append([]RewriteInput(nil), copy.RewriteInputs...),
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, errors.New("source snapshot cannot be frozen")
	}
	return manifest, nil
}

func canonical(value any) ([]byte, string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	payload = append(bytes.TrimSpace(payload), '\n')
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func CanonicalManifest(value Manifest) ([]byte, string, error) {
	if err := value.Validate(); err != nil {
		return nil, "", err
	}
	return canonical(value)
}

func DecodeManifest(payload []byte) (Manifest, error) {
	var value Manifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF || value.Validate() != nil {
		return Manifest{}, errors.New("source manifest is invalid")
	}
	return value, nil
}

func qualificationDigest(manifestDigest string, snapshot Snapshot) (string, error) {
	if !lowerHex(manifestDigest) || snapshot.Validate() != nil {
		return "", errors.New("source qualification digest input is invalid")
	}
	document := struct {
		Version        int      `json:"version"`
		ManifestDigest string   `json:"manifest_digest"`
		Snapshot       Snapshot `json:"snapshot"`
	}{Version: ManifestVersion, ManifestDigest: manifestDigest, Snapshot: snapshot}
	_, digest, err := canonical(document)
	return digest, err
}
