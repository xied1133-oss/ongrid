// ssh_identity.go — HTTP layer for /v1/knowledge/ssh-identities
//Mounted on the knowledge router so the "代码仓库" page
// has a single URL prefix to talk to (matches the singpchia ask
// "all git stuff on that one page").
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	biz "github.com/ongridio/ongrid/internal/manager/biz/knowledge"
	model "github.com/ongridio/ongrid/internal/manager/model/knowledge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// sshIdentityDTO is the public view of a stored identity. private_key
// and passphrase are never serialised — even admins only ever see the
// public key + fingerprint after creation.
type sshIdentityDTO struct {
	ID          uint64     `json:"id"`
	Name        string     `json:"name"`
	PublicKey   string     `json:"public_key"`
	Fingerprint string     `json:"fingerprint"`
	Hosts       []string   `json:"hosts"`
	KnownHosts  string     `json:"known_hosts,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toSSHIdentityDTO(row *model.SSHIdentity) sshIdentityDTO {
	hosts := []string{}
	if row.HostsJSON != "" {
		_ = json.Unmarshal([]byte(row.HostsJSON), &hosts)
	}
	return sshIdentityDTO{
		ID:          row.ID,
		Name:        row.Name,
		PublicKey:   row.PublicKey,
		Fingerprint: row.Fingerprint,
		Hosts:       hosts,
		KnownHosts:  row.KnownHosts,
		LastUsedAt:  row.LastUsedAt,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

type createSSHIdentityReq struct {
	Name       string   `json:"name"`
	PrivateKey string   `json:"private_key"`
	Hosts      []string `json:"hosts"`
	KnownHosts string   `json:"known_hosts,omitempty"`
}

type updateSSHIdentityReq struct {
	Name       string   `json:"name"`
	Hosts      []string `json:"hosts"`
	KnownHosts string   `json:"known_hosts"`
}

type generateSSHIdentityReq struct {
	Name       string   `json:"name"`
	Hosts      []string `json:"hosts"`
	KnownHosts string   `json:"known_hosts,omitempty"`
}

func (h *Handler) listSSHIdentities(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.ListSSHIdentities(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]sshIdentityDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSSHIdentityDTO(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

func (h *Handler) createSSHIdentity(w http.ResponseWriter, r *http.Request) {
	var req createSSHIdentityReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	row, err := h.svc.CreateSSHIdentity(r.Context(), biz.CreateSSHIdentityInput{
		Name:       req.Name,
		PrivateKey: req.PrivateKey,
		Hosts:      req.Hosts,
		KnownHosts: req.KnownHosts,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSSHIdentityDTO(row))
}

func (h *Handler) generateSSHIdentity(w http.ResponseWriter, r *http.Request) {
	var req generateSSHIdentityReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	row, err := h.svc.GenerateSSHIdentity(r.Context(), biz.GenerateSSHIdentityInput{
		Name:       req.Name,
		Hosts:      req.Hosts,
		KnownHosts: req.KnownHosts,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	// 201 with the public_key prominently in the response — the SPA
	// shows it in a copyable block immediately so the operator can
	// paste it into the host's Deploy keys list without an extra
	// round-trip.
	writeJSON(w, http.StatusCreated, toSSHIdentityDTO(row))
}

func (h *Handler) updateSSHIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(r, "id")
	if err != nil {
		writeErr(w, err)
		return
	}
	var req updateSSHIdentityReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	row, err := h.svc.UpdateSSHIdentity(r.Context(), id, biz.UpdateSSHIdentityInput{
		Name:       req.Name,
		Hosts:      req.Hosts,
		KnownHosts: req.KnownHosts,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSSHIdentityDTO(row))
}

func (h *Handler) deleteSSHIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(r, "id")
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.svc.DeleteSSHIdentity(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseUintParam(r *http.Request, key string) (uint64, error) {
	raw := chi.URLParam(r, key)
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, errors.Join(errs.ErrInvalid, err)
	}
	return n, nil
}

// Ensure context is imported even if no direct usage in this file's
// helpers — gives compiler stable surface across edits.
var _ = context.TODO
