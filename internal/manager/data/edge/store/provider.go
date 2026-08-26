package store

import (
	"gorm.io/gorm"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
)

// NewBizRepo is the wire-ready constructor. cmd/ongrid binds this at
// assembly time to produce the biz.Repo value that Usecase + Authenticator
// consume. The bare NewRepo in edge.go returns the concrete *Repo (for
// test introspection); this helper returns the interface to keep the wiring
// layer free of the concrete type.
func NewBizRepo(db *gorm.DB) biz.Repo {
	return NewRepo(db)
}
