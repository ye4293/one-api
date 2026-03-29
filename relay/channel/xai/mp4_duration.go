package xai

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GetRemoteMP4Duration 通过 HTTP Range 请求解析远程 MP4 文件头部获取精确时长（秒）。
// 仅下载必要的字节（moov/mvhd atom），不下载整个文件。
// 失败时返回 0 和 error，调用方可决定是否阻断主流程。
func GetRemoteMP4Duration(videoURL string) (float64, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	// 先尝试读取文件头部 32KB，moov atom 通常在文件开头
	duration, err := parseMoovFromRange(client, videoURL, 0, 32*1024)
	if err == nil && duration > 0 {
		return duration, nil
	}

	// moov 可能在文件尾部，先获取文件大小
	headResp, err := client.Head(videoURL)
	if err != nil {
		return 0, fmt.Errorf("HEAD request failed: %w", err)
	}
	headResp.Body.Close()

	fileSize := headResp.ContentLength
	if fileSize <= 0 {
		return 0, fmt.Errorf("cannot determine file size (Content-Length: %d)", fileSize)
	}

	// 读取文件尾部 256KB 寻找 moov
	tailSize := int64(256 * 1024)
	if tailSize > fileSize {
		tailSize = fileSize
	}
	start := fileSize - tailSize
	duration, err = parseMoovFromRange(client, videoURL, start, tailSize)
	if err != nil {
		return 0, fmt.Errorf("moov atom not found in file tail: %w", err)
	}
	return duration, nil
}

// parseMoovFromRange 从指定的字节范围中查找 moov atom 并提取时长
func parseMoovFromRange(client *http.Client, url string, start, length int64) (float64, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+length-1))

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// 限制读取量，防止服务器忽略 Range 返回整个文件
	maxRead := length + 1024 // 允许少量溢出
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRead))
	if err != nil {
		return 0, err
	}

	return findMvhdDuration(data)
}

// findMvhdDuration 在字节数据中查找 moov > mvhd atom 并提取时长
func findMvhdDuration(data []byte) (float64, error) {
	moovData, err := findBox(data, "moov")
	if err != nil {
		return 0, err
	}

	mvhdData, err := findBox(moovData, "mvhd")
	if err != nil {
		return 0, err
	}

	return parseMvhd(mvhdData)
}

// findBox 在 MP4 box 序列中查找指定类型的 box，返回 box 的 payload（不含 header）
func findBox(data []byte, boxType string) ([]byte, error) {
	offset := 0
	for offset+8 <= len(data) {
		size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		typ := string(data[offset+4 : offset+8])

		headerSize := 8
		actualSize := size

		if size == 1 {
			// 64-bit extended size
			if offset+16 > len(data) {
				break
			}
			actualSize = int(binary.BigEndian.Uint64(data[offset+8 : offset+16]))
			headerSize = 16
		} else if size == 0 {
			// box extends to end of data
			actualSize = len(data) - offset
		}

		if actualSize < headerSize || offset+actualSize > len(data) {
			// 数据不完整但可能是 moov 在边界，尝试用剩余数据
			if typ == boxType {
				return data[offset+headerSize:], nil
			}
			break
		}

		if typ == boxType {
			return data[offset+headerSize : offset+actualSize], nil
		}

		offset += actualSize
	}
	return nil, fmt.Errorf("box '%s' not found", boxType)
}

// parseMvhd 解析 mvhd atom payload，提取 timescale 和 duration
func parseMvhd(data []byte) (float64, error) {
	if len(data) < 1 {
		return 0, fmt.Errorf("mvhd data too short")
	}

	version := data[0]
	switch version {
	case 0:
		// version 0: 4 bytes each for creation/modification time, timescale, duration
		if len(data) < 20 {
			return 0, fmt.Errorf("mvhd v0 data too short: %d bytes", len(data))
		}
		timescale := binary.BigEndian.Uint32(data[12:16])
		duration := binary.BigEndian.Uint32(data[16:20])
		if timescale == 0 {
			return 0, fmt.Errorf("timescale is 0")
		}
		return float64(duration) / float64(timescale), nil
	case 1:
		// version 1: version(1) + flags(3) + creation_time(8) + modification_time(8) + timescale(4) + duration(8) = 32
		if len(data) < 32 {
			return 0, fmt.Errorf("mvhd v1 data too short: %d bytes", len(data))
		}
		timescale := binary.BigEndian.Uint32(data[20:24])
		duration := binary.BigEndian.Uint64(data[24:32])
		if timescale == 0 {
			return 0, fmt.Errorf("timescale is 0")
		}
		return float64(duration) / float64(timescale), nil
	default:
		return 0, fmt.Errorf("unsupported mvhd version: %d", version)
	}
}
