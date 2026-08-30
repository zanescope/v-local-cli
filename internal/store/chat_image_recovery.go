package store

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

const chatImageDirectCDNHost = "novac2c.cdn.weixin.qq.com"

type ChatImageRemoteRecoveryInspection struct {
	EvidenceID                  string
	Chat                        string
	LocalID                     int64
	ServerID                    int64
	Timestamp                   int64
	SortKey                     int64
	LocalQualityTier            string
	RemoteDescriptorStatus      string
	RemoteDescriptorParseStatus string
	RemoteProtocolStatus        string
	RemoteDescriptorTiers       []string
	AcquisitionStatus           string
	CandidateTier               string
	CandidateDescriptorSHA256   string
	MessageBindingSHA256        string
	DescriptorBinding           string
	NetworkDestination          string
	SourceOriginalQualityStatus string
}

type ChatImageRemoteArtifact struct {
	EvidenceID                  string
	Chat                        string
	LocalID                     int64
	ServerID                    int64
	Timestamp                   int64
	SortKey                     int64
	Format                      string
	Bytes                       int
	Width                       int
	Height                      int
	SHA256                      string
	ContentMD5                  string
	QualityTier                 string
	QualityBasis                string
	SourceOriginalQualityStatus string
	ContainerValidation         string
	DecryptionScope             string
	MIMEStatus                  string
	DescriptorBytesStatus       string
	DescriptorDimensionsStatus  string
	DescriptorMD5Status         string
	DescriptorSHA256            string
	MessageBindingSHA256        string
	RetrievedAt                 string
	NetworkAccessPerformed      bool
	Data                        []byte
}

type ChatImageRemoteRecoveryError struct {
	Kind                   string
	DescriptorExpiryStatus string
	NetworkAttempted       bool
}

func (err *ChatImageRemoteRecoveryError) Error() string { return err.Kind }

func newChatImageRemoteRecoveryError(kind string, attempted bool) *ChatImageRemoteRecoveryError {
	expiryStatus := "not_evaluated"
	switch kind {
	case "chat_image_remote_authorization_rejected", "chat_image_remote_resource_unavailable", "chat_image_remote_http_status":
		expiryStatus = "unknown_unavailable_at_request_time"
	case "chat_image_remote_rate_limited":
		expiryStatus = "not_evidence_of_expiry"
	case "chat_image_remote_redirect_rejected", "chat_image_remote_request_failed", "chat_image_remote_dns_failed",
		"chat_image_remote_direct_dns_failed", "chat_image_remote_direct_dns_transport_failed",
		"chat_image_remote_connection_failed", "chat_image_remote_response_read_failed",
		"chat_image_remote_non_public_address", "chat_image_remote_synthetic_proxy_address",
		"chat_image_remote_invalid_address":
		expiryStatus = "unknown_after_request_failure"
	case "chat_image_remote_response_size_invalid", "chat_image_remote_mime_invalid", "chat_image_remote_mime_mismatch",
		"chat_image_remote_decrypt_failed", "chat_image_remote_container_invalid", "chat_image_remote_descriptor_size_mismatch",
		"chat_image_remote_descriptor_dimensions_mismatch", "chat_image_remote_descriptor_md5_mismatch":
		expiryStatus = "response_unverified"
	}
	return &ChatImageRemoteRecoveryError{Kind: kind, DescriptorExpiryStatus: expiryStatus, NetworkAttempted: attempted}
}

func parseDirectChatImageURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > maxChatImageRemoteParameterBytes {
		return "", false
	}
	target, err := url.Parse(value)
	if err != nil || !strings.EqualFold(target.Scheme, "https") || target.User != nil || target.Fragment != "" ||
		target.Hostname() == "" || target.Port() != "" || target.Opaque != "" || target.RawPath != "" || target.ForceQuery {
		return "", false
	}
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	if host != chatImageDirectCDNHost || target.Path != "/c2c/download" {
		return "", false
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil || len(query) != 1 {
		return "", false
	}
	parameters, found := query["encrypted_query_param"]
	if !found || len(parameters) != 1 || !validRemoteToken(parameters[0]) {
		return "", false
	}
	target.Scheme = "https"
	target.Host = chatImageDirectCDNHost
	return target.String(), true
}

func writeChatImageBindingField(destination hash.Hash, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write(value)
}

func chatImageMessageBinding(message Message) string {
	digest := sha256.New()
	for _, value := range []string{
		message.EvidenceID, message.Chat, strconv.FormatInt(message.LocalID, 10), strconv.FormatInt(message.ServerID, 10),
		strconv.FormatInt(message.Timestamp, 10), strconv.FormatInt(message.SortKey, 10), message.SourceDB,
	} {
		writeChatImageBindingField(digest, []byte(value))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func chatImageRemoteCandidateFingerprint(message Message, candidate *chatImageRemoteCandidate) string {
	if candidate == nil {
		return ""
	}
	hash := sha256.New()
	writeChatImageBindingField(hash, []byte(chatImageMessageBinding(message)))
	for _, value := range []string{
		candidate.tier, candidate.encryptedQueryParameter, candidate.parameterEncoding,
		strconv.FormatInt(candidate.expectedBytes, 10), strconv.Itoa(candidate.expectedWidth),
		strconv.Itoa(candidate.expectedHeight), candidate.expectedMD5,
	} {
		writeChatImageBindingField(hash, []byte(value))
	}
	writeChatImageBindingField(hash, candidate.aesKey[:])
	return hex.EncodeToString(hash.Sum(nil))
}

func chatImageDescriptorBinding(candidate *chatImageRemoteCandidate) string {
	if candidate == nil {
		return "none"
	}
	if candidate.expectedMD5 != "" {
		return "plaintext_md5"
	}
	if candidate.expectedBytes > 0 && candidate.expectedWidth > 0 && candidate.expectedHeight > 0 {
		return "plaintext_size_and_dimensions"
	}
	return "insufficient"
}

func inspectChatImageRemoteRecovery(root, evidenceID, localTier string) (ChatImageRemoteRecoveryInspection, chatImageRemoteDescriptor, int, error) {
	message, err := FindImageMessage(root, evidenceID)
	if err != nil {
		return ChatImageRemoteRecoveryInspection{}, unknownChatImageRemoteDescriptor(), -1,
			newChatImageRemoteRecoveryError("chat_image_recovery_evidence_unavailable", false)
	}
	descriptor := inspectChatImageRemoteDescriptor(root, message)
	inspection := ChatImageRemoteRecoveryInspection{
		EvidenceID: message.EvidenceID, Chat: message.Chat, LocalID: message.LocalID, ServerID: message.ServerID,
		Timestamp: message.Timestamp, SortKey: message.SortKey, LocalQualityTier: localTier,
		RemoteDescriptorStatus: descriptor.status, RemoteDescriptorParseStatus: descriptor.parseStatus,
		RemoteProtocolStatus: descriptor.protocolStatus, RemoteDescriptorTiers: append([]string(nil), descriptor.tiers...),
		MessageBindingSHA256: chatImageMessageBinding(message), SourceOriginalQualityStatus: "unknown",
	}
	if localTier == "unknown" {
		inspection.AcquisitionStatus = "local_quality_tier_unknown_manual_review"
		return inspection, descriptor, -1, nil
	}
	localRank := 0
	if localTier != "" {
		localRank = chatImageQualityRank(localTier)
	}
	selected := -1
	higherOpaqueCandidate := false
	for index := range descriptor.candidates {
		candidate := &descriptor.candidates[index]
		if chatImageQualityRank(candidate.tier) <= localRank {
			continue
		}
		if candidate.parameterEncoding != "direct_https_url" {
			higherOpaqueCandidate = true
			continue
		}
		if selected < 0 || chatImageQualityRank(candidate.tier) > chatImageQualityRank(descriptor.candidates[selected].tier) {
			selected = index
		}
	}
	if selected >= 0 {
		candidate := &descriptor.candidates[selected]
		inspection.AcquisitionStatus = "direct_https_candidate_available"
		inspection.CandidateTier = candidate.tier
		inspection.CandidateDescriptorSHA256 = chatImageRemoteCandidateFingerprint(message, candidate)
		inspection.DescriptorBinding = chatImageDescriptorBinding(candidate)
		inspection.NetworkDestination = chatImageDirectCDNHost
		return inspection, descriptor, selected, nil
	}
	switch {
	case higherOpaqueCandidate:
		inspection.AcquisitionStatus = "unavailable_unverified_desktop_protocol"
	case len(descriptor.candidates) > 0:
		inspection.AcquisitionStatus = "only_lower_or_equal_remote_variant"
	case descriptor.status == "missing":
		inspection.AcquisitionStatus = "remote_descriptor_missing"
	case descriptor.parseStatus == "present_incomplete" || descriptor.parseStatus == "present_invalid":
		inspection.AcquisitionStatus = "remote_descriptor_not_attemptable"
	default:
		inspection.AcquisitionStatus = "remote_descriptor_unknown"
	}
	return inspection, descriptor, -1, nil
}

func InspectChatImageRemoteRecovery(root, evidenceID, localTier string) (ChatImageRemoteRecoveryInspection, error) {
	inspection, descriptor, _, err := inspectChatImageRemoteRecovery(root, evidenceID, localTier)
	descriptor.clear()
	return inspection, err
}

func newSafeChatImageDownloader() *safeMomentDownloader {
	dialer := &momentSafeDialer{
		resolver: net.DefaultResolver,
		dialer:   net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1},
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 * 1024,
		DisableKeepAlives:      true,
		DisableCompression:     true,
	}
	return &safeMomentDownloader{client: &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func responseImageMIME(contentType string) (string, bool) {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return "", false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", false
	}
	mediaType = strings.ToLower(mediaType)
	switch mediaType {
	case "application/octet-stream", "image/jpeg", "image/png", "image/gif", "image/webp":
		return mediaType, true
	default:
		return mediaType, false
	}
}

func imageFormatMIME(format string) string {
	switch format {
	case "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}

func downloadAndVerifyChatImageCandidate(
	ctx context.Context,
	message Message,
	candidate *chatImageRemoteCandidate,
	target *url.URL,
	downloader momentRemoteDownloader,
) (ChatImageRemoteArtifact, error) {
	if candidate == nil || !chatImageRemoteCandidateHasBindingMetadata(candidate) || downloader == nil {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_descriptor_binding_insufficient", false)
	}
	response, err := downloader.Download(ctx, target, maxChatImageRemoteResponseBytes)
	if len(response.Payload) > 0 {
		defer clear(response.Payload)
	}
	if err != nil {
		kind := "request_failed"
		var downloadErr *momentDownloadError
		if errors.As(err, &downloadErr) && downloadErr.Kind != "" {
			kind = downloadErr.Kind
		}
		networkAttempted := kind != "request_build_failed"
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_"+kind, networkAttempted)
	}
	if response.Path != "" || len(response.Payload) == 0 || response.Bytes != int64(len(response.Payload)) {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_response_read_failed", true)
	}
	responseMIME, mimeAllowed := responseImageMIME(response.ContentType)
	if !mimeAllowed {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_mime_invalid", true)
	}
	rawValidation, rawValidationErr := cryptoutil.ValidateImageStructure(response.Payload)
	plain := []byte(nil)
	decryptionScope := "full_payload_aes_128_ecb_pkcs7"
	if rawValidationErr == nil {
		if response.Encrypted {
			return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_mime_mismatch", true)
		}
		plain = append([]byte(nil), response.Payload...)
		decryptionScope = "not_required"
		if expected := imageFormatMIME(rawValidation.Format); responseMIME != "application/octet-stream" && responseMIME != expected {
			clear(plain)
			return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_mime_mismatch", true)
		}
	} else {
		if responseMIME != "application/octet-stream" {
			return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_mime_mismatch", true)
		}
		plain, err = decryptChatImageAES128ECBPKCS7(response.Payload, candidate.aesKey)
		if err != nil {
			return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_decrypt_failed", true)
		}
	}
	if len(plain) == 0 || len(plain) > maxChatImageBytes {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_response_size_invalid", true)
	}
	verified := false
	defer func() {
		if !verified {
			clear(plain)
		}
	}()
	validation, err := cryptoutil.ValidateImageStructure(plain)
	if err != nil {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_container_invalid", true)
	}
	bytesStatus := "not_provided"
	if candidate.expectedBytes > 0 {
		if candidate.expectedBytes != int64(len(plain)) {
			return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_descriptor_size_mismatch", true)
		}
		bytesStatus = "match"
	}
	dimensionsStatus := "not_provided"
	if candidate.expectedWidth > 0 || candidate.expectedHeight > 0 {
		if candidate.expectedWidth != validation.Width || candidate.expectedHeight != validation.Height {
			return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_descriptor_dimensions_mismatch", true)
		}
		dimensionsStatus = "match_observation_not_quality_gate"
	}
	md5Digest := md5.Sum(plain)
	contentMD5 := hex.EncodeToString(md5Digest[:])
	md5Status := "not_provided"
	if candidate.expectedMD5 != "" {
		if !strings.EqualFold(candidate.expectedMD5, contentMD5) {
			return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_descriptor_md5_mismatch", true)
		}
		md5Status = "match"
	}
	sha := sha256.Sum256(plain)
	verified = true
	return ChatImageRemoteArtifact{
		EvidenceID: message.EvidenceID, Chat: message.Chat, LocalID: message.LocalID, ServerID: message.ServerID,
		Timestamp: message.Timestamp, SortKey: message.SortKey, Format: validation.Format, Bytes: len(plain),
		Width: validation.Width, Height: validation.Height, SHA256: hex.EncodeToString(sha[:]), ContentMD5: contentMD5,
		QualityTier: candidate.tier, QualityBasis: "snapshot_remote_descriptor_variant",
		SourceOriginalQualityStatus: "unknown", ContainerValidation: validation.Method, DecryptionScope: decryptionScope,
		MIMEStatus: "response_mime_and_decoded_structure_consistent", DescriptorBytesStatus: bytesStatus,
		DescriptorDimensionsStatus: dimensionsStatus, DescriptorMD5Status: md5Status,
		DescriptorSHA256: chatImageRemoteCandidateFingerprint(message, candidate), MessageBindingSHA256: chatImageMessageBinding(message),
		RetrievedAt: time.Now().UTC().Format(time.RFC3339Nano), NetworkAccessPerformed: true, Data: plain,
	}, nil
}

func recoverChatImageRemoteWithDownloader(
	ctx context.Context,
	root, evidenceID, localTier, expectedDescriptorSHA256 string,
	downloader momentRemoteDownloader,
) (ChatImageRemoteArtifact, error) {
	inspection, descriptor, selected, err := inspectChatImageRemoteRecovery(root, evidenceID, localTier)
	defer descriptor.clear()
	if err != nil {
		return ChatImageRemoteArtifact{}, err
	}
	if selected < 0 || inspection.AcquisitionStatus != "direct_https_candidate_available" {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_candidate_unavailable", false)
	}
	if expectedDescriptorSHA256 == "" || inspection.CandidateDescriptorSHA256 != expectedDescriptorSHA256 {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_descriptor_mismatch", false)
	}
	targetString, valid := parseDirectChatImageURL(descriptor.candidates[selected].encryptedQueryParameter)
	if !valid {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_url_rejected", false)
	}
	target, err := url.Parse(targetString)
	if err != nil {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_url_rejected", false)
	}
	message, err := FindImageMessage(root, evidenceID)
	if err != nil || chatImageMessageBinding(message) != inspection.MessageBindingSHA256 {
		return ChatImageRemoteArtifact{}, newChatImageRemoteRecoveryError("chat_image_remote_message_binding_mismatch", false)
	}
	return downloadAndVerifyChatImageCandidate(ctx, message, &descriptor.candidates[selected], target, downloader)
}

func RecoverChatImageRemote(
	ctx context.Context,
	root, evidenceID, localTier, expectedDescriptorSHA256 string,
) (ChatImageRemoteArtifact, error) {
	return recoverChatImageRemoteWithDownloader(ctx, root, evidenceID, localTier, expectedDescriptorSHA256, newSafeChatImageDownloader())
}
