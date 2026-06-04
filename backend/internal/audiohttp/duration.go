package audiohttp

import (
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"

	"github.com/shopspring/decimal"
)

const minCompressedBitsPerSecond int64 = 8000

type durationEstimate struct {
	Seconds   decimal.Decimal
	Precise   bool
	SizeBound bool
	Source    string
}

func estimateDuration(file audioFile) (durationEstimate, error) {
	if looksLikeWAV(file) {
		if seconds, err := wavDurationSeconds(file.Data); err == nil && seconds.GreaterThan(decimal.Zero) {
			return durationEstimate{Seconds: seconds, Precise: true, Source: "wav"}, nil
		}
	}
	if len(file.Data) == 0 {
		return durationEstimate{}, errors.New("audio duration: empty file")
	}
	bits := decimal.NewFromInt(int64(len(file.Data))).Mul(decimal.NewFromInt(8))
	seconds := bits.Div(decimal.NewFromInt(minCompressedBitsPerSecond))
	if !seconds.GreaterThan(decimal.Zero) {
		return durationEstimate{}, errors.New("audio duration: non-positive size bound")
	}
	return durationEstimate{Seconds: seconds, SizeBound: true, Source: "size_bound"}, nil
}

func looksLikeWAV(file audioFile) bool {
	if len(file.Data) >= 12 && string(file.Data[0:4]) == "RIFF" && string(file.Data[8:12]) == "WAVE" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(file.Name))
	return ext == ".wav" || strings.Contains(strings.ToLower(file.ContentType), "wav")
}

func wavDurationSeconds(raw []byte) (decimal.Decimal, error) {
	if len(raw) < 44 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return decimal.Zero, errors.New("audio duration: not wav")
	}
	var sampleRate uint32
	var blockAlign uint16
	var dataBytes uint32
	for off := 12; off+8 <= len(raw); {
		chunkID := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		dataStart := off + 8
		dataEnd := dataStart + size
		if size < 0 || dataEnd > len(raw) {
			return decimal.Zero, errors.New("audio duration: invalid wav chunk")
		}
		switch chunkID {
		case "fmt ":
			if size < 16 {
				return decimal.Zero, errors.New("audio duration: invalid fmt chunk")
			}
			format := binary.LittleEndian.Uint16(raw[dataStart : dataStart+2])
			if format != 1 && format != 3 {
				return decimal.Zero, errors.New("audio duration: unsupported wav format")
			}
			sampleRate = binary.LittleEndian.Uint32(raw[dataStart+4 : dataStart+8])
			blockAlign = binary.LittleEndian.Uint16(raw[dataStart+12 : dataStart+14])
		case "data":
			dataBytes = uint32(size)
		}
		off = dataEnd
		if off%2 == 1 {
			off++
		}
	}
	if sampleRate == 0 || blockAlign == 0 || dataBytes == 0 {
		return decimal.Zero, errors.New("audio duration: wav timing fields missing")
	}
	samples := decimal.NewFromInt(int64(dataBytes)).Div(decimal.NewFromInt(int64(blockAlign)))
	return samples.Div(decimal.NewFromInt(int64(sampleRate))), nil
}
