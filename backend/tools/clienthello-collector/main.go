// main.go 负责 CLI 参数、输入源选择与监听编排。
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const (
	defaultModeName      = "anthropic-cli-mimicry-v1"
	defaultTargetHost    = "api.anthropic.com"
	defaultListenAddress = "127.0.0.1:9443"
)

func main() {
	addr := flag.String("addr", defaultListenAddress, "TCP address to listen on")
	outPath := flag.String("out", "", "output JSON path; stdout when empty")
	modeName := flag.String("mode-name", defaultModeName, "template mode_name")
	targetHost := flag.String("target-host", defaultTargetHost, "sanitized target_host for template metadata")
	captureTargetHost := flag.String("capture-target-host", "127.0.0.1", "actual capture target host")
	stdin := flag.Bool("stdin", false, "read one TLS ClientHello record from stdin instead of listening")
	timeout := flag.Duration("timeout", 20*time.Second, "accept/read timeout")
	flag.Parse()

	var output *CollectorOutput
	var err error
	if *stdin {
		output, err = collectFromReader(os.Stdin, *modeName, *targetHost, *captureTargetHost)
	} else {
		output, err = collect(*addr, *timeout, *modeName, *targetHost, *captureTargetHost)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "clienthello-collector: %v\n", err)
		os.Exit(1)
	}
	if err := writeCollectorOutput(output, *outPath); err != nil {
		fmt.Fprintf(os.Stderr, "clienthello-collector: %v\n", err)
		os.Exit(1)
	}
	if *outPath != "" {
		fmt.Fprintf(os.Stderr, "wrote %s\n", *outPath)
	}
}

func collect(addr string, timeout time.Duration, modeName, targetHost, captureTargetHost string) (*CollectorOutput, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	defer listener.Close()
	if tcp, ok := listener.(*net.TCPListener); ok && timeout > 0 {
		if err := tcp.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, fmt.Errorf("set listener deadline: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "listening on %s\n", listener.Addr())
	conn, err := listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}
	defer conn.Close()
	if timeout > 0 {
		if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, fmt.Errorf("set connection deadline: %w", err)
		}
	}
	return collectFromReader(conn, modeName, targetHost, captureTargetHost)
}

func collectFromReader(r io.Reader, modeName, targetHost, captureTargetHost string) (*CollectorOutput, error) {
	record, err := readTLSRecord(r)
	if err != nil {
		return nil, err
	}
	return outputFromRecord(record, modeName, targetHost, captureTargetHost)
}
