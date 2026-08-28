package store

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

const maxMomentMediaFiles = 200000
const maxMomentImageBytes = 64 * 1024 * 1024
const maxMomentVideoBytes = 512 * 1024 * 1024

var momentResourceID = regexp.MustCompile(`^[0-9A-Za-z_-]{12,200}$`)

type MomentMediaOptions struct {
	AccountPath   string
	SnapshotPath  string
	AESKey        string
	XORKey        int
	KeysAvailable bool
}

type MomentMediaResolution struct {
	Requested                 bool           `json:"requested"`
	LogicalMedia              int            `json:"logical_media"`
	VerifiedLocalMedia        int            `json:"verified_local_media"`
	IdentityConflicts         int            `json:"identity_conflicts"`
	NoResourceIdentifier      int            `json:"no_resource_identifier"`
	NoLocalCandidate          int            `json:"no_local_candidate"`
	LocalCandidateUnverified  int            `json:"local_candidate_unverified"`
	AmbiguousStrongCandidates int            `json:"ambiguous_strong_candidates"`
	RootsScanned              []string       `json:"roots_scanned"`
	FilesScanned              int            `json:"files_scanned"`
	ScanTruncated             bool           `json:"scan_truncated"`
	Proofs                    map[string]int `json:"proofs"`
}

type momentMediaReference struct {
	Media *MomentMedia
	Keys  map[string]string
	MD5s  map[string]bool
}

type momentFileCandidate struct {
	Path  string
	Root  string
	Key   string
	Proof string
}

type verifiedMomentCandidate struct {
	Candidate  momentFileCandidate
	Format     string
	Cipher     string
	Bytes      int64
	SourceMD5  string
	ContentMD5 string
	VerifiedBy string
	ProofValue string
	Rank       int
}

func md5Hex(data []byte) string {
	digest := md5.Sum(data)
	return hex.EncodeToString(digest[:])
}

func urlResourceKeys(value string) map[string]string {
	result := map[string]string{}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return result
	}
	parts := strings.FieldsFunc(parsed.Path, func(value rune) bool { return value == '/' })
	for index := len(parts) - 1; index >= 0; index-- {
		part := parts[index]
		if part == "0" || part == "150" || len(part) < 20 || !momentResourceID.MatchString(part) {
			continue
		}
		result[strings.ToLower(part)] = "url_resource_id"
		break
	}
	basePath := parsed.Path
	if len(parts) > 0 && (parts[len(parts)-1] == "0" || parts[len(parts)-1] == "150") {
		basePath = strings.TrimSuffix(parsed.Path, "/"+parts[len(parts)-1])
	}
	for _, scheme := range []string{"http", "https"} {
		for _, path := range []string{parsed.Path, basePath} {
			copy := *parsed
			copy.Scheme = scheme
			copy.Path = path
			copy.Fragment = ""
			result[md5Hex([]byte(copy.String()))] = "url_cache_key"
		}
	}
	return result
}

func momentMediaKeys(media *MomentMedia) (map[string]string, map[string]bool) {
	keys := map[string]string{}
	md5s := map[string]bool{}
	for _, value := range append([]string{media.MetadataMD5}, media.MetadataMD5Candidates...) {
		value = strings.ToLower(strings.TrimSpace(value))
		if len(value) == 32 {
			keys[value] = "metadata_md5_name"
			md5s[value] = true
		}
	}
	mediaID := strings.TrimSpace(media.MediaID)
	if momentResourceID.MatchString(mediaID) && len(mediaID) != 32 {
		keys[strings.ToLower(mediaID)] = "media_id_name"
	}
	for _, value := range []string{media.URL, media.ThumbURL} {
		for key, proof := range urlResourceKeys(value) {
			keys[key] = proof
		}
	}
	return keys, md5s
}

func pathDepth(root, path string) int {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." {
		return 0
	}
	return len(strings.FieldsFunc(filepath.ToSlash(relative), func(value rune) bool { return value == '/' }))
}

// relativeMomentRoots 把扫描根归一化为相对账号目录的正斜杠路径，供输出使用，
// 去掉本地绝对前缀。无法相对化的项退回到 base 名，始终不带绝对路径。
func relativeMomentRoots(accountPath string, roots []string) []string {
	if len(roots) == 0 {
		return nil
	}
	base, err := filepath.Abs(accountPath)
	relative := make([]string, 0, len(roots))
	for _, root := range roots {
		if err == nil {
			if rel, relErr := filepath.Rel(base, root); relErr == nil &&
				rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				relative = append(relative, filepath.ToSlash(rel))
				continue
			}
		}
		relative = append(relative, filepath.Base(root))
	}
	return relative
}

func discoverMomentMediaRoots(accountPath string) []string {
	accountPath, err := filepath.Abs(accountPath)
	if err != nil || accountPath == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(accountPath, "cache"),
		filepath.Join(accountPath, "cache", "sns"),
		filepath.Join(accountPath, "cache", "moments"),
		filepath.Join(accountPath, "msg", "attach"),
		filepath.Join(accountPath, "msg", "video"),
		filepath.Join(accountPath, "FileStorage", "Sns"),
		filepath.Join(accountPath, "FileStorage", "Moments"),
		filepath.Join(accountPath, "FileStorage", "MsgAttach"),
		filepath.Join(accountPath, "FileStorage", "Video"),
	}
	interesting := map[string]bool{
		"sns": true, "moments": true, "msgattach": true,
		"attach": true, "video": true, "cache": true,
	}
	directories := 0
	_ = filepath.WalkDir(accountPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		directories++
		if directories > 20000 {
			return fs.SkipAll
		}
		if pathDepth(accountPath, path) > 6 {
			return fs.SkipDir
		}
		if strings.EqualFold(entry.Name(), "db_storage") {
			return fs.SkipDir
		}
		if interesting[strings.ToLower(entry.Name())] {
			candidates = append(candidates, path)
		}
		return nil
	})
	seen := map[string]bool{}
	var roots []string
	for _, candidate := range candidates {
		info, statErr := os.Stat(candidate)
		if statErr != nil || !info.IsDir() {
			continue
		}
		absolute, absErr := filepath.Abs(candidate)
		if absErr != nil {
			continue
		}
		identity := strings.ToLower(filepath.Clean(absolute))
		if seen[identity] {
			continue
		}
		seen[identity] = true
		roots = append(roots, absolute)
	}
	sort.Slice(roots, func(left, right int) bool {
		leftDepth := pathDepth(accountPath, roots[left])
		rightDepth := pathDepth(accountPath, roots[right])
		if leftDepth == rightDepth {
			return strings.ToLower(roots[left]) < strings.ToLower(roots[right])
		}
		return leftDepth > rightDepth
	})
	return roots
}

func normalizedResourceStem(name string) string {
	stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	if strings.HasSuffix(stem, "_h") || strings.HasSuffix(stem, "_t") {
		stem = stem[:len(stem)-2]
	}
	return stem
}

func scanMomentCandidates(roots []string, wanted map[string]bool) (map[string][]momentFileCandidate, int, bool) {
	result := map[string][]momentFileCandidate{}
	seenPaths := map[string]bool{}
	filesScanned := 0
	truncated := false
	for _, root := range roots {
		if truncated {
			break
		}
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			identity := strings.ToLower(filepath.Clean(path))
			if seenPaths[identity] {
				return nil
			}
			seenPaths[identity] = true
			filesScanned++
			if filesScanned > maxMomentMediaFiles {
				truncated = true
				return fs.SkipAll
			}
			stem := normalizedResourceStem(entry.Name())
			if !wanted[stem] {
				return nil
			}
			result[stem] = append(result[stem], momentFileCandidate{Path: path, Root: root, Key: stem})
			return nil
		})
	}
	return result, filesScanned, truncated
}

func pathUnderRoot(path, root string) bool {
	path, pathErr := filepath.Abs(path)
	root, rootErr := filepath.Abs(root)
	if pathErr != nil || rootErr != nil {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func hardlinkDirectoryMap(database *sql.DB) map[int64]string {
	result := map[int64]string{}
	table := findTableCI(database, "dir2id")
	if table == "" {
		return result
	}
	available := columns(database, table)
	usernameColumn := columnCI(available, "username")
	if usernameColumn == "" {
		return result
	}
	rows, err := database.Query("SELECT rowid, " + quoteIdentifier(usernameColumn) + " FROM " + quoteIdentifier(table))
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var value any
		if rows.Scan(&id, &value) == nil {
			result[id] = asString(value)
		}
	}
	return result
}

func hardlinkRoots(accountPath, kind string) []string {
	values := []string{
		filepath.Join(accountPath, "msg", "attach"),
		filepath.Join(accountPath, "FileStorage", "MsgAttach"),
	}
	if kind == "video" {
		values = append([]string{
			filepath.Join(accountPath, "msg", "video"),
			filepath.Join(accountPath, "FileStorage", "Video"),
		}, values...)
	}
	var result []string
	for _, value := range values {
		if info, err := os.Stat(value); err == nil && info.IsDir() {
			result = append(result, value)
		}
	}
	return result
}

func hardlinkMomentCandidates(options MomentMediaOptions, reference momentMediaReference) []momentFileCandidate {
	if options.SnapshotPath == "" || options.AccountPath == "" {
		return nil
	}
	files, err := sqliteFiles(options.SnapshotPath)
	if err != nil {
		return nil
	}
	tableName := "image_hardlink_info_v4"
	if reference.Media.Kind == "video" {
		tableName = "video_hardlink_info_v4"
	}
	var result []momentFileCandidate
	seen := map[string]bool{}
	for _, path := range files {
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, tableName)
		if table == "" {
			_ = database.Close()
			continue
		}
		available := columns(database, table)
		md5Column := columnCI(available, "md5")
		fileColumn := columnCI(available, "file_name")
		dir1Column := columnCI(available, "dir1")
		dir2Column := columnCI(available, "dir2")
		if md5Column == "" || fileColumn == "" || dir1Column == "" || dir2Column == "" {
			_ = database.Close()
			continue
		}
		directories := hardlinkDirectoryMap(database)
		for value := range reference.MD5s {
			rows, queryErr := database.Query(
				"SELECT "+quoteIdentifier(fileColumn)+", "+quoteIdentifier(dir1Column)+", "+quoteIdentifier(dir2Column)+" FROM "+quoteIdentifier(table)+" WHERE "+quoteIdentifier(md5Column)+" = ? COLLATE NOCASE ORDER BY rowid",
				value,
			)
			if queryErr != nil {
				continue
			}
			for rows.Next() {
				var fileName any
				var dir1, dir2 int64
				if rows.Scan(&fileName, &dir1, &dir2) != nil {
					continue
				}
				segments := []string{}
				for _, segment := range []string{directories[dir1], directories[dir2], filepath.Base(asString(fileName))} {
					if strings.TrimSpace(segment) != "" {
						segments = append(segments, segment)
					}
				}
				for _, root := range hardlinkRoots(options.AccountPath, reference.Media.Kind) {
					candidatePath := filepath.Join(append([]string{root}, segments...)...)
					if !pathUnderRoot(candidatePath, root) {
						continue
					}
					info, statErr := os.Stat(candidatePath)
					if statErr != nil || info.IsDir() {
						continue
					}
					identity := strings.ToLower(filepath.Clean(candidatePath))
					if seen[identity] {
						continue
					}
					seen[identity] = true
					result = append(result, momentFileCandidate{
						Path: candidatePath, Root: root, Key: value, Proof: "hardlink_md5",
					})
				}
			}
			_ = rows.Close()
		}
		_ = database.Close()
	}
	return result
}

func verifyImageCandidate(candidate momentFileCandidate, reference momentMediaReference, options MomentMediaOptions) *verifiedMomentCandidate {
	info, err := os.Stat(candidate.Path)
	if err != nil || info.Size() <= 0 || info.Size() > maxMomentImageBytes {
		return nil
	}
	raw, err := os.ReadFile(candidate.Path)
	if err != nil {
		return nil
	}
	plain := raw
	format := cryptoutil.ImageFormat(raw)
	cipher := "plain"
	if format == "unknown" {
		if !options.KeysAvailable {
			return nil
		}
		plain, format, err = cryptoutil.DecryptImageDAT(raw, options.AESKey, options.XORKey)
		if err != nil {
			return nil
		}
		cipher = "dat"
	}
	sourceMD5 := md5Hex(raw)
	contentMD5 := md5Hex(plain)
	verifiedBy := candidate.Proof
	if verifiedBy == "" {
		verifiedBy = reference.Keys[candidate.Key]
	}
	rank := 3
	if verifiedBy == "hardlink_md5" {
		rank = 2
	}
	proofValue := candidate.Key
	if reference.MD5s[contentMD5] {
		verifiedBy, rank, proofValue = "plaintext_md5", 0, contentMD5
	} else if reference.MD5s[sourceMD5] {
		verifiedBy, rank, proofValue = "source_file_md5", 1, sourceMD5
	} else if verifiedBy == "metadata_md5_name" {
		verifiedBy, rank = "exact_metadata_name", 2
	}
	if verifiedBy == "" {
		return nil
	}
	return &verifiedMomentCandidate{
		Candidate: candidate, Format: format, Cipher: cipher, Bytes: info.Size(),
		SourceMD5: sourceMD5, ContentMD5: contentMD5,
		VerifiedBy: verifiedBy, ProofValue: proofValue, Rank: rank,
	}
}

func verifyVideoCandidate(candidate momentFileCandidate, reference momentMediaReference) *verifiedMomentCandidate {
	info, err := os.Stat(candidate.Path)
	if err != nil || info.IsDir() || info.Size() <= 0 || info.Size() > maxMomentVideoBytes {
		return nil
	}
	file, err := os.Open(candidate.Path)
	if err != nil {
		return nil
	}
	defer file.Close()
	if _, err := cryptoutil.ValidateMP4Reader(file, info.Size()); err != nil {
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	hasher := md5.New()
	written, err := io.Copy(hasher, file)
	if err != nil {
		return nil
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	verifiedBy := candidate.Proof
	if verifiedBy == "" {
		verifiedBy = reference.Keys[candidate.Key]
	}
	rank := 3
	if verifiedBy == "hardlink_md5" {
		rank = 2
	}
	proofValue := candidate.Key
	if reference.MD5s[digest] {
		verifiedBy, rank, proofValue = "source_file_md5", 0, digest
	} else if verifiedBy == "metadata_md5_name" {
		verifiedBy, rank = "exact_metadata_name", 2
	}
	if verifiedBy == "" {
		return nil
	}
	return &verifiedMomentCandidate{
		Candidate: candidate, Format: "mp4", Cipher: "plain", Bytes: written,
		SourceMD5: digest, ContentMD5: digest,
		VerifiedBy: verifiedBy, ProofValue: proofValue, Rank: rank,
	}
}

func chooseMomentCandidate(values []*verifiedMomentCandidate) (*verifiedMomentCandidate, bool) {
	if len(values) == 0 {
		return nil, false
	}
	sort.Slice(values, func(left, right int) bool {
		if values[left].Rank == values[right].Rank {
			return strings.ToLower(values[left].Candidate.Path) < strings.ToLower(values[right].Candidate.Path)
		}
		return values[left].Rank < values[right].Rank
	})
	best := values[0]
	for _, value := range values[1:] {
		if value.Rank != best.Rank {
			break
		}
		if value.ContentMD5 != best.ContentMD5 {
			return nil, true
		}
	}
	return best, false
}

// ResolveMomentMedia 只在账号媒体目录中按 XML 派生的精确标识寻找候选，并在容器验真后回附本地路径。
func ResolveMomentMedia(items []Moment, options MomentMediaOptions) MomentMediaResolution {
	result := MomentMediaResolution{Requested: true, Proofs: map[string]int{}}
	wanted := map[string]bool{}
	var references []momentMediaReference
	addReference := func(media *MomentMedia, identityConflict bool) {
		result.LogicalMedia++
		if identityConflict || media.ResolutionStatus == "identity_conflict" {
			result.IdentityConflicts++
			return
		}
		keys, md5s := momentMediaKeys(media)
		if len(keys) == 0 {
			media.ResolutionStatus = "no_resource_identifier"
			media.VerifiedBy = ""
			result.NoResourceIdentifier++
			return
		}
		for key := range keys {
			wanted[key] = true
		}
		references = append(references, momentMediaReference{Media: media, Keys: keys, MD5s: md5s})
	}
	for itemIndex := range items {
		identityConflict := items[itemIndex].ParseStatus == "identity_conflict"
		for mediaIndex := range items[itemIndex].Media {
			addReference(&items[itemIndex].Media[mediaIndex], identityConflict)
		}
		for interactionIndex := range items[itemIndex].Interactions.Comments {
			interaction := &items[itemIndex].Interactions.Comments[interactionIndex]
			for mediaIndex := range interaction.Media {
				addReference(&interaction.Media[mediaIndex], identityConflict)
			}
		}
	}
	roots := discoverMomentMediaRoots(options.AccountPath)
	// roots 是账号目录下的绝对路径，用于扫描；但 roots_scanned 会进入命令输出，
	// 绝对路径会泄露账号在本地文件系统的位置（与 Path、SourceRoot 用 json:"-" 同理）。
	// 只暴露相对账号目录的类别（cache、msg/attach 等固定的微信目录名），不泄露前缀。
	result.RootsScanned = relativeMomentRoots(options.AccountPath, roots)
	candidates, scanned, truncated := scanMomentCandidates(roots, wanted)
	result.FilesScanned = scanned
	result.ScanTruncated = truncated
	for _, reference := range references {
		var validated []*verifiedMomentCandidate
		seen := map[string]bool{}
		for _, candidate := range hardlinkMomentCandidates(options, reference) {
			identity := strings.ToLower(filepath.Clean(candidate.Path))
			seen[identity] = true
			var verified *verifiedMomentCandidate
			if reference.Media.Kind == "video" {
				verified = verifyVideoCandidate(candidate, reference)
			} else {
				verified = verifyImageCandidate(candidate, reference, options)
			}
			if verified != nil {
				validated = append(validated, verified)
			}
		}
		for key, proof := range reference.Keys {
			for _, candidate := range candidates[key] {
				candidate.Proof = proof
				identity := strings.ToLower(filepath.Clean(candidate.Path))
				if seen[identity] {
					continue
				}
				seen[identity] = true
				var verified *verifiedMomentCandidate
				if reference.Media.Kind == "video" {
					verified = verifyVideoCandidate(candidate, reference)
				} else {
					verified = verifyImageCandidate(candidate, reference, options)
				}
				if verified != nil {
					validated = append(validated, verified)
				}
			}
		}
		if len(seen) == 0 {
			reference.Media.ResolutionStatus = "no_local_candidate"
			reference.Media.VerifiedBy = ""
			result.NoLocalCandidate++
			continue
		}
		selected, ambiguous := chooseMomentCandidate(validated)
		if ambiguous {
			reference.Media.ResolutionStatus = "ambiguous_strong_candidates"
			reference.Media.VerifiedBy = ""
			result.AmbiguousStrongCandidates++
			continue
		}
		if selected == nil {
			reference.Media.ResolutionStatus = "local_candidate_unverified"
			reference.Media.VerifiedBy = ""
			result.LocalCandidateUnverified++
			continue
		}
		absolute, _ := filepath.Abs(selected.Candidate.Path)
		reference.Media.ResolutionStatus = "verified_local"
		reference.Media.VerifiedBy = selected.VerifiedBy
		reference.Media.Local = &MomentLocalMedia{
			Path: absolute, Format: selected.Format, Cipher: selected.Cipher,
			Bytes: selected.Bytes, SourceMD5: selected.SourceMD5, ContentMD5: selected.ContentMD5,
			ProofValue: selected.ProofValue, VerifiedBy: selected.VerifiedBy,
			SourceRoot: selected.Candidate.Root,
		}
		result.VerifiedLocalMedia++
		result.Proofs[selected.VerifiedBy]++
	}
	return result
}
