/*
Copyright 2026 The pdfcpu Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Added in the hajekt2/pdfcpu fork: opt-in refusal of reader repairs.

package model

import (
	"context"
	"errors"
	"fmt"
)

// RepairSite identifies a reader recovery operation. Zero is reserved.
// The defined constants are the complete site vocabulary; no PDF content is stored.
type RepairSite uint8

const (
	RepairXRefReconstruction RepairSite = iota + 1
	RepairXRefSameLine
	RepairXRefShift
	RepairXRefEntry
	RepairXRefGeneration
	RepairXRefSize
	RepairXRefCycle
	RepairEOF
	RepairHeader
	RepairTrailerID
	RepairFilterName
	RepairFilterPipeline
	RepairStreamScan
	RepairStreamLength
	RepairStreamMarker
	RepairPlaintextMetadata
	RepairParserHex
	RepairParserToken
	RepairParserInteger
	RepairParserFallback
	RepairParserKeyword
	RepairParserDuplicateKey
	RepairParserNumber
	RepairObjectHeader
	RepairObjectNull
	RepairObjectSkipped
	RepairFreeHead
	RepairFreeGeneration // Reserved: object-0 generation normalization is now allowed.
	RepairFreeLink
	RepairRootVersion
	RepairEncryption
	RepairFilterData
	RepairObjectStreamIndex
)

// RepairRefusedError reports a refused repair without retaining source content.
// Object is -1 when unknown, 0 for a classic trailer or the free-list head.
// Offset is an absolute file byte offset, or -1 when unavailable (including
// decoded object-stream members). Site identifies the first encountered repair.
type RepairRefusedError struct {
	Site   RepairSite
	Object int
	Offset int64
}

// NoRepairNormalizations records the permitted reader normalizations during a
// NoRepair read. These facts contain no source content and do not certify later
// validation, optimization or writing.
type NoRepairNormalizations struct {
	FreeListHeadSynthesized     bool
	FreeListHeadGenerationFixed bool
	XRefSizeLowered             bool
	XRefSizeDeclared            int
	XRefSizeEffective           int
}

// NoRepairNormalizations returns a read-only snapshot of this table's facts.
// Changing the returned value cannot change the context's record.
func (xRefTable *XRefTable) NoRepairNormalizations() NoRepairNormalizations {
	return xRefTable.noRepairNormalizations
}

// NormalizeReadXRefSize applies the final reader Size correction. NoRepair
// permits only lowering an oversized declaration to one past the highest
// selected entry, including free and null entries (ISO 32000-1 7.5.5).
// It changes no entries or reference results and records only numeric facts.
func (xRefTable *XRefTable) NormalizeReadXRefSize(size int) error {
	if xRefTable.Size != nil && *xRefTable.Size == size {
		return nil
	}
	if xRefTable.RepairsForbidden() {
		highest := -1
		for n := range xRefTable.Table {
			if n > highest {
				highest = n
			}
		}
		if xRefTable.Size == nil || size <= 0 || *xRefTable.Size <= size || highest != size-1 {
			return xRefTable.RefuseRepair(RepairXRefSize, -1, -1)
		}
		xRefTable.noRepairNormalizations.XRefSizeLowered = true
		xRefTable.noRepairNormalizations.XRefSizeDeclared = *xRefTable.Size
		xRefTable.noRepairNormalizations.XRefSizeEffective = size
	}
	xRefTable.Size = &size
	return nil
}

func (e *RepairRefusedError) Error() string {
	return fmt.Sprintf("pdfcpu: repair refused (site=%d object=%d offset=%d)", e.Site, e.Object, e.Offset)
}

// IsRepairRefused reports whether err wraps a refused reader repair.
func IsRepairRefused(err error) bool {
	var refused *RepairRefusedError
	return errors.As(err, &refused)
}

// RepairsForbidden returns the policy captured when this table was created.
// Changing the caller's Configuration cannot relax a returned reader's policy.
func (xRefTable *XRefTable) RepairsForbidden() bool {
	return xRefTable != nil && xRefTable.noRepair
}

// RefuseRepair must be called before a recovery attempt or a source correction.
func (xRefTable *XRefTable) RefuseRepair(site RepairSite, object int, offset int64) error {
	if xRefTable.RepairsForbidden() {
		return &RepairRefusedError{Site: site, Object: object, Offset: offset}
	}
	return nil
}

type repairScopeKey struct{}
type repairScope struct {
	object int
	offset int64
}

// WithRepairScope carries a frozen refusal policy and numeric source location
// through recursive parsing and lazy decoding. It allocates nothing when disabled.
func WithRepairScope(c context.Context, enabled bool, object int, offset int64) context.Context {
	if !enabled {
		return c
	}
	return context.WithValue(c, repairScopeKey{}, repairScope{object, offset})
}

// RepairsForbidden reports whether parsing under c must refuse recovery.
func RepairsForbidden(c context.Context) bool {
	if c == nil {
		return false
	}
	_, ok := c.Value(repairScopeKey{}).(repairScope)
	return ok
}

// RefuseRepair checks the parser's policy before performing a correction.
func RefuseRepair(c context.Context, site RepairSite) error {
	if c != nil {
		if s, ok := c.Value(repairScopeKey{}).(repairScope); ok {
			return &RepairRefusedError{Site: site, Object: s.object, Offset: s.offset}
		}
	}
	return nil
}
