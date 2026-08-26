//go:build linux

// Package packetcapture provides the edge-local packet capture primitive.
package packetcapture

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	defaultDuration   = 30 * time.Second
	defaultMaxBytes   = 64 << 20
	defaultMaxPackets = 100_000
	defaultSnaplen    = 1514

	maxDuration = 5 * time.Minute
	maxBytes    = 256 << 20
	maxPackets  = 500_000
	maxSnaplen  = 65_535

	previewLineLimit = 80
)

type Request struct {
	CaptureID        string        `json:"capture_id"`
	Interface        string        `json:"interface"`
	NetworkNamespace string        `json:"network_namespace,omitempty"`
	Filter           string        `json:"filter,omitempty"`
	Duration         time.Duration `json:"-"`
	MaxBytes         int64         `json:"max_bytes"`
	MaxPackets       int           `json:"max_packets"`
	Snaplen          int           `json:"snaplen"`
	Promiscuous      bool          `json:"promiscuous"`
	StartAt          *time.Time    `json:"start_at,omitempty"`
}

// Result is the edge-owned capture snapshot. LivePreview keeps a bounded tail
// of decoded tcpdump lines and never carries raw packet payloads.
type Result struct {
	Path          string    `json:"-"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Packets       int       `json:"packets"`
	PayloadBytes  int64     `json:"payload_bytes"`
	FileBytes     int64     `json:"file_bytes"`
	StopReason    string    `json:"stop_reason"`
	InterfaceName string    `json:"interface"`
	LivePreview   []string  `json:"live_preview,omitempty"`
}

type ProgressReporter func(Result)

type Capturer struct{ baseDir string }

func New(baseDir string) (*Capturer, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("packet capture: base directory required")
	}
	clean := filepath.Clean(baseDir)
	if clean == "." || clean == string(filepath.Separator) {
		return nil, fmt.Errorf("packet capture: unsafe base directory %q", baseDir)
	}
	return &Capturer{baseDir: clean}, nil
}

func (c *Capturer) Capture(ctx context.Context, in Request) (Result, error) {
	return c.capture(ctx, in, nil)
}

func (c *Capturer) CaptureWithProgress(ctx context.Context, in Request, report ProgressReporter) (Result, error) {
	return c.capture(ctx, in, report)
}

func (c *Capturer) capture(ctx context.Context, in Request, report ProgressReporter) (Result, error) {
	if c == nil {
		return Result{}, errors.New("packet capture: nil capturer")
	}
	req, err := normalizeRequest(in)
	if err != nil {
		return Result{}, err
	}
	return c.captureInNamespace(ctx, req, report)
}

func (c *Capturer) captureInNamespace(ctx context.Context, req Request, report ProgressReporter) (result Result, err error) {
	if req.NetworkNamespace == "" {
		return c.captureCurrentNamespace(ctx, req, report)
	}
	// A network namespace is attached to an OS thread. Child processes inherit
	// the selected namespace; restore host networking before the runtime reuses
	// this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	original, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return Result{}, fmt.Errorf("packet capture: open current network namespace: %w", err)
	}
	defer original.Close()
	target, err := os.Open(filepath.Join("/var/run/netns", req.NetworkNamespace))
	if err != nil {
		return Result{}, fmt.Errorf("packet capture: open network namespace %q: %w", req.NetworkNamespace, err)
	}
	defer target.Close()
	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		return Result{}, fmt.Errorf("packet capture: enter network namespace %q: %w", req.NetworkNamespace, err)
	}
	defer func() {
		if restoreErr := unix.Setns(int(original.Fd()), unix.CLONE_NEWNET); restoreErr != nil && err == nil {
			err = fmt.Errorf("packet capture: restore host network namespace: %w", restoreErr)
		}
	}()
	return c.captureCurrentNamespace(ctx, req, report)
}

// captureCurrentNamespace runs a fixed tcpdump binary with an argv slice. The
// PCAP stream is persisted and decoded by a second tcpdump process, so the
// preview and artifact represent exactly the same collection.
func (c *Capturer) captureCurrentNamespace(ctx context.Context, req Request, report ProgressReporter) (Result, error) {
	tcpdumpPath, err := exec.LookPath("tcpdump")
	if err != nil {
		return Result{}, fmt.Errorf("packet capture: tcpdump is required on edge: %w", err)
	}
	if err := os.MkdirAll(c.baseDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("packet capture: create output directory: %w", err)
	}
	path := filepath.Join(c.baseDir, req.CaptureID+".pcap")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("packet capture: create pcap: %w", err)
	}
	fileClosed := false
	cleanup := func(cause error) (Result, error) {
		if !fileClosed {
			closeErr := file.Close()
			fileClosed = true
			if closeErr != nil {
				cause = fmt.Errorf("%w; close pcap: %v", cause, closeErr)
			}
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			cause = fmt.Errorf("%w; remove partial capture: %v", cause, removeErr)
		}
		return Result{}, cause
	}

	producer := exec.Command(tcpdumpPath, tcpdumpWriteArgs(req)...)
	producerOut, err := producer.StdoutPipe()
	if err != nil {
		return cleanup(fmt.Errorf("packet capture: tcpdump stdout: %w", err))
	}
	var producerErr bytes.Buffer
	producer.Stderr = &producerErr
	decoder := exec.Command(tcpdumpPath, "-l", "-n", "-q", "-r", "-")
	decoderIn, err := decoder.StdinPipe()
	if err != nil {
		return cleanup(fmt.Errorf("packet capture: tcpdump preview stdin: %w", err))
	}
	decoderOut, err := decoder.StdoutPipe()
	if err != nil {
		return cleanup(fmt.Errorf("packet capture: tcpdump preview stdout: %w", err))
	}
	var decoderErr bytes.Buffer
	decoder.Stderr = &decoderErr
	if err := decoder.Start(); err != nil {
		return cleanup(fmt.Errorf("packet capture: start preview decoder: %w", err))
	}
	if err := producer.Start(); err != nil {
		_ = decoderIn.Close()
		_ = decoder.Wait()
		return cleanup(fmt.Errorf("packet capture: start tcpdump: %w", err))
	}

	startedAt := time.Now().UTC()
	result := Result{Path: path, StartedAt: startedAt, InterfaceName: req.Interface}
	var resultMu sync.Mutex
	snapshot := func() Result {
		resultMu.Lock()
		defer resultMu.Unlock()
		out := result
		out.LivePreview = append([]string(nil), result.LivePreview...)
		return out
	}
	var reportMu sync.Mutex
	lastReportedAt := time.Time{}
	reportProgress := func(force bool) {
		if report == nil {
			return
		}
		reportMu.Lock()
		if !force && time.Since(lastReportedAt) < 300*time.Millisecond {
			reportMu.Unlock()
			return
		}
		lastReportedAt = time.Now()
		reportMu.Unlock()
		report(snapshot())
	}
	reportProgress(true)

	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(io.MultiWriter(file, decoderIn), producerOut)
		if closeErr := decoderIn.Close(); closeErr != nil && copyErr == nil {
			copyErr = closeErr
		}
		copyDone <- copyErr
	}()
	previewDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(decoderOut)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			resultMu.Lock()
			result.Packets++
			result.LivePreview = append(result.LivePreview, line)
			if len(result.LivePreview) > previewLineLimit {
				result.LivePreview = append([]string(nil), result.LivePreview[len(result.LivePreview)-previewLineLimit:]...)
			}
			resultMu.Unlock()
			reportProgress(false)
		}
		previewDone <- scanner.Err()
	}()
	producerDone := make(chan error, 1)
	go func() { producerDone <- producer.Wait() }()

	deadline := time.NewTimer(req.Duration)
	defer deadline.Stop()
	statsTicker := time.NewTicker(200 * time.Millisecond)
	defer statsTicker.Stop()
	stopReason := ""
	var producerWaitErr error
	producerExited := false
	stopProducer := func(reason string) {
		if stopReason != "" {
			return
		}
		stopReason = reason
		if producer.Process != nil {
			_ = producer.Process.Signal(os.Interrupt)
		}
	}
	for !producerExited {
		select {
		case producerWaitErr = <-producerDone:
			producerExited = true
		case <-ctx.Done():
			stopProducer("cancelled")
		case <-deadline.C:
			stopProducer("duration_limit")
		case <-statsTicker.C:
			info, statErr := file.Stat()
			if statErr != nil {
				stopProducer("failed")
				producerWaitErr = fmt.Errorf("packet capture: stat pcap: %w", statErr)
				break
			}
			resultMu.Lock()
			result.PayloadBytes = info.Size()
			resultMu.Unlock()
			if info.Size() >= req.MaxBytes {
				stopProducer("byte_limit")
			}
			reportProgress(false)
		}
		if stopReason != "" && !producerExited {
			select {
			case producerWaitErr = <-producerDone:
				producerExited = true
			case <-time.After(3 * time.Second):
				if producer.Process != nil {
					_ = producer.Process.Kill()
				}
				producerWaitErr = <-producerDone
				producerExited = true
			}
		}
	}

	copyErr := <-copyDone
	if closeErr := file.Close(); closeErr != nil && copyErr == nil {
		copyErr = closeErr
	}
	fileClosed = true
	decoderWaitErr := decoder.Wait()
	previewErr := <-previewDone
	if stopReason == "" && producerWaitErr != nil {
		return cleanup(fmt.Errorf("packet capture: tcpdump failed: %w: %s", producerWaitErr, strings.TrimSpace(producerErr.String())))
	}
	if copyErr != nil {
		return cleanup(fmt.Errorf("packet capture: copy tcpdump stream: %w", copyErr))
	}
	if decoderWaitErr != nil || previewErr != nil {
		cause := decoderWaitErr
		if cause == nil {
			cause = previewErr
		}
		return cleanup(fmt.Errorf("packet capture: decode preview: %w: %s", cause, strings.TrimSpace(decoderErr.String())))
	}
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("packet capture: stat completed pcap: %w", err)
	}
	resultMu.Lock()
	result.PayloadBytes = info.Size()
	result.FileBytes = info.Size()
	result.FinishedAt = time.Now().UTC()
	if stopReason == "" {
		stopReason = "completed"
	}
	result.StopReason = stopReason
	resultMu.Unlock()
	reportProgress(true)
	return snapshot(), nil
}

func tcpdumpWriteArgs(req Request) []string {
	args := []string{"-U", "-n", "-i", req.Interface, "-s", strconv.Itoa(req.Snaplen), "-c", strconv.Itoa(req.MaxPackets), "-w", "-"}
	if !req.Promiscuous {
		args = append(args, "-p")
	}
	if req.Filter != "" {
		args = append(args, req.Filter)
	}
	return args
}

func normalizeRequest(in Request) (Request, error) {
	in.CaptureID = strings.TrimSpace(in.CaptureID)
	if !validCaptureID(in.CaptureID) {
		return Request{}, errors.New("packet capture: capture_id must be a UUID or lowercase identifier")
	}
	in.Interface = strings.TrimSpace(in.Interface)
	if in.Interface == "" || len(in.Interface) > 15 || strings.ContainsAny(in.Interface, "/\\\x00") {
		return Request{}, errors.New("packet capture: valid interface required")
	}
	in.NetworkNamespace = strings.TrimSpace(in.NetworkNamespace)
	if !validNetworkNamespace(in.NetworkNamespace) {
		return Request{}, errors.New("packet capture: invalid network namespace")
	}
	if in.Duration <= 0 {
		in.Duration = defaultDuration
	}
	if in.Duration > maxDuration {
		return Request{}, fmt.Errorf("packet capture: duration exceeds %s", maxDuration)
	}
	if in.MaxBytes <= 0 {
		in.MaxBytes = defaultMaxBytes
	}
	if in.MaxBytes > maxBytes {
		return Request{}, fmt.Errorf("packet capture: max_bytes exceeds %d", maxBytes)
	}
	if in.MaxPackets <= 0 {
		in.MaxPackets = defaultMaxPackets
	}
	if in.MaxPackets > maxPackets {
		return Request{}, fmt.Errorf("packet capture: max_packets exceeds %d", maxPackets)
	}
	if in.Snaplen <= 0 {
		in.Snaplen = defaultSnaplen
	}
	if in.Snaplen > maxSnaplen {
		return Request{}, fmt.Errorf("packet capture: snaplen exceeds %d", maxSnaplen)
	}
	filter, err := normalizeFilter(in.Filter)
	if err != nil {
		return Request{}, err
	}
	in.Filter = filter
	return in, nil
}

func validNetworkNamespace(namespace string) bool {
	if namespace == "" {
		return true
	}
	if len(namespace) > 128 {
		return false
	}
	for _, r := range namespace {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	return true
}

func validCaptureID(v string) bool {
	if len(v) < 8 || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// normalizeFilter preserves the prior, deliberately small BPF request
// surface. The normalized expression is passed to tcpdump as one argv value.
func normalizeFilter(raw string) (string, error) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(raw)))
	if len(fields) == 0 {
		return "", nil
	}
	if len(fields) == 3 && isFilterProtocol(fields[0]) && (fields[1] == "host" || fields[1] == "port") {
		fields = []string{fields[0], "and", fields[1], fields[2]}
	} else if len(fields) == 4 && fields[0] == "host" && fields[2] == "port" {
		fields = []string{"host", fields[1], "and", "port", fields[3]}
	}
	protocolSeen, hostSeen, portSeen := false, false, false
	for i := 0; i < len(fields); {
		if fields[i] == "and" {
			return "", errors.New("packet capture: invalid filter")
		}
		switch fields[i] {
		case "tcp", "udp", "icmp", "icmp6", "icmpv6":
			if protocolSeen {
				return "", errors.New("packet capture: filter accepts one protocol")
			}
			protocolSeen = true
			i++
		case "host":
			if hostSeen || i+1 >= len(fields) {
				return "", errors.New("packet capture: filter accepts one host")
			}
			if _, err := netip.ParseAddr(fields[i+1]); err != nil {
				return "", fmt.Errorf("packet capture: invalid host filter: %w", err)
			}
			hostSeen = true
			i += 2
		case "port":
			if portSeen || i+1 >= len(fields) {
				return "", errors.New("packet capture: filter accepts one port")
			}
			port, err := strconv.ParseUint(fields[i+1], 10, 16)
			if err != nil || port == 0 {
				return "", errors.New("packet capture: invalid port filter")
			}
			portSeen = true
			i += 2
		default:
			return "", fmt.Errorf("packet capture: unsupported filter %q; use tcp, udp, icmp, host <ip>, port <n>, joined with and", fields[i])
		}
		if i < len(fields) {
			if fields[i] != "and" {
				return "", errors.New("packet capture: invalid filter")
			}
			i++
			if i == len(fields) {
				return "", errors.New("packet capture: invalid filter")
			}
		}
	}
	return strings.Join(fields, " "), nil
}

func isFilterProtocol(value string) bool {
	return value == "tcp" || value == "udp" || value == "icmp" || value == "icmp6" || value == "icmpv6"
}
