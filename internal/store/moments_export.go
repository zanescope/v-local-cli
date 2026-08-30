package store

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

const maxMomentRemoteTokenBytes = 4096

var dnsPodDOTAddresses = []string{"1.12.12.12:853", "120.53.53.53:853"}

var momentImageHost = regexp.MustCompile(`^(?:[0-9a-z-]*mmsns\.qpic\.cn|vweixinthumb[0-9a-z-]*\.tc\.qq\.com)$`)
var momentLegacyVideoHost = regexp.MustCompile(`^snsvideodownload[0-9a-z-]*\.tc\.qq\.com$`)
var momentTencentVideoHost = regexp.MustCompile(`^[0-9a-z](?:[0-9a-z-]{0,61}[0-9a-z])?\.video\.qq\.com$`)

var reservedRemotePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

type MomentMediaExportError struct {
	Kind string
}

func (err *MomentMediaExportError) Error() string {
	return err.Kind
}

type MomentMediaArtifact struct {
	EvidenceID             string `json:"evidence_id"`
	Kind                   string `json:"media_kind"`
	Format                 string `json:"format"`
	Bytes                  int    `json:"bytes"`
	ContentMD5             string `json:"content_md5"`
	ContainerValidation    string `json:"container_validation"`
	DecryptionScope        string `json:"decryption_scope"`
	DescriptorMD5Status    string `json:"descriptor_md5_status"`
	DescriptorSizeStatus   string `json:"descriptor_size_status"`
	Source                 string `json:"source"`
	ResolutionStatus       string `json:"resolution_status"`
	VerifiedBy             string `json:"verified_by"`
	NetworkAccessPerformed bool   `json:"network_access_performed"`
	Data                   []byte `json:"-"`
	Path                   string `json:"-"`
	RemoveAfterRead        bool   `json:"-"`
}

type MomentMediaExportOptions struct {
	MomentMediaOptions
	AllowNetwork       bool
	TemporaryDirectory string
}

type momentMediaLookup struct {
	Moment Moment
	Media  MomentMedia
}

type momentRemoteResponse struct {
	Payload     []byte
	Path        string
	Bytes       int64
	Encrypted   bool
	ContentType string
}

type momentRemoteDownloader interface {
	Download(context.Context, *url.URL, int64) (momentRemoteResponse, error)
}

type momentDownloadError struct {
	Kind string
}

func (err *momentDownloadError) Error() string {
	return err.Kind
}

func mediaByEvidenceID(item *Moment, evidenceID string) *MomentMedia {
	for index := range item.Media {
		if item.Media[index].EvidenceID == evidenceID {
			return &item.Media[index]
		}
	}
	for interactionIndex := range item.Interactions.Comments {
		interaction := &item.Interactions.Comments[interactionIndex]
		for mediaIndex := range interaction.Media {
			if interaction.Media[mediaIndex].EvidenceID == evidenceID {
				return &interaction.Media[mediaIndex]
			}
		}
	}
	return nil
}

func remoteDescriptorFingerprint(media MomentMedia) string {
	original := media.remote.Original
	thumbnail := media.remote.Thumbnail
	digest := sha256.Sum256([]byte(strings.Join([]string{
		media.EvidenceID, media.Kind,
		original.URL, original.Token, original.Key, original.EncryptionIdx, original.ExpectedMD5, strconv.FormatInt(original.ExpectedBytes, 10),
		thumbnail.URL, thumbnail.Token, thumbnail.Key, thumbnail.EncryptionIdx, thumbnail.ExpectedMD5, strconv.FormatInt(thumbnail.ExpectedBytes, 10),
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func findMomentMedia(root, evidenceID string) (momentMediaLookup, error) {
	evidenceID = strings.TrimSpace(evidenceID)
	if evidenceID == "" || len(evidenceID) > 1000 {
		return momentMediaLookup{}, &MomentMediaExportError{Kind: "moment_media_not_found"}
	}
	files, err := sqliteFiles(root)
	if err != nil {
		return momentMediaLookup{}, err
	}
	var selected *momentMediaLookup
	fingerprint := ""
	for _, path := range files {
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, "SnsTimeLine")
		if table == "" {
			_ = database.Close()
			continue
		}
		available := columns(database, table)
		tidColumn := columnCI(available, "tid")
		usernameColumn := columnCI(available, "user_name")
		contentColumn := columnCI(available, "content")
		if tidColumn == "" || usernameColumn == "" || contentColumn == "" {
			_ = database.Close()
			continue
		}
		rows, queryErr := database.Query("SELECT " + quoteIdentifier(tidColumn) + ", " + quoteIdentifier(usernameColumn) + ", " + quoteIdentifier(contentColumn) + " FROM " + quoteIdentifier(table))
		if queryErr != nil {
			_ = database.Close()
			continue
		}
		for rows.Next() {
			var tid, username, content any
			if rows.Scan(&tid, &username, &content) != nil {
				continue
			}
			item := parseMoment(tid, username, content, "", "")
			media := mediaByEvidenceID(&item, evidenceID)
			if media == nil {
				continue
			}
			if item.ParseStatus == "identity_conflict" {
				_ = rows.Close()
				_ = database.Close()
				return momentMediaLookup{}, &MomentMediaExportError{Kind: "moment_media_identity_conflict"}
			}
			currentFingerprint := remoteDescriptorFingerprint(*media)
			if selected != nil && currentFingerprint != fingerprint {
				_ = rows.Close()
				_ = database.Close()
				return momentMediaLookup{}, &MomentMediaExportError{Kind: "moment_media_ambiguous"}
			}
			copy := momentMediaLookup{Moment: item, Media: *media}
			selected = &copy
			fingerprint = currentFingerprint
		}
		if rowErr := rows.Err(); rowErr != nil {
			_ = rows.Close()
			_ = database.Close()
			return momentMediaLookup{}, rowErr
		}
		_ = rows.Close()
		_ = database.Close()
	}
	if selected == nil {
		return momentMediaLookup{}, &MomentMediaExportError{Kind: "moment_media_not_found"}
	}
	return *selected, nil
}

func momentMediaByteLimit(kind string) int64 {
	if kind == "video" {
		return maxMomentVideoBytes
	}
	return maxMomentImageBytes
}

func readLocalMomentMedia(media MomentMedia, options MomentMediaOptions) (MomentMediaArtifact, error) {
	if media.Local == nil || media.ResolutionStatus != "verified_local" {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_local_unavailable"}
	}
	limit := momentMediaByteLimit(media.Kind)
	info, err := os.Stat(media.Local.Path)
	if err != nil || info.IsDir() || info.Size() <= 0 || info.Size() > limit {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_local_unavailable"}
	}
	format := "mp4"
	containerValidation := ""
	if media.Kind == "video" {
		if media.Local.Cipher != "plain" {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_local_unavailable"}
		}
		file, err := os.Open(media.Local.Path)
		if err != nil {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_local_unavailable"}
		}
		validation, validationErr := cryptoutil.ValidateMP4Reader(file, info.Size())
		_, _ = file.Seek(0, io.SeekStart)
		hash := md5.New()
		_, hashErr := io.Copy(hash, io.LimitReader(file, limit+1))
		_ = file.Close()
		if validationErr != nil {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed"}
		}
		if hashErr != nil {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_local_unavailable"}
		}
		contentMD5 := hex.EncodeToString(hash.Sum(nil))
		if media.Local.SourceMD5 != "" && !strings.EqualFold(media.Local.SourceMD5, contentMD5) {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed"}
		}
		if media.Local.ContentMD5 != "" && !strings.EqualFold(media.Local.ContentMD5, contentMD5) {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed"}
		}
		containerValidation = validation.Method
		return MomentMediaArtifact{
			EvidenceID: media.EvidenceID, Kind: media.Kind, Format: format, Bytes: int(info.Size()), ContentMD5: contentMD5,
			ContainerValidation: containerValidation, DecryptionScope: "local_cache",
			DescriptorMD5Status: "not_applicable", DescriptorSizeStatus: "not_applicable",
			Source: "local_cache", ResolutionStatus: "verified_local", VerifiedBy: media.Local.VerifiedBy,
			NetworkAccessPerformed: false, Path: media.Local.Path,
		}, nil
	} else {
		file, err := os.Open(media.Local.Path)
		if err != nil {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_local_unavailable"}
		}
		payload, err := io.ReadAll(io.LimitReader(file, limit+1))
		_ = file.Close()
		if err != nil || len(payload) == 0 || int64(len(payload)) > limit {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_local_unavailable"}
		}
		if media.Local.SourceMD5 != "" && !strings.EqualFold(media.Local.SourceMD5, md5Hex(payload)) {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed"}
		}
		format = cryptoutil.ImageFormat(payload)
		if media.Local.Cipher == "dat" {
			payload, format, err = cryptoutil.DecryptImageDAT(payload, options.AESKey, options.XORKey)
			if err != nil {
				return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_local_unavailable"}
			}
		}
		if format == "unknown" {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed"}
		}
		validation, validationErr := cryptoutil.ValidateImageStructure(payload)
		if validationErr != nil {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed"}
		}
		containerValidation = validation.Method
		contentMD5 := md5Hex(payload)
		if media.Local.ContentMD5 != "" && !strings.EqualFold(media.Local.ContentMD5, contentMD5) {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed"}
		}
		return MomentMediaArtifact{
			EvidenceID: media.EvidenceID, Kind: media.Kind, Format: format, Bytes: len(payload), ContentMD5: contentMD5,
			ContainerValidation: containerValidation, DecryptionScope: "local_cache",
			DescriptorMD5Status: "not_applicable", DescriptorSizeStatus: "not_applicable",
			Source: "local_cache", ResolutionStatus: "verified_local", VerifiedBy: media.Local.VerifiedBy,
			NetworkAccessPerformed: false, Data: payload,
		}, nil
	}
}

func validRemoteToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxMomentRemoteTokenBytes {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f {
			return false
		}
	}
	return true
}

func buildMomentMediaURL(variant momentRemoteVariant, kind string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(variant.URL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, &MomentMediaExportError{Kind: "moment_media_remote_url_rejected"}
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, &MomentMediaExportError{Kind: "moment_media_remote_url_rejected"}
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	hostAllowed := kind == "image" && momentImageHost.MatchString(host) || kind == "video" && momentVideoLocationAllowed(host, parsed.Path)
	if parsed.Port() != "" || !hostAllowed {
		return nil, &MomentMediaExportError{Kind: "moment_media_remote_url_rejected"}
	}
	if !validRemoteToken(variant.Token) {
		return nil, &MomentMediaExportError{Kind: "moment_media_remote_descriptor_missing"}
	}
	parsed.Scheme = "https"
	parsed.Host = host
	if kind == "video" {
		existing := parsed.Query()
		existing.Del("token")
		existing.Del("idx")
		parsed.RawQuery = "token=" + url.QueryEscape(strings.TrimSpace(variant.Token)) + "&idx=1"
		if encoded := existing.Encode(); encoded != "" {
			parsed.RawQuery += "&" + encoded
		}
	} else {
		query := url.Values{}
		query.Set("token", strings.TrimSpace(variant.Token))
		query.Set("idx", "1")
		parsed.RawQuery = query.Encode()
	}
	return parsed, nil
}

// momentVideoLocationAllowed 同时兼容旧下载域名和微信当前使用的 wxsns 视频域名。
// 新域名除腾讯主域边界外，还必须同时满足 wxsns 标签和固定下载端点，避免把令牌发往普通腾讯视频服务。
func momentVideoLocationAllowed(host, requestPath string) bool {
	if momentLegacyVideoHost.MatchString(host) {
		return true
	}
	if !momentTencentVideoHost.MatchString(host) {
		return false
	}
	label := strings.TrimSuffix(host, ".video.qq.com")
	if !strings.Contains(label, "wxsns") {
		return false
	}
	trimmedPath := strings.TrimRight(requestPath, "/")
	separator := strings.LastIndex(trimmedPath, "/")
	return separator >= 0 && strings.EqualFold(trimmedPath[separator+1:], "snsvideodownload")
}

func publicRemoteIP(address net.IP) bool {
	if address == nil || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	parsed, ok := netip.AddrFromSlice(address)
	if !ok {
		return false
	}
	parsed = parsed.Unmap()
	for _, prefix := range reservedRemotePrefixes {
		if prefix.Contains(parsed) {
			return false
		}
	}
	return true
}

func rejectedRemoteIPKind(address net.IP) string {
	parsed, ok := netip.AddrFromSlice(address)
	if !ok {
		return "non_public_address"
	}
	parsed = parsed.Unmap()
	if netip.MustParsePrefix("198.18.0.0/15").Contains(parsed) {
		return "synthetic_proxy_address"
	}
	return "non_public_address"
}

type momentSafeDialer struct {
	resolver         *net.Resolver
	fallbackResolver *net.Resolver
	dialer           net.Dialer
}

func syntheticProxyAddressesOnly(addresses []net.IPAddr) bool {
	if len(addresses) == 0 {
		return false
	}
	for _, address := range addresses {
		if rejectedRemoteIPKind(address.IP) != "synthetic_proxy_address" {
			return false
		}
	}
	return true
}

func directDNSPodResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo:     true,
		StrictErrors: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			tlsDialer := tls.Dialer{
				NetDialer: &net.Dialer{Timeout: 5 * time.Second},
				Config:    &tls.Config{ServerName: "dot.pub", MinVersion: tls.VersionTLS12},
			}
			for _, address := range dnsPodDOTAddresses {
				connection, err := tlsDialer.DialContext(ctx, "tcp", address)
				if err == nil {
					return connection, nil
				}
			}
			return nil, &momentDownloadError{Kind: "direct_dns_transport_failed"}
		},
	}
}

func (dialer *momentSafeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, &momentDownloadError{Kind: "invalid_address"}
	}
	addresses, err := dialer.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, &momentDownloadError{Kind: "dns_failed"}
	}
	if syntheticProxyAddressesOnly(addresses) && dialer.fallbackResolver != nil {
		fallbackAddresses, fallbackErr := dialer.fallbackResolver.LookupIPAddr(ctx, host)
		if fallbackErr != nil || len(fallbackAddresses) == 0 {
			return nil, &momentDownloadError{Kind: "direct_dns_failed"}
		}
		addresses = fallbackAddresses
	}
	publicAddressFound := false
	rejectedAddressKind := ""
	for _, candidate := range addresses {
		if !publicRemoteIP(candidate.IP) {
			kind := rejectedRemoteIPKind(candidate.IP)
			if rejectedAddressKind == "" {
				rejectedAddressKind = kind
			} else if rejectedAddressKind != kind {
				rejectedAddressKind = "non_public_address"
			}
			continue
		}
		publicAddressFound = true
		connection, dialErr := dialer.dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
	}
	if !publicAddressFound {
		if rejectedAddressKind == "" {
			rejectedAddressKind = "non_public_address"
		}
		return nil, &momentDownloadError{Kind: rejectedAddressKind}
	}
	return nil, &momentDownloadError{Kind: "connection_failed"}
}

type safeMomentDownloader struct {
	client             *http.Client
	temporaryDirectory string
}

func newSafeMomentDownloader(temporaryDirectory string) *safeMomentDownloader {
	dialer := &momentSafeDialer{
		resolver:         net.DefaultResolver,
		fallbackResolver: directDNSPodResolver(),
		dialer:           net.Dialer{Timeout: 10 * time.Second, KeepAlive: 20 * time.Second},
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
	}
	return &safeMomentDownloader{temporaryDirectory: temporaryDirectory, client: &http.Client{
		Transport: transport,
		Timeout:   5 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (downloader *safeMomentDownloader) Download(ctx context.Context, target *url.URL, maxBytes int64) (momentRemoteResponse, error) {
	if maxBytes <= 0 || maxBytes > maxMomentVideoBytes {
		return momentRemoteResponse{}, &momentDownloadError{Kind: "response_size_invalid"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return momentRemoteResponse{}, &momentDownloadError{Kind: "request_build_failed"}
	}
	request.Header.Set("User-Agent", "MicroMessenger Client")
	request.Header.Set("Accept", "*/*")
	response, err := downloader.client.Do(request)
	if err != nil {
		var downloadErr *momentDownloadError
		if errors.As(err, &downloadErr) {
			return momentRemoteResponse{}, downloadErr
		}
		return momentRemoteResponse{}, &momentDownloadError{Kind: "request_failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		kind := "http_status"
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			kind = "authorization_rejected"
		case http.StatusNotFound, http.StatusGone:
			kind = "resource_unavailable"
		case http.StatusTooManyRequests:
			kind = "rate_limited"
		default:
			if response.StatusCode >= 300 && response.StatusCode < 400 {
				kind = "redirect_rejected"
			}
		}
		return momentRemoteResponse{}, &momentDownloadError{Kind: kind}
	}
	if response.ContentLength > maxBytes {
		return momentRemoteResponse{}, &momentDownloadError{Kind: "response_size_invalid"}
	}
	if maxBytes == maxMomentVideoBytes {
		file, err := os.CreateTemp(downloader.temporaryDirectory, "v-local-cli-moment-video-*.tmp")
		if err != nil {
			return momentRemoteResponse{}, &momentDownloadError{Kind: "response_read_failed"}
		}
		path := file.Name()
		remove := true
		defer func() {
			_ = file.Close()
			if remove {
				_ = os.Remove(path)
			}
		}()
		_ = file.Chmod(0o600)
		written, copyErr := io.Copy(file, io.LimitReader(response.Body, maxBytes+1))
		syncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return momentRemoteResponse{}, &momentDownloadError{Kind: "response_read_failed"}
		}
		if written <= 0 || written > maxBytes {
			return momentRemoteResponse{}, &momentDownloadError{Kind: "response_size_invalid"}
		}
		remove = false
		return momentRemoteResponse{Path: path, Bytes: written, Encrypted: strings.TrimSpace(response.Header.Get("X-Enc")) == "1", ContentType: response.Header.Get("Content-Type")}, nil
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return momentRemoteResponse{}, &momentDownloadError{Kind: "response_read_failed"}
	}
	if len(payload) == 0 || int64(len(payload)) > maxBytes {
		return momentRemoteResponse{}, &momentDownloadError{Kind: "response_size_invalid"}
	}
	return momentRemoteResponse{Payload: payload, Bytes: int64(len(payload)), Encrypted: strings.TrimSpace(response.Header.Get("X-Enc")) == "1", ContentType: response.Header.Get("Content-Type")}, nil
}

func chooseRemoteMediaVariant(media MomentMedia) (momentRemoteVariant, error) {
	if media.Kind == "video" {
		variant := media.remote.Original
		if variant.URL != "" && variant.Token != "" && variant.Key != "" {
			return variant, nil
		}
		return momentRemoteVariant{}, &MomentMediaExportError{Kind: "moment_media_remote_descriptor_missing"}
	}
	variants := []momentRemoteVariant{media.remote.Original, media.remote.Thumbnail}
	for _, variant := range variants {
		if variant.URL != "" && variant.Token != "" && variant.Key != "" {
			return variant, nil
		}
	}
	for _, variant := range variants {
		if variant.URL == "" || variant.Token == "" {
			continue
		}
		if variant.Key == "" {
			variant.Key = media.remote.Original.Key
		}
		return variant, nil
	}
	return momentRemoteVariant{}, &MomentMediaExportError{Kind: "moment_media_remote_descriptor_missing"}
}

type remoteDescriptorEvidence struct {
	contentMD5          string
	verifiedBy          string
	descriptorMD5Status string
	descriptorSizeState string
}

func verifyRemoteDescriptorDigests(rawMD5, contentMD5 string, payloadBytes int64, variant momentRemoteVariant) remoteDescriptorEvidence {
	result := remoteDescriptorEvidence{
		contentMD5: contentMD5, verifiedBy: "trusted_cdn_tls_token_and_plaintext_container",
		descriptorMD5Status: "not_provided", descriptorSizeState: "not_provided",
	}
	expectedMD5 := strings.ToLower(strings.TrimSpace(variant.ExpectedMD5))
	if len(expectedMD5) == 32 {
		switch expectedMD5 {
		case contentMD5:
			result.verifiedBy = "plaintext_md5"
			result.descriptorMD5Status = "plaintext_match"
		case rawMD5:
			result.verifiedBy = "ciphertext_md5_and_plaintext_container"
			result.descriptorMD5Status = "ciphertext_match"
		default:
			result.descriptorMD5Status = "not_content_digest"
		}
	}
	if variant.ExpectedBytes > 0 {
		result.descriptorSizeState = "mismatch"
		if payloadBytes == variant.ExpectedBytes {
			result.descriptorSizeState = "match"
		}
	}
	return result
}

func verifyRemoteDescriptor(rawPayload, payload []byte, variant momentRemoteVariant) remoteDescriptorEvidence {
	rawDigest := md5.Sum(rawPayload)
	digest := md5.Sum(payload)
	return verifyRemoteDescriptorDigests(
		hex.EncodeToString(rawDigest[:]), hex.EncodeToString(digest[:]), int64(len(payload)), variant,
	)
}

func md5File(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := md5.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxMomentVideoBytes+1))
	if err != nil {
		return "", 0, err
	}
	if written <= 0 || written > maxMomentVideoBytes {
		return "", written, errors.New("视频大小无效")
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func decodeRemoteMomentImage(media MomentMedia, variant momentRemoteVariant, response momentRemoteResponse) (MomentMediaArtifact, error) {
	rawPayload := response.Payload
	if len(rawPayload) == 0 || len(rawPayload) > maxMomentImageBytes {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
	}
	payload := rawPayload
	validation, validationErr := cryptoutil.ValidateImageStructure(payload)
	decryptionScope := "not_required"
	if response.Encrypted || cryptoutil.ImageFormat(rawPayload) == "unknown" {
		seed, err := strconv.ParseUint(strings.TrimSpace(variant.Key), 10, 64)
		if err != nil {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_remote_descriptor_missing"}
		}
		payload = append([]byte(nil), rawPayload...)
		stream := isaac64Keystream(seed, len(payload))
		for index := range payload {
			payload[index] ^= stream[index]
		}
		validation, validationErr = cryptoutil.ValidateImageStructure(payload)
		decryptionScope = "full_payload"
	}
	if validationErr != nil {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_container"}
	}
	evidence := verifyRemoteDescriptor(rawPayload, payload, variant)
	return MomentMediaArtifact{
		EvidenceID: media.EvidenceID, Kind: media.Kind, Format: validation.Format, Bytes: len(payload), ContentMD5: evidence.contentMD5,
		ContainerValidation: validation.Method,
		DecryptionScope:     decryptionScope,
		DescriptorMD5Status: evidence.descriptorMD5Status, DescriptorSizeStatus: evidence.descriptorSizeState,
		Source: "wechat_cdn", ResolutionStatus: "verified_remote_download", VerifiedBy: evidence.verifiedBy,
		NetworkAccessPerformed: true, Data: payload,
	}, nil
}

func decodeRemoteMomentVideo(media MomentMedia, variant momentRemoteVariant, response momentRemoteResponse, temporaryDirectory string) (MomentMediaArtifact, error) {
	path := response.Path
	if path == "" {
		if len(response.Payload) == 0 || len(response.Payload) > maxMomentVideoBytes {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
		}
		file, err := os.CreateTemp(temporaryDirectory, "v-local-cli-moment-video-*.tmp")
		if err != nil {
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
		}
		path = file.Name()
		_ = file.Chmod(0o600)
		written, writeErr := file.Write(response.Payload)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil || written != len(response.Payload) {
			_ = os.Remove(path)
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
		}
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	rawMD5, totalBytes, err := md5File(path)
	if err != nil || totalBytes <= 0 || totalBytes > maxMomentVideoBytes {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
	}
	validation, validationErr := cryptoutil.ValidateMP4Reader(file, totalBytes)
	decryptionScope := "not_required"
	if response.Encrypted || validationErr != nil {
		seed, err := strconv.ParseUint(strings.TrimSpace(variant.Key), 10, 64)
		if err != nil {
			_ = file.Close()
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_remote_descriptor_missing"}
		}
		decryptBytes := totalBytes
		if decryptBytes > 128*1024 {
			decryptBytes = 128 * 1024
		}
		prefix := make([]byte, int(decryptBytes))
		if _, err := io.ReadFull(io.NewSectionReader(file, 0, decryptBytes), prefix); err != nil {
			_ = file.Close()
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
		}
		stream := isaac64Keystream(seed, len(prefix))
		for index := range prefix {
			prefix[index] ^= stream[index]
		}
		if _, err := file.WriteAt(prefix, 0); err != nil {
			_ = file.Close()
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
		}
		validation, validationErr = cryptoutil.ValidateMP4Reader(file, totalBytes)
		decryptionScope = "prefix_131072"
	}
	_ = file.Close()
	if validationErr != nil {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_container"}
	}
	contentMD5, plainBytes, err := md5File(path)
	if err != nil || plainBytes != totalBytes {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_verify_failed_payload_size"}
	}
	evidence := verifyRemoteDescriptorDigests(rawMD5, contentMD5, totalBytes, variant)
	keep = true
	return MomentMediaArtifact{
		EvidenceID: media.EvidenceID, Kind: media.Kind, Format: "mp4", Bytes: int(totalBytes), ContentMD5: evidence.contentMD5,
		ContainerValidation: validation.Method,
		DecryptionScope:     decryptionScope,
		DescriptorMD5Status: evidence.descriptorMD5Status, DescriptorSizeStatus: evidence.descriptorSizeState,
		Source: "wechat_cdn", ResolutionStatus: "verified_remote_download", VerifiedBy: evidence.verifiedBy,
		NetworkAccessPerformed: true, Path: path, RemoveAfterRead: true,
	}, nil
}

func exportMomentMediaWithDownloader(ctx context.Context, root, evidenceID string, options MomentMediaExportOptions, downloader momentRemoteDownloader) (MomentMediaArtifact, error) {
	lookup, err := findMomentMedia(root, evidenceID)
	if err != nil {
		return MomentMediaArtifact{}, err
	}
	if lookup.Media.Kind != "image" && lookup.Media.Kind != "video" {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_kind_unsupported"}
	}
	items := []Moment{lookup.Moment}
	ResolveMomentMedia(items, options.MomentMediaOptions)
	media := mediaByEvidenceID(&items[0], evidenceID)
	if media == nil {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_not_found"}
	}
	if media.ResolutionStatus == "verified_local" {
		return readLocalMomentMedia(*media, options.MomentMediaOptions)
	}
	variant, err := chooseRemoteMediaVariant(*media)
	if err != nil {
		return MomentMediaArtifact{}, err
	}
	target, err := buildMomentMediaURL(variant, media.Kind)
	if err != nil {
		return MomentMediaArtifact{}, err
	}
	if !options.AllowNetwork {
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: "moment_media_network_authorization_required"}
	}
	if downloader == nil {
		downloader = newSafeMomentDownloader(options.TemporaryDirectory)
	}
	response, err := downloader.Download(ctx, target, momentMediaByteLimit(media.Kind))
	if err != nil {
		kind := "moment_media_download_failed"
		var downloadErr *momentDownloadError
		if errors.As(err, &downloadErr) && downloadErr.Kind != "" {
			kind += "_" + downloadErr.Kind
		}
		return MomentMediaArtifact{}, &MomentMediaExportError{Kind: kind}
	}
	if media.Kind == "video" {
		return decodeRemoteMomentVideo(*media, variant, response, options.TemporaryDirectory)
	}
	return decodeRemoteMomentImage(*media, variant, response)
}

// ExportMomentMedia 先尝试经过验真的本地缓存；只有显式授权后才会访问受限的媒体 CDN。
func ExportMomentMedia(ctx context.Context, root, evidenceID string, options MomentMediaExportOptions) (MomentMediaArtifact, error) {
	return exportMomentMediaWithDownloader(ctx, root, evidenceID, options, nil)
}
