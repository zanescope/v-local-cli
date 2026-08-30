package store

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

const syntheticChatImageProtocolProfile = "synthetic_loopback_crypto_binding_harness_aes_128_ecb_pkcs7"
const maxChatImageRemoteResponseBytes = maxChatImageBytes + aes.BlockSize

// chatImageQualificationProtocol is deliberately not exported or wired to the CLI. This is a
// crypto/binding harness, not a model of the authenticated desktop CDN request envelope.
type chatImageQualificationProtocol struct {
	name             string
	endpoint         *url.URL
	expectedHost     string
	syntheticFixture bool
}

type chatImageQualificationArtifact struct {
	protocolProfile            string
	protocolStatus             string
	networkScope               string
	tier                       string
	format                     string
	bytes                      int
	width                      int
	height                     int
	sha256                     string
	contentMD5                 string
	containerValidation        string
	descriptorBytesStatus      string
	descriptorDimensionsStatus string
	descriptorMD5Status        string
	networkAccessPerformed     bool
	data                       []byte
}

type chatImageQualificationError struct {
	kind         string
	expiryStatus string
}

func (err *chatImageQualificationError) Error() string { return err.kind }

func newChatImageQualificationError(kind string) *chatImageQualificationError {
	expiryStatus := "not_evaluated"
	switch kind {
	case "chat_image_qualification_download_failed_authorization_rejected", "chat_image_qualification_download_failed_resource_unavailable":
		expiryStatus = "unknown_unavailable_at_request_time"
	case "chat_image_qualification_download_failed_http_status":
		expiryStatus = "unknown_unavailable_at_request_time"
	case "chat_image_qualification_download_failed_rate_limited":
		expiryStatus = "not_evidence_of_expiry"
	case "chat_image_qualification_download_failed_request_failed", "chat_image_qualification_download_failed_dns_failed",
		"chat_image_qualification_download_failed_direct_dns_failed", "chat_image_qualification_download_failed_direct_dns_transport_failed",
		"chat_image_qualification_download_failed_connection_failed", "chat_image_qualification_download_failed_response_read_failed",
		"chat_image_qualification_download_failed_redirect_rejected", "chat_image_qualification_download_failed_non_public_address",
		"chat_image_qualification_download_failed_synthetic_proxy_address", "chat_image_qualification_download_failed_invalid_address":
		expiryStatus = "unknown_after_request_failure"
	case "chat_image_qualification_decrypt_failed", "chat_image_qualification_container_invalid",
		"chat_image_qualification_descriptor_size_mismatch", "chat_image_qualification_descriptor_dimensions_mismatch",
		"chat_image_qualification_descriptor_md5_mismatch", "chat_image_qualification_download_failed_response_size_invalid":
		expiryStatus = "response_unverified"
	}
	return &chatImageQualificationError{kind: kind, expiryStatus: expiryStatus}
}

func buildSyntheticChatImageQualificationURL(protocol chatImageQualificationProtocol, candidate *chatImageRemoteCandidate) (*url.URL, error) {
	if protocol.name != syntheticChatImageProtocolProfile || !protocol.syntheticFixture {
		return nil, newChatImageQualificationError("chat_image_qualification_real_endpoint_not_enabled")
	}
	if protocol.endpoint == nil || !strings.EqualFold(protocol.endpoint.Scheme, "https") || protocol.endpoint.Hostname() == "" ||
		protocol.endpoint.User != nil || protocol.endpoint.Fragment != "" || protocol.endpoint.RawQuery != "" || protocol.endpoint.RawPath != "" ||
		protocol.endpoint.Opaque != "" || protocol.endpoint.ForceQuery || strings.TrimSpace(protocol.expectedHost) == "" ||
		protocol.endpoint.Path != "/download" || !strings.EqualFold(strings.TrimSuffix(protocol.endpoint.Hostname(), "."), strings.TrimSuffix(protocol.expectedHost, ".")) {
		return nil, newChatImageQualificationError("chat_image_qualification_endpoint_rejected")
	}
	endpointIP := net.ParseIP(protocol.endpoint.Hostname())
	if endpointIP == nil || !endpointIP.IsLoopback() {
		return nil, newChatImageQualificationError("chat_image_qualification_real_endpoint_not_enabled")
	}
	if candidate == nil {
		return nil, newChatImageQualificationError("chat_image_qualification_descriptor_invalid")
	}
	parameter, encoding, valid := parseChatImageRemoteParameter(candidate.encryptedQueryParameter)
	if !valid || encoding != "opaque_hex" || candidate.parameterEncoding != "opaque_hex" {
		return nil, newChatImageQualificationError("chat_image_qualification_descriptor_invalid")
	}
	target := *protocol.endpoint
	query := url.Values{}
	query.Set("encrypted_query_param", parameter)
	target.RawQuery = query.Encode()
	return &target, nil
}

func newSyntheticChatImageQualificationDownloader(source *http.Client) (*safeMomentDownloader, error) {
	if source == nil || source.Transport == nil {
		return nil, newChatImageQualificationError("chat_image_qualification_transport_required")
	}
	sourceTransport, ok := source.Transport.(*http.Transport)
	if !ok {
		return nil, newChatImageQualificationError("chat_image_qualification_transport_rejected")
	}
	if sourceTransport.Proxy != nil {
		return nil, newChatImageQualificationError("chat_image_qualification_proxy_rejected")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if sourceTransport.TLSClientConfig != nil {
		if sourceTransport.TLSClientConfig.InsecureSkipVerify || len(sourceTransport.TLSClientConfig.Certificates) != 0 ||
			sourceTransport.TLSClientConfig.GetClientCertificate != nil {
			return nil, newChatImageQualificationError("chat_image_qualification_tls_config_rejected")
		}
		// Only the test CA trust and an optional verification name cross this
		// boundary. Do not inherit callbacks, key log writers, client identity,
		// session caches, or other caller-owned TLS behavior.
		if sourceTransport.TLSClientConfig.RootCAs != nil {
			tlsConfig.RootCAs = sourceTransport.TLSClientConfig.RootCAs.Clone()
		}
		tlsConfig.ServerName = sourceTransport.TLSClientConfig.ServerName
	}
	timeout := source.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		TLSClientConfig:        tlsConfig,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 * 1024,
		DisableKeepAlives:      true,
		DisableCompression:     true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		Jar:       nil,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &safeMomentDownloader{client: client}, nil
}

func decryptChatImageAES128ECBPKCS7(ciphertext []byte, key [16]byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(ciphertext) > maxChatImageRemoteResponseBytes || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("ciphertext_size_invalid")
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	for offset := 0; offset < len(ciphertext); offset += aes.BlockSize {
		block.Decrypt(plain[offset:offset+aes.BlockSize], ciphertext[offset:offset+aes.BlockSize])
	}
	padding := int(plain[len(plain)-1])
	if padding <= 0 || padding > aes.BlockSize || padding > len(plain) {
		clear(plain)
		return nil, errors.New("pkcs7_padding_invalid")
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			clear(plain)
			return nil, errors.New("pkcs7_padding_invalid")
		}
	}
	plainBytes := len(plain) - padding
	clear(plain[plainBytes:])
	return plain[:plainBytes:plainBytes], nil
}

func qualifySyntheticChatImageRemoteCandidate(
	ctx context.Context,
	protocol chatImageQualificationProtocol,
	candidate *chatImageRemoteCandidate,
	downloader momentRemoteDownloader,
) (chatImageQualificationArtifact, error) {
	if downloader == nil {
		return chatImageQualificationArtifact{}, newChatImageQualificationError("chat_image_qualification_transport_required")
	}
	if !chatImageRemoteCandidateHasBindingMetadata(candidate) {
		return chatImageQualificationArtifact{}, newChatImageQualificationError("chat_image_qualification_descriptor_binding_insufficient")
	}
	target, err := buildSyntheticChatImageQualificationURL(protocol, candidate)
	if err != nil {
		return chatImageQualificationArtifact{}, err
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
		return chatImageQualificationArtifact{}, newChatImageQualificationError("chat_image_qualification_download_failed_" + kind)
	}
	if response.Path != "" || len(response.Payload) == 0 || response.Bytes != int64(len(response.Payload)) {
		return chatImageQualificationArtifact{}, newChatImageQualificationError("chat_image_qualification_download_failed_response_read_failed")
	}
	plain, err := decryptChatImageAES128ECBPKCS7(response.Payload, candidate.aesKey)
	if err != nil {
		return chatImageQualificationArtifact{}, newChatImageQualificationError("chat_image_qualification_decrypt_failed")
	}
	verified := false
	defer func() {
		if !verified {
			clear(plain)
		}
	}()
	validation, err := cryptoutil.ValidateImageStructure(plain)
	if err != nil {
		return chatImageQualificationArtifact{}, newChatImageQualificationError("chat_image_qualification_container_invalid")
	}
	bytesStatus := "not_provided"
	if candidate.expectedBytes > 0 {
		if candidate.expectedBytes != int64(len(plain)) {
			return chatImageQualificationArtifact{}, newChatImageQualificationError("chat_image_qualification_descriptor_size_mismatch")
		}
		bytesStatus = "match"
	}
	dimensionsStatus := "not_provided"
	if candidate.expectedWidth > 0 || candidate.expectedHeight > 0 {
		if candidate.expectedWidth != validation.Width || candidate.expectedHeight != validation.Height {
			return chatImageQualificationArtifact{}, newChatImageQualificationError("chat_image_qualification_descriptor_dimensions_mismatch")
		}
		dimensionsStatus = "match_observation_not_quality_gate"
	}
	digest := md5.Sum(plain)
	contentMD5 := hex.EncodeToString(digest[:])
	md5Status := "not_provided"
	if candidate.expectedMD5 != "" {
		if !strings.EqualFold(candidate.expectedMD5, contentMD5) {
			return chatImageQualificationArtifact{}, newChatImageQualificationError("chat_image_qualification_descriptor_md5_mismatch")
		}
		md5Status = "match"
	}
	sha := sha256.Sum256(plain)
	verified = true
	return chatImageQualificationArtifact{
		protocolProfile: syntheticChatImageProtocolProfile, protocolStatus: "synthetic_crypto_binding_harness_only",
		networkScope: "literal_loopback_fixture_only", tier: candidate.tier, format: validation.Format,
		bytes: len(plain), width: validation.Width, height: validation.Height,
		sha256: hex.EncodeToString(sha[:]), contentMD5: contentMD5, containerValidation: validation.Method,
		descriptorBytesStatus: bytesStatus, descriptorDimensionsStatus: dimensionsStatus, descriptorMD5Status: md5Status,
		networkAccessPerformed: true, data: plain,
	}, nil
}
