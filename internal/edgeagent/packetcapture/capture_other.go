//go:build !linux

package packetcapture

import (
	"context"
	"errors"
	"time"
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

type Capturer struct{}

type Task struct {
	ID         string     `json:"id"`
	Request    Request    `json:"request"`
	State      string     `json:"state"`
	Result     Result     `json:"result,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type RawObject struct {
	Data      []byte
	SizeBytes uint64
	SHA256Hex string
}

type Service struct{}

func New(string) (*Capturer, error) {
	return nil, errors.New("packet capture: supported on linux only")
}

func (*Capturer) Capture(context.Context, Request) (Result, error) {
	return Result{}, errors.New("packet capture: supported on linux only")
}

func (*Capturer) CaptureWithProgress(context.Context, Request, ProgressReporter) (Result, error) {
	return Result{}, errors.New("packet capture: supported on linux only")
}

func NewService(*Capturer) (*Service, error) {
	return nil, errors.New("packet capture: supported on linux only")
}

func (*Service) Start(Request) (Task, error) {
	return Task{}, errors.New("packet capture: supported on linux only")
}

func (*Service) Get(string) (Task, bool) { return Task{}, false }

func (*Service) Cancel(string) (Task, error) {
	return Task{}, errors.New("packet capture: supported on linux only")
}

func (*Service) Stop(string) (Task, error) {
	return Task{}, errors.New("packet capture: supported on linux only")
}

func (*Service) Read(string, uint64) (RawObject, error) {
	return RawObject{}, errors.New("packet capture: supported on linux only")
}
