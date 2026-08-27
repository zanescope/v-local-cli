package cryptoutil

import (
	"crypto/hmac"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

type walFrameLocation struct {
	pageNumber uint32
	pageOffset int64
}

func readAtFull(file *os.File, offset int64, buffer []byte) error {
	_, err := io.ReadFull(io.NewSectionReader(file, offset, int64(len(buffer))), buffer)
	return err
}

// scanWALFile 只接受能被实际数据支撑的提交记录。WAL 校验和是公开且无密钥的算法，
// 帧头里的 db-size 字段可以被任意构造，因此不能直接拿它决定输出文件长度：
// mainPages 给出主库已有的页数，帧自身提供其余页，超出两者之和的声明一律视为损坏。
func scanWALFile(path string, expectedPageSize int64, mainPages uint32) ([]walFrameLocation, WALInfo, error) {
	info := WALInfo{Status: "absent"}
	if path == "" {
		return nil, info, nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, info, nil
	}
	if err != nil {
		return nil, info, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, info, err
	}
	info.Present = true
	info.Status = "no_committed_frames"
	if stat.Size() < 32 {
		info.TrailingBytes = int(stat.Size())
		return nil, info, nil
	}
	header := make([]byte, 32)
	if err := readAtFull(file, 0, header); err != nil {
		return nil, info, err
	}
	magic := binary.BigEndian.Uint32(header[:4])
	pageSize := binary.BigEndian.Uint32(header[8:12])
	if (magic != 0x377f0682 && magic != 0x377f0683) || int64(pageSize) != expectedPageSize {
		info.Status = "unsupported_header"
		return nil, info, nil
	}
	bigEndian := magic == 0x377f0683
	first, second, err := walChecksum(header[:24], bigEndian, 0, 0)
	if err != nil || first != binary.BigEndian.Uint32(header[24:28]) || second != binary.BigEndian.Uint32(header[28:32]) {
		info.Status = "invalid_header_checksum"
		return nil, info, nil
	}
	salts := append([]byte(nil), header[16:24]...)
	frameSize := int64(24) + expectedPageSize
	frameHeader := make([]byte, 24)
	page := make([]byte, int(expectedPageSize))
	frames := make([]walFrameLocation, 0)
	committed := 0
	var databasePages uint32
	offset := int64(32)
	for offset+frameSize <= stat.Size() {
		if err := readAtFull(file, offset, frameHeader); err != nil {
			break
		}
		if err := readAtFull(file, offset+24, page); err != nil {
			break
		}
		if !hmac.Equal(frameHeader[8:16], salts) {
			break
		}
		pageNumber := binary.BigEndian.Uint32(frameHeader[:4])
		if pageNumber == 0 {
			break
		}
		first, second, err = walChecksum(frameHeader[:8], bigEndian, first, second)
		if err != nil {
			break
		}
		first, second, err = walChecksum(page, bigEndian, first, second)
		if err != nil || first != binary.BigEndian.Uint32(frameHeader[16:20]) || second != binary.BigEndian.Uint32(frameHeader[20:24]) {
			break
		}
		frames = append(frames, walFrameLocation{pageNumber: pageNumber, pageOffset: offset + 24})
		if size := binary.BigEndian.Uint32(frameHeader[4:8]); size != 0 {
			// 提交后的每一页都必须真实存在：要么本就在主库里，要么由某个 WAL 帧提供。
			// 因此库大小的上界是“主库页数 + 已见帧数”。帧头里的 pageNumber 同样是可
			// 任意构造的 uint32，不能拿它当界，否则单个伪造帧就能把上界抬到 4G 页。
			if uint64(size) > uint64(mainPages)+uint64(len(frames)) {
				info.Status = "invalid_commit_size"
				break
			}
			committed = len(frames)
			databasePages = size
		}
		offset += frameSize
	}
	info.ValidFrames = len(frames)
	info.CommittedFrames = committed
	info.DatabasePages = databasePages
	info.TrailingBytes = int(stat.Size() - offset)
	if committed > 0 {
		info.Status = "applied"
	}
	return frames[:committed], info, nil
}

func plainSQLitePageSize(header []byte) (int64, error) {
	if len(header) < 100 || string(header[:len(sqliteHeader)]) != string(sqliteHeader) {
		return 0, errors.New("不是明文 SQLite 主库")
	}
	value := binary.BigEndian.Uint16(header[16:18])
	if value == 1 {
		return 65536, nil
	}
	if !validPageSizes[value] {
		return 0, errors.New("明文 SQLite 页大小无效")
	}
	return int64(value), nil
}

func decryptMainFile(source, destination, keyHex string) (*solvedKey, int64, int64, error) {
	input, err := os.Open(source)
	if err != nil {
		return nil, 0, 0, err
	}
	defer input.Close()
	stat, err := input.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	if stat.Size() < 100 {
		return nil, 0, 0, errors.New("数据库过短")
	}
	firstPageBytes := int64(SQLCipherPageSize)
	if stat.Size() < firstPageBytes {
		firstPageBytes = stat.Size()
	}
	firstPage := make([]byte, int(firstPageBytes))
	if err := readAtFull(input, 0, firstPage); err != nil {
		return nil, 0, 0, err
	}
	plainInput := string(firstPage[:len(sqliteHeader)]) == string(sqliteHeader)
	var solved *solvedKey
	pageSize := int64(SQLCipherPageSize)
	if plainInput {
		pageSize, err = plainSQLitePageSize(firstPage)
		if err != nil || stat.Size()%pageSize != 0 {
			return nil, 0, 0, errors.New("明文 SQLite 主库页边界无效")
		}
	} else {
		passphrase, normalizeErr := normalizeHexKey(keyHex)
		if normalizeErr != nil {
			return nil, 0, 0, normalizeErr
		}
		if stat.Size() < SQLCipherPageSize || stat.Size()%SQLCipherPageSize != 0 {
			return nil, 0, 0, fmt.Errorf("数据库长度不是 %d 的整数倍", SQLCipherPageSize)
		}
		solved, err = solveKey(firstPage, passphrase)
		if err != nil {
			return nil, 0, 0, err
		}
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, 0, 0, err
	}
	removeOutput := true
	defer func() {
		_ = output.Close()
		if removeOutput {
			_ = os.Remove(destination)
		}
	}()
	if solved == nil {
		written, copyErr := io.Copy(output, io.LimitReader(input, stat.Size()+1))
		if copyErr != nil || written != stat.Size() {
			return nil, 0, 0, errors.New("明文 SQLite 主库流式复制失败")
		}
	} else {
		page := make([]byte, SQLCipherPageSize)
		for offset := int64(0); offset < stat.Size(); offset += SQLCipherPageSize {
			if err := readAtFull(input, offset, page); err != nil {
				return nil, 0, 0, err
			}
			value, decryptErr := decryptPage(page, solved.key, solved.reserve, uint32(offset/SQLCipherPageSize+1))
			if decryptErr != nil {
				return nil, 0, 0, decryptErr
			}
			if _, err := output.Write(value); err != nil {
				return nil, 0, 0, err
			}
		}
	}
	if err := output.Sync(); err != nil {
		return nil, 0, 0, err
	}
	if err := output.Close(); err != nil {
		return nil, 0, 0, err
	}
	removeOutput = false
	return solved, pageSize, stat.Size(), nil
}

func applyWALFile(path string, output *os.File, frames []walFrameLocation, info *WALInfo, solved *solvedKey, pageSize int64) error {
	if len(frames) == 0 {
		return nil
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	page := make([]byte, int(pageSize))
	seen := make(map[uint32]bool)
	for _, frame := range frames {
		if frame.pageNumber > info.DatabasePages {
			continue
		}
		if err := readAtFull(input, frame.pageOffset, page); err != nil {
			return err
		}
		value := page
		if solved != nil {
			value, err = decryptPage(page, solved.key, solved.reserve, frame.pageNumber)
			if err != nil {
				return err
			}
		}
		offset := int64(frame.pageNumber-1) * pageSize
		if _, err := output.WriteAt(value, offset); err != nil {
			return err
		}
		seen[frame.pageNumber] = true
	}
	info.AppliedPages = len(seen)
	return output.Truncate(int64(info.DatabasePages) * pageSize)
}

// DecryptSQLCipherSnapshotFiles 逐页解密稳定副本，并流式回放最后一个校验通过的 WAL 提交。
func DecryptSQLCipherSnapshotFiles(databasePath, walPath, destination, keyHex string) (WALInfo, int64, error) {
	return DecryptSQLCipherSnapshotFilesWithProfile(databasePath, walPath, destination, keyHex, SQLCipherDefaultProfileID)
}

// DecryptSQLCipherSnapshotFilesWithProfile 把 profile 作为解密兼容性的原子门禁。
// 当前版本只登记已经通过验证的 WCDB v4 profile；未知 profile 必须显式失败，
// 不能静默套用默认参数。
func DecryptSQLCipherSnapshotFilesWithProfile(databasePath, walPath, destination, keyHex, profileID string) (WALInfo, int64, error) {
	if profileID != "" && profileID != SQLCipherDefaultProfileID {
		return WALInfo{}, 0, errors.New("数据库 profile 不受支持")
	}
	solved, pageSize, mainSize, err := decryptMainFile(databasePath, destination, keyHex)
	if err != nil {
		return WALInfo{}, 0, err
	}
	// 主库长度已被 decryptMainFile 校验为页大小的整数倍，可直接换算成页数。
	frames, info, err := scanWALFile(walPath, pageSize, uint32(mainSize/pageSize))
	if err != nil {
		_ = os.Remove(destination)
		return info, 0, err
	}
	if info.Status != "absent" && info.Status != "no_committed_frames" && info.Status != "applied" {
		_ = os.Remove(destination)
		return info, 0, fmt.Errorf("WAL 无法安全读取：%s", info.Status)
	}
	if len(frames) > 0 {
		output, openErr := os.OpenFile(destination, os.O_RDWR, 0)
		if openErr != nil {
			_ = os.Remove(destination)
			return info, 0, openErr
		}
		applyErr := applyWALFile(walPath, output, frames, &info, solved, pageSize)
		syncErr := output.Sync()
		closeErr := output.Close()
		if applyErr != nil || syncErr != nil || closeErr != nil {
			_ = os.Remove(destination)
			if applyErr != nil {
				return info, 0, applyErr
			}
			if syncErr != nil {
				return info, 0, syncErr
			}
			return info, 0, closeErr
		}
	}
	stat, err := os.Stat(destination)
	if err != nil {
		_ = os.Remove(destination)
		return info, 0, err
	}
	return info, stat.Size(), nil
}
