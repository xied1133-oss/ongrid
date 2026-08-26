// orgs.go holds the Phase-1 enterprise endpoints — org CRUD + member
// management + the enriched /v1/me. These methods receiver against the
// same Handler defined in http.go; split into a separate file so the
// org/user surface area stays diff-friendly.
package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ongridio/ongrid/internal/iam/biz/user"
	"github.com/ongridio/ongrid/internal/iam/biz/org"
	"github.com/ongridio/ongrid/internal/iam/model"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// ----- DTOs -----

type orgDTO struct {
	ID          uint64    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	// ParentID nil → 顶级 org。前端用它在 list 之上构建树。
	ParentID    *uint64   `json:"parent_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type orgListResp struct {
	Items []orgDTO `json:"items"`
	Total int      `json:"total"`
}

type membershipDTO struct {
	UserID      uint64 `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role"`
}

type membershipListResp struct {
	Items []membershipDTO `json:"items"`
	Total int             `json:"total"`
}

type orgMembershipForUserDTO struct {
	OrgID   uint64 `json:"org_id"`
	OrgName string `json:"org_name"`
	Role    string `json:"role"`
}

// meDTO / fullUserDTO — the privilege tier is `role` (admin|user) only
// since May 2026. Legacy is_superuser flag dropped from the API; admin
// = full system privileges. DB column kept until a follow-up migration.
type meDTO struct {
	ID          uint64                    `json:"id"`
	Email       string                    `json:"email"`
	DisplayName string                    `json:"display_name,omitempty"`
	Phone       string                    `json:"phone,omitempty"`
	Role        string                    `json:"role"`
	Status      string                    `json:"status"`
	Memberships []orgMembershipForUserDTO `json:"memberships"`
}

type fullUserDTO struct {
	ID          uint64    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name,omitempty"`
	Phone       string    `json:"phone,omitempty"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type createOrgReq struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	ParentID    *uint64 `json:"parent_id,omitempty"`
}

// updateOrgReq — parent_id 缺省时不动；显式传 null 提到顶级；传数字移到那个 org 之下。
// "parent_id_set" 是前端层面的 "用户确实想改 parent" 信号，避免 JSON null 与
// "字段没传" 被同一种 zero-value 表示的歧义。
type updateOrgReq struct {
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	ParentIDSet  bool    `json:"parent_id_set,omitempty"`
	ParentID     *uint64 `json:"parent_id,omitempty"`
}

type addMemberReq struct {
	UserID uint64 `json:"user_id"`
	Role   string `json:"role"`
}

type updateMemberReq struct {
	Role string `json:"role"`
}

type createUserReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Phone       string `json:"phone"`
	Role        string `json:"role"`
	// SkipDefaultOrg lets the caller opt out of the auto-join behavior.
	// Default is to add the new user as `member` of "默认组织" so they
	// can do something useful immediately — the casbin policy denies
	// every action when a user has no membership.
	SkipDefaultOrg bool `json:"skip_default_org"`
}

type updateUserReq struct {
	DisplayName *string `json:"display_name,omitempty"`
	Phone       *string `json:"phone,omitempty"`
	Status      *string `json:"status,omitempty"`
}

type resetPasswordReq struct {
	Password string `json:"password"`
}

// ----- helpers -----

func toOrgDTO(o *model.Org) orgDTO {
	return orgDTO{
		ID:          o.ID,
		Name:        o.Name,
		Description: o.Description,
		ParentID:    o.ParentID,
		CreatedAt:   o.CreatedAt,
		UpdatedAt:   o.UpdatedAt,
	}
}

func toFullUserDTO(u *model.User) fullUserDTO {
	return fullUserDTO{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Phone:       u.Phone,
		Role:        u.Role,
		Status:      u.Status,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}

func parseUserParam(r *http.Request) (uint64, error) {
	raw := chi.URLParam(r, "user_id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, errors.Join(errs.ErrInvalid, err)
	}
	return id, nil
}

// requireOrgsService 404s when the org service isn't wired yet (legacy
// kernels that boot without iam-Phase-1 will skip these routes).
func (h *Handler) requireOrgsService(w http.ResponseWriter) *org.Service {
	svc := h.svc.Orgs()
	if svc == nil {
		writeErr(w, errs.ErrNotWiredYet)
	}
	return svc
}

// ----- /v1/me -----

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	u, err := h.svc.GetByID(r.Context(), t.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := meDTO{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Phone:       u.Phone,
		Role:        u.Role,
		Status:      u.Status,
		Memberships: []orgMembershipForUserDTO{},
	}
	if rows, err := h.svc.MembershipsByUser(r.Context(), t.UserID); err == nil {
		for _, m := range rows {
			out.Memberships = append(out.Memberships, orgMembershipForUserDTO{
				OrgID:   m.OrgID,
				OrgName: m.Org.Name,
				Role:    m.Role,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ----- /v1/orgs -----

func (h *Handler) listOrgs(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	svc := h.requireOrgsService(w)
	if svc == nil {
		return
	}
	rows, err := svc.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]orgDTO, 0, len(rows))
	for _, o := range rows {
		out = append(out, toOrgDTO(o))
	}
	writeJSON(w, http.StatusOK, orgListResp{Items: out, Total: len(out)})
}

func (h *Handler) createOrg(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	svc := h.requireOrgsService(w)
	if svc == nil {
		return
	}
	var in createOrgReq
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	o, err := svc.Create(r.Context(), org.CreateInput{Name: in.Name, Description: in.Description, ParentID: in.ParentID})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toOrgDTO(o))
}

func (h *Handler) updateOrg(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	svc := h.requireOrgsService(w)
	if svc == nil {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in updateOrgReq
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	o, err := svc.Update(r.Context(), id, org.UpdateInput{
		Name:        in.Name,
		Description: in.Description,
		SetParent:   in.ParentIDSet,
		ParentID:    in.ParentID,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrgDTO(o))
}

func (h *Handler) deleteOrg(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	svc := h.requireOrgsService(w)
	if svc == nil {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := svc.Delete(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- /v1/orgs/{id}/members -----

func (h *Handler) listOrgMembers(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	ms := h.svc.Memberships()
	if ms == nil {
		writeErr(w, errs.ErrNotWiredYet)
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := ms.ListByOrg(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]membershipDTO, 0, len(rows))
	for _, m := range rows {
		out = append(out, membershipDTO{
			UserID:      m.UserID,
			Email:       m.User.Email,
			DisplayName: m.User.DisplayName,
			Role:        m.Role,
		})
	}
	writeJSON(w, http.StatusOK, membershipListResp{Items: out, Total: len(out)})
}

func (h *Handler) addOrgMember(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ms := h.svc.Memberships()
	if ms == nil {
		writeErr(w, errs.ErrNotWiredYet)
		return
	}
	orgID, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in addMemberReq
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if in.UserID == 0 {
		writeErr(w, errs.ErrInvalid)
		return
	}
	row, err := ms.AddOrUpdate(r.Context(), in.UserID, orgID, in.Role)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user_id": row.UserID,
		"org_id":  row.OrgID,
		"role":    row.Role,
	})
}

func (h *Handler) updateOrgMember(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ms := h.svc.Memberships()
	if ms == nil {
		writeErr(w, errs.ErrNotWiredYet)
		return
	}
	orgID, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	uid, err := parseUserParam(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in updateMemberReq
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := ms.AddOrUpdate(r.Context(), uid, orgID, in.Role); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) removeOrgMember(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ms := h.svc.Memberships()
	if ms == nil {
		writeErr(w, errs.ErrNotWiredYet)
		return
	}
	orgID, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	uid, err := parseUserParam(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := ms.Remove(r.Context(), uid, orgID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- /v1/users (extended) -----

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var in createUserReq
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	u, err := h.svc.User().Create(r.Context(), user.CreateInput{
		Email:       in.Email,
		Password:    in.Password,
		DisplayName: in.DisplayName,
		Phone:       in.Phone,
		Role:        in.Role,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	// Auto-join "默认组织" as member so the new user has something to
	// access immediately. Skip when the caller explicitly opts out (e.g.
	// staged onboarding flows that add to a specific team next). Best-
	// effort: a missing default org or a casbin hiccup is logged but
	// doesn't fail the user creation — admin can fix membership later.
	if !in.SkipDefaultOrg {
		if orgs := h.svc.Orgs(); orgs != nil {
			ms := h.svc.Memberships()
			if seed, err := orgs.EnsureSeed(r.Context(), "默认组织", ""); err == nil && seed != nil && ms != nil {
				if _, mErr := ms.AddOrUpdate(r.Context(), u.ID, seed.ID, "member"); mErr != nil {
					h.log.Warn("iam: auto-join default org",
						"user_id", u.ID,
						"err", mErr)
				}
			}
		}
	}
	writeJSON(w, http.StatusCreated, toFullUserDTO(u))
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in updateUserReq
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	u, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if in.DisplayName != nil || in.Phone != nil {
		dn, ph := u.DisplayName, u.Phone
		if in.DisplayName != nil {
			dn = *in.DisplayName
		}
		if in.Phone != nil {
			ph = *in.Phone
		}
		if err := h.svc.User().UpdateProfile(r.Context(), id, dn, ph); err != nil {
			writeErr(w, err)
			return
		}
	}
	if in.Status != nil {
		if err := h.svc.User().SetStatus(r.Context(), id, *in.Status); err != nil {
			writeErr(w, err)
			return
		}
	}
	final, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toFullUserDTO(final))
}

// setSuperuser handler was removed May 2026. Privilege model is now a
// single tier — role=admin gets full system access. Keeping the
// function reference here as a marker would mislead readers; the
// route registration is also gone (see http.go).

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in resetPasswordReq
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.svc.User().ResetPassword(r.Context(), id, in.Password); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
