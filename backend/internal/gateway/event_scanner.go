package gateway

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"time"
)

const (
	defaultScannerBufferCap = 1 << 20
	maxScannerBufferCap     = 64 << 20
)

// SSEEvent 是 F-GW-002 Phase A 中有界的 upstream event 信封。
type SSEEvent struct {
	Type       string    `json:"type"`
	Data       []byte    `json:"data"`
	ObservedAt time.Time `json:"observed_at"`
}

// ScanSSEEvents 用一个有界缓冲区扫描 F-GW-002 Phase A 的 SSE event。
func ScanSSEEvents(ctx context.Context, r io.Reader, bufferCap int) iter.Seq2[SSEEvent, error] {
	return func(yield func(SSEEvent, error) bool) {
		capBytes := normalizeScannerCap(bufferCap)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), capBytes)

		var typ string
		var data bytes.Buffer
		emit := func() bool {
			if data.Len() == 0 && typ == "" {
				return true
			}
			payload := bytes.Clone(bytes.TrimSuffix(data.Bytes(), []byte{'\n'}))
			evt := SSEEvent{Type: typ, Data: payload, ObservedAt: time.Now()}
			typ = ""
			data.Reset()
			return yield(evt, nil)
		}

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				yield(SSEEvent{}, ctx.Err())
				return
			default:
			}
			line := scanner.Bytes()
			if len(line) == 0 {
				if !emit() {
					return
				}
				continue
			}
			if bytes.HasPrefix(line, []byte(":")) {
				continue
			}
			if bytes.HasPrefix(line, []byte("event:")) {
				typ = strings.TrimSpace(string(bytes.TrimPrefix(line, []byte("event:"))))
				continue
			}
			if bytes.HasPrefix(line, []byte("data:")) {
				part := bytes.TrimPrefix(line, []byte("data:"))
				if len(part) > 0 && part[0] == ' ' {
					part = part[1:]
				}
				if data.Len()+len(part)+1 > capBytes {
					yield(SSEEvent{}, ErrScannerOverflow)
					return
				}
				data.Write(part)
				data.WriteByte('\n')
			}
		}
		if err := scanner.Err(); err != nil {
			if errors.Is(err, bufio.ErrTooLong) {
				yield(SSEEvent{}, ErrScannerOverflow)
			} else {
				yield(SSEEvent{}, err)
			}
			return
		}
		emit()
	}
}

func normalizeScannerCap(bufferCap int) int {
	if bufferCap <= 0 {
		return defaultScannerBufferCap
	}
	if bufferCap > maxScannerBufferCap {
		return maxScannerBufferCap
	}
	return bufferCap
}
