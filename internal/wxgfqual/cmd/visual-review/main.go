// visual-review is a qualification-only helper. It prepares offline comparison
// pages and evaluates private review records; it is not built or distributed
// with v-local-cli releases.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/wxgfqual"
)

const (
	maximumRequestBytes = 256 * 1024
	maximumRecordBytes  = 64 * 1024
	maximumImageBytes   = 64 * 1024 * 1024
)

var validatePrivateReviewRoot = state.ValidatePrivateDirectorySecurity

type helperRequest struct {
	Protocol               string   `json:"protocol"`
	Action                 string   `json:"action"`
	ReviewRoot             string   `json:"review_root,omitempty"`
	RecordRoot             string   `json:"record_root,omitempty"`
	RecordPaths            []string `json:"record_paths,omitempty"`
	ReportedDecoder        string   `json:"reported_decoder,omitempty"`
	ReportedDecoderVersion string   `json:"reported_decoder_version,omitempty"`
}

type preparedSample struct {
	Ordinal        int                        `json:"ordinal"`
	EvidenceID     string                     `json:"evidence_id"`
	QualityTier    string                     `json:"quality_tier"`
	WXGFSHA256     string                     `json:"wxgf_sha256"`
	BundleFileName string                     `json:"bundle_file_name"`
	Reference      wxgfqual.VisualReviewImage `json:"reference"`
	Decoded        wxgfqual.VisualReviewImage `json:"decoded"`
}

type helperResponse struct {
	Protocol string                        `json:"protocol"`
	Status   string                        `json:"status"`
	Capture  *wxgfqual.VisualReviewCapture `json:"capture,omitempty"`
	Samples  []preparedSample              `json:"samples,omitempty"`
	Matrix   *wxgfqual.VisualReviewMatrix  `json:"matrix,omitempty"`
}

func decodeStrict(reader io.Reader, maximum int64, target any) error {
	payload, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || len(payload) == 0 || int64(len(payload)) > maximum {
		return errors.New("invalid_json")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid_json")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("invalid_json")
	}
	return nil
}

func privateRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) ||
		(runtime.GOOS == "windows" && (strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//"))) {
		return "", errors.New("invalid_private_root")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("invalid_private_root")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || validatePrivateReviewRoot(absolute) != nil {
		return "", errors.New("invalid_private_root")
	}
	return absolute, nil
}

func childPath(root, path, expectedBase string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", errors.New("invalid_private_path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil || filepath.Base(absolute) != expectedBase {
		return "", errors.New("invalid_private_path")
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative != expectedBase || filepath.IsAbs(relative) {
		return "", errors.New("invalid_private_path")
	}
	return absolute, nil
}

func readRegular(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("invalid_private_file")
	}
	payload, err := os.ReadFile(path)
	if err != nil || int64(len(payload)) != info.Size() {
		return nil, errors.New("invalid_private_file")
	}
	return payload, nil
}

func writeExclusive(path string, payload []byte) (returnErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	succeeded := false
	defer func() {
		_ = file.Close()
		if !succeeded {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	succeeded = true
	return nil
}

func prepare(root string, createBundles bool) (helperResponse, error) {
	capturePath, err := childPath(root, filepath.Join(root, "capture.json"), "capture.json")
	if err != nil {
		return helperResponse{}, err
	}
	payload, err := readRegular(capturePath, maximumRecordBytes)
	if err != nil {
		return helperResponse{}, err
	}
	var capture wxgfqual.VisualReviewCapture
	if err := decodeStrict(bytes.NewReader(payload), maximumRecordBytes, &capture); err != nil || wxgfqual.ValidateVisualReviewCapture(capture) != nil {
		return helperResponse{}, errors.New("invalid_capture")
	}
	status := "inspected"
	if createBundles {
		status = "prepared"
	}
	response := helperResponse{Protocol: wxgfqual.VisualReviewHelperProtocol, Status: status, Capture: &capture}
	created := []string{}
	succeeded := false
	defer func() {
		if !succeeded {
			for _, path := range created {
				_ = os.Remove(path)
			}
		}
	}()
	for _, sample := range capture.Samples {
		decodedPath, err := childPath(root, filepath.Join(root, sample.DecodedFileName), sample.DecodedFileName)
		if err != nil {
			return helperResponse{}, err
		}
		decoded, err := readRegular(decodedPath, maximumImageBytes)
		if err != nil {
			return helperResponse{}, err
		}
		decodedReview, err := wxgfqual.InspectVisualReviewPNG(decoded)
		if err != nil || decodedReview.SHA256 != sample.DecodedSHA256 ||
			decodedReview.VisualFingerprint != sample.DecodedVisualFingerprint ||
			decodedReview.Width != sample.DecodedWidth || decodedReview.Height != sample.DecodedHeight {
			return helperResponse{}, errors.New("capture_binding_failed")
		}
		if !createBundles {
			response.Samples = append(response.Samples, preparedSample{
				Ordinal: sample.Ordinal, EvidenceID: sample.EvidenceID, QualityTier: sample.QualityTier,
				WXGFSHA256: sample.WXGFSHA256, Decoded: decodedReview,
			})
			continue
		}
		referenceName := fmt.Sprintf("reference-%02d.png", sample.Ordinal)
		referencePath, err := childPath(root, filepath.Join(root, referenceName), referenceName)
		if err != nil {
			return helperResponse{}, err
		}
		reference, err := readRegular(referencePath, maximumImageBytes)
		if err != nil {
			return helperResponse{}, err
		}
		bundle, err := wxgfqual.PrepareVisualReviewBundle(reference, decoded)
		if err != nil || bundle.Decoded != decodedReview {
			return helperResponse{}, errors.New("capture_binding_failed")
		}
		bundleName := fmt.Sprintf("review-%02d.html", sample.Ordinal)
		bundlePath, err := childPath(root, filepath.Join(root, bundleName), bundleName)
		if err != nil || writeExclusive(bundlePath, bundle.HTML) != nil {
			return helperResponse{}, errors.New("review_bundle_write_failed")
		}
		created = append(created, bundlePath)
		response.Samples = append(response.Samples, preparedSample{
			Ordinal: sample.Ordinal, EvidenceID: sample.EvidenceID, QualityTier: sample.QualityTier,
			WXGFSHA256: sample.WXGFSHA256, BundleFileName: bundleName,
			Reference: bundle.Reference, Decoded: bundle.Decoded,
		})
	}
	succeeded = true
	return response, nil
}

func evaluate(recordRoot string, paths []string, reportedDecoder, reportedDecoderVersion string) (helperResponse, error) {
	if len(paths) == 0 || len(paths) > 100 {
		return helperResponse{}, errors.New("invalid_record_count")
	}
	records := make([]wxgfqual.VisualReviewRecord, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) ||
			(runtime.GOOS == "windows" && (strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//"))) {
			return helperResponse{}, errors.New("invalid_record_path")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return helperResponse{}, errors.New("invalid_record_path")
		}
		relative, err := filepath.Rel(recordRoot, absolute)
		relativeDirectory := filepath.Dir(relative)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) ||
			filepath.Base(absolute) != "record.json" || relativeDirectory == "." || filepath.Dir(relativeDirectory) != "." ||
			seen[strings.ToLower(filepath.Clean(absolute))] || validatePrivateReviewRoot(filepath.Dir(absolute)) != nil {
			return helperResponse{}, errors.New("invalid_record_path")
		}
		seen[strings.ToLower(filepath.Clean(absolute))] = true
		payload, err := readRegular(absolute, maximumRecordBytes)
		if err != nil {
			return helperResponse{}, err
		}
		var record wxgfqual.VisualReviewRecord
		if err := decodeStrict(bytes.NewReader(payload), maximumRecordBytes, &record); err != nil {
			return helperResponse{}, errors.New("invalid_record")
		}
		records = append(records, record)
	}
	matrix, err := wxgfqual.EvaluateVisualReviewMatrix(records, reportedDecoder, reportedDecoderVersion)
	if err != nil {
		return helperResponse{}, err
	}
	return helperResponse{Protocol: wxgfqual.VisualReviewHelperProtocol, Status: "evaluated", Matrix: &matrix}, nil
}

func run(stdin io.Reader, stdout io.Writer) error {
	var request helperRequest
	if err := decodeStrict(stdin, maximumRequestBytes, &request); err != nil || request.Protocol != wxgfqual.VisualReviewHelperProtocol {
		return errors.New("invalid_request")
	}
	var response helperResponse
	var err error
	switch request.Action {
	case "prepare":
		if request.RecordRoot != "" || len(request.RecordPaths) != 0 ||
			request.ReportedDecoder != "" || request.ReportedDecoderVersion != "" {
			return errors.New("invalid_request")
		}
		root, rootErr := privateRoot(request.ReviewRoot)
		if rootErr != nil {
			return rootErr
		}
		response, err = prepare(root, true)
	case "inspect":
		if request.RecordRoot != "" || len(request.RecordPaths) != 0 ||
			request.ReportedDecoder != "" || request.ReportedDecoderVersion != "" {
			return errors.New("invalid_request")
		}
		root, rootErr := privateRoot(request.ReviewRoot)
		if rootErr != nil {
			return rootErr
		}
		response, err = prepare(root, false)
	case "evaluate_matrix":
		if request.ReviewRoot != "" {
			return errors.New("invalid_request")
		}
		root, rootErr := privateRoot(request.RecordRoot)
		if rootErr != nil {
			return rootErr
		}
		response, err = evaluate(root, request.RecordPaths, request.ReportedDecoder, request.ReportedDecoderVersion)
	default:
		return errors.New("invalid_action")
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(response)
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "visual_review_failed")
		os.Exit(1)
	}
}
