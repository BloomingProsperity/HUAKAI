package main

import (
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/tools/fingerprint-collector/internal/capture"
)

func TestCaptureOptions_DefaultParametersStayCompatible(t *testing.T) {
	flags, fs, err := parseCommandFlags(nil, io.Discard)
	if err != nil {
		t.Fatalf("parse default flags: %v", err)
	}
	opts, err := flags.captureOptions(fs)
	if err != nil {
		t.Fatalf("resolve default options: %v", err)
	}
	if opts.SampleCount != defaultSampleCount {
		t.Fatalf("sample count = %d, want %d", opts.SampleCount, defaultSampleCount)
	}
	if opts.OutputDir != defaultOutputDir {
		t.Fatalf("output dir = %q, want %q", opts.OutputDir, defaultOutputDir)
	}
	if opts.TargetName != "" {
		t.Fatalf("target name = %q, want empty", opts.TargetName)
	}
}

func TestCaptureOptions_TargetNameSetsMetadataModeName(t *testing.T) {
	flags, fs, err := parseCommandFlags([]string{"-target-name", "openai_codex"}, io.Discard)
	if err != nil {
		t.Fatalf("parse target flags: %v", err)
	}
	opts, err := flags.captureOptions(fs)
	if err != nil {
		t.Fatalf("resolve target options: %v", err)
	}
	meta := buildMetadata(opts, time.Unix(1, 0).UTC(), time.Unix(2, 0).UTC(), 3, "ok", true)
	if meta.ModeName != "openai_codex" {
		t.Fatalf("metadata mode_name = %q, want openai_codex", meta.ModeName)
	}
	if meta.SampleCount != 3 {
		t.Fatalf("metadata sample_count = %d, want 3", meta.SampleCount)
	}
}

func TestCaptureOptions_OutputDirIsolation(t *testing.T) {
	flags, fs, err := parseCommandFlags([]string{"-target-name", "kiro_cli"}, io.Discard)
	if err != nil {
		t.Fatalf("parse target flags: %v", err)
	}
	opts, err := flags.captureOptions(fs)
	if err != nil {
		t.Fatalf("resolve target options: %v", err)
	}
	if opts.OutputDir != "./output/kiro_cli/" {
		t.Fatalf("target output dir = %q, want ./output/kiro_cli/", opts.OutputDir)
	}

	flags, fs, err = parseCommandFlags([]string{
		"-target-name", "gemini_advanced",
		"-output-dir", "/tmp/hk-gemini-capture",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parse output-dir flags: %v", err)
	}
	opts, err = flags.captureOptions(fs)
	if err != nil {
		t.Fatalf("resolve output-dir options: %v", err)
	}
	if opts.OutputDir != "/tmp/hk-gemini-capture" {
		t.Fatalf("explicit output dir = %q, want /tmp/hk-gemini-capture", opts.OutputDir)
	}
}

func TestCaptureOptions_SampleCountOverridesLegacyMinSamples(t *testing.T) {
	flags, fs, err := parseCommandFlags([]string{"-min-samples", "2", "-sample-count", "7"}, io.Discard)
	if err != nil {
		t.Fatalf("parse sample flags: %v", err)
	}
	opts, err := flags.captureOptions(fs)
	if err != nil {
		t.Fatalf("resolve sample flags: %v", err)
	}
	if opts.SampleCount != 7 {
		t.Fatalf("sample count = %d, want 7", opts.SampleCount)
	}
}

func TestCaptureFilter_RawBPFFlagOverridesHostDerivedFilter(t *testing.T) {
	rawBPF := "net 64.239.0.0/16 and tcp port 443"
	flags, fs, err := parseCommandFlags([]string{
		"-host", "chatgpt.com",
		"-bpf", rawBPF,
	}, io.Discard)
	if err != nil {
		t.Fatalf("parse bpf flags: %v", err)
	}
	opts, err := flags.captureOptions(fs)
	if err != nil {
		t.Fatalf("resolve bpf options: %v", err)
	}

	filter, resolvedIPs, usedRawBPF, err := resolveCaptureFilter(opts.Host, opts.RawBPF)
	if err != nil {
		t.Fatalf("resolve raw bpf filter: %v", err)
	}
	if !usedRawBPF {
		t.Fatalf("usedRawBPF = false, want true")
	}
	if filter != rawBPF {
		t.Fatalf("filter = %q, want raw BPF %q", filter, rawBPF)
	}
	if resolvedIPs != nil {
		t.Fatalf("resolved IPs = %v, want nil for raw BPF", resolvedIPs)
	}
}

func TestCaptureFilter_EmptyBPFUsesBuildBPFFilter(t *testing.T) {
	flags, fs, err := parseCommandFlags([]string{"-host", "127.0.0.1"}, io.Discard)
	if err != nil {
		t.Fatalf("parse host flags: %v", err)
	}
	opts, err := flags.captureOptions(fs)
	if err != nil {
		t.Fatalf("resolve host options: %v", err)
	}

	filter, resolvedIPs, usedRawBPF, err := resolveCaptureFilter(opts.Host, opts.RawBPF)
	if err != nil {
		t.Fatalf("resolve derived bpf filter: %v", err)
	}
	wantFilter, wantIPs, err := capture.BuildBPFFilter(opts.Host, 443)
	if err != nil {
		t.Fatalf("build expected bpf filter: %v", err)
	}
	if usedRawBPF {
		t.Fatalf("usedRawBPF = true, want false")
	}
	if filter != wantFilter {
		t.Fatalf("filter = %q, want %q", filter, wantFilter)
	}
	if !reflect.DeepEqual(resolvedIPs, wantIPs) {
		t.Fatalf("resolved IPs = %v, want %v", resolvedIPs, wantIPs)
	}
}

func TestValidateBPF_RejectsEmptyExpression(t *testing.T) {
	if err := capture.ValidateBPF(" \t "); err == nil {
		t.Fatalf("ValidateBPF(empty) returned nil, want error")
	}
}
