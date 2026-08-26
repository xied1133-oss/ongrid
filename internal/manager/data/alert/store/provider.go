package store

import (
	"gorm.io/gorm"

	biz "github.com/ongridio/ongrid/internal/manager/biz/alert"
)

// NewBizRepo is the wire-ready constructor for biz/alert.Repo.
func NewBizRepo(db *gorm.DB) biz.Repo {
	return NewRepo(db)
}
