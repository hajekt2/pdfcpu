package model

import (
	"errors"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func TestInvalidXRefOffsetDereference(t *testing.T) {
	zero := int64(0)
	generation := 0
	x := &XRefTable{Table: map[int]*XRefTableEntry{
		1: {Offset: &zero, Generation: &generation},
		2: {Offset: &zero, Generation: &generation, Object: types.Integer(42)},
	}}
	if _, err := x.Dereference(*types.NewIndirectRef(1, 0)); !errors.Is(err, ErrInvalidXRefOffset) {
		t.Fatalf("bad entry: %v", err)
	}
	if o, err := x.Dereference(*types.NewIndirectRef(2, 0)); err != nil || o != types.Integer(42) {
		t.Fatalf("materialized entry: %v, %v", o, err)
	}
}
