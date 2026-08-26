// Package webshell turns the edge agent into a generic stream port-
// forwarder. Manager opens a frontier stream into the edge with a
// Meta blob describing the target (today: `{"target":"127.0.0.1:22"}`);
// the edge dials that local TCP socket and io.Copy's bytes both ways.
//
// SSH lives entirely on the manager side now: manager wraps the
// stream with ssh.NewClientConn, runs PTY + Shell, and pumps to the
// browser WebSocket. The edge has no SSH client, no pty management,
// no session map — it's a one-screen TCP forwarder. This keeps the
// edge tiny and lets the manager be the sole owner of webshell
// policy / audit / concurrency / kick-out logic.
//
// Wire shape (manager → edge stream Meta):
//
//	{"target": "127.0.0.1:22"}        # default WebSSH path
//	{"target": "10.0.0.5:22"}         # future: jumpbox / sidecar SSH
//
// Targets are validated against a small allowlist (just localhost +
// loopback ports today) so the manager can't aim the edge at
// arbitrary intranet hosts via a forged Meta. Phase 2 may widen this
// behind a per-edge config setting.
package webshell

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// Acceptor accepts inbound streams. *tunnel.Client satisfies it.
type Acceptor interface {
	AcceptStream() (tunnel.StreamConn, error)
}

// Register kicks off the AcceptStream loop in a goroutine. Each
// accepted stream is dispatched to a forwarder goroutine that lives
// for the duration of the stream. Returns immediately; stops only
// when AcceptStream returns a fatal error (tunnel torn down).
func Register(client Acceptor, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	go acceptLoop(client, log)
	log.Info("webshell: stream forwarder running")
}

func HandleStream(stream tunnel.StreamConn, log *slog.Logger) {
	handleStream(stream, log)
}

// acceptLoop pumps AcceptStream calls forever (until tunnel close).
// Each stream is handed to a separate goroutine — concurrent shells
// don't block one another.
func acceptLoop(client Acceptor, log *slog.Logger) {
	for {
		stream, err := client.AcceptStream()
		if err != nil {
			// Treat "not dialed" / EOF / closed as transient — wait
			// a beat and retry. The tunnel layer drives reconnect.
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "not dialed") || strings.Contains(err.Error(), "closed") {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			log.Warn("webshell: accept stream", slog.Any("err", err))
			time.Sleep(time.Second)
			continue
		}
		go handleStream(stream, log)
	}
}

// streamMeta is the JSON shape the manager puts in the stream's Meta
// blob. Keep field names stable; future fields (ttl, audit_id, ...)
// can be added without breaking older edges as long as we json.Decode
// with allow-unknown-fields semantics (default).
type streamMeta struct {
	Target string `json:"target"`
}

// allowedTargets restricts which addresses the edge will dial when
// the manager opens a stream. Today the only sane target is the
// host's local sshd; we hardcode 127.0.0.1:22. Phase 2 may widen
// behind /etc/ongrid-edge/webshell.yaml.
//
// The localhost narrow scope is the security boundary — without it
// a compromised manager could pivot the edge to any reachable IP.
var allowedTargets = map[string]bool{
	"127.0.0.1:22": true,
	"localhost:22": true,
}

func handleStream(stream tunnel.StreamConn, log *slog.Logger) {
	defer stream.Close()
	var m streamMeta
	if raw := stream.Meta(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			writeStreamError(stream, fmt.Sprintf("bad meta: %v", err))
			return
		}
	}
	target := strings.TrimSpace(m.Target)
	if target == "" {
		target = "127.0.0.1:22"
	}
	if !allowedTargets[target] {
		writeStreamError(stream, fmt.Sprintf("target %q not allowed", target))
		log.Warn("webshell: rejected target", slog.String("target", target))
		return
	}

	conn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		writeStreamError(stream, fmt.Sprintf("dial %s: %v", target, err))
		return
	}
	defer conn.Close()

	log.Info("webshell: forwarding", slog.String("target", target))

	// Bidirectional copy. First side to error closes the other.
	errs := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, stream)
		errs <- err
	}()
	go func() {
		_, err := io.Copy(stream, conn)
		errs <- err
	}()
	<-errs
	// Closing both ends releases the surviving io.Copy.
	_ = conn.Close()
	_ = stream.Close()
	<-errs
}

// writeStreamError sends a brief plain-text error to the stream so
// the manager-side ssh.NewClientConn fails with a useful message
// rather than a generic "EOF on protocol read".
func writeStreamError(s io.Writer, msg string) {
	_, _ = io.WriteString(s, "ongrid-edge webshell forwarder: "+msg+"\n")
}
