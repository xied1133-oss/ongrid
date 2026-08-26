// Package streamrouter owns the edge's single AcceptStream loop.
package streamrouter

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

type Acceptor interface {
	AcceptStream() (tunnel.StreamConn, error)
}

type Handler func(tunnel.StreamConn)

func Register(client Acceptor, handlers map[string]Handler, fallback Handler, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	go acceptLoop(client, handlers, fallback, log)
	log.Info("streamrouter: stream dispatcher running")
}

func acceptLoop(client Acceptor, handlers map[string]Handler, fallback Handler, log *slog.Logger) {
	for {
		stream, err := client.AcceptStream()
		if err != nil {
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "not dialed") || strings.Contains(err.Error(), "closed") {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			log.Warn("streamrouter: accept stream", slog.Any("err", err))
			time.Sleep(time.Second)
			continue
		}
		handler := fallback
		if kind := streamKind(stream.Meta()); kind != "" {
			if h, ok := handlers[kind]; ok {
				handler = h
			}
		}
		if handler == nil {
			if err := stream.Close(); err != nil {
				log.Debug("streamrouter: close unroutable stream", slog.Any("err", err))
			}
			continue
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error("streamrouter: handler panic",
						slog.Any("panic", r),
						slog.String("stack", string(debug.Stack())),
					)
				}
			}()
			handler(stream)
		}()
	}
}

func streamKind(meta []byte) string {
	if len(meta) == 0 {
		return ""
	}
	var m struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(meta, &m); err != nil {
		return ""
	}
	return strings.TrimSpace(m.Kind)
}
