package store

import (
	"gorm.io/gorm"

	biz "github.com/ongridio/ongrid/internal/manager/biz/metric"
)

// NewBizWriter returns the biz.Writer interface value, used at wire time
// (cmd/ongrid) to construct the biz.Ingester without exposing the
// concrete *Writer type to the wiring layer.
func NewBizWriter(db *gorm.DB) biz.Writer { return NewWriter(db) }

// NewBizReader returns the biz.Reader interface value. Paired with
// NewBizWriter for the same assembly-time convenience.
func NewBizReader(db *gorm.DB) biz.Reader { return NewReader(db) }
