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

// Added in the hajekt2/pdfcpu fork: oversized Size normalization regressions.

package pdfcpu

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func TestNoRepairOversizedXRefSize(t *testing.T) {
	classic := discardClassic(discardObjects("(en) /Absent 7 0 R"), " /Info 4 0 R", false, "")
	compressed := discardCompressed(discardObjects("(en) /Absent 7 0 R"), " /Info 4 0 R")
	incremental := discardIncremental(discardObjects("(en)"), map[int]string{4: "<< /Title (new) >>"}, "", " /Info 4 0 R", false)
	for _, tc := range []struct {
		name      string
		data      []byte
		effective int
	}{
		{"classic", bytes.ReplaceAll(classic, []byte("/Size 5"), []byte("/Size 8")), 5},
		{"xref stream with Index", bytes.Replace(compressed, []byte("/Size 7"), []byte("/Size 8 /Index [0 7]"), 1), 7},
		{"incremental", bytes.ReplaceAll(incremental, []byte("/Size 5"), []byte("/Size 8")), 5},
	} {
		for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
			t.Run(fmt.Sprintf("%s/mode=%d", tc.name, mode), func(t *testing.T) {
				legacy, err := noRepairRead(tc.data, false, mode)
				if err != nil {
					t.Fatal(err)
				}
				ctx, err := noRepairRead(tc.data, true, mode)
				if err != nil {
					t.Fatal(err)
				}
				want := model.NoRepairNormalizations{XRefSizeLowered: true, XRefSizeDeclared: 8, XRefSizeEffective: tc.effective}
				if got := ctx.NoRepairNormalizations(); got != want {
					t.Fatalf("normalizations = %+v; want %+v", got, want)
				}
				if got := legacy.NoRepairNormalizations(); got != (model.NoRepairNormalizations{}) {
					t.Fatalf("legacy normalizations = %+v", got)
				}
				if *ctx.Size != tc.effective || *legacy.Size != tc.effective || len(ctx.Table) != len(legacy.Table) {
					t.Fatal("Size normalization changed the selected table")
				}
				// Compare every selected object, a missing object below the new
				// bound, the new boundary, and one above the old boundary.
				for n := 0; n <= 9; n++ {
					ref := *types.NewIndirectRef(n, 0)
					got, err := ctx.Dereference(ref)
					if err != nil {
						t.Fatal(err)
					}
					old, err := legacy.Dereference(ref)
					same := got == nil && old == nil
					if got != nil && old != nil {
						same = got.PDFString() == old.PDFString()
					}
					if err != nil || !same {
						t.Fatalf("object %d differs: %v", n, err)
					}
				}
				// A snapshot cannot mutate the context, and the source xref
				// stream's dictionary retains its declared Size.
				facts := ctx.NoRepairNormalizations()
				facts.XRefSizeDeclared = 0
				facts.XRefSizeLowered = false
				if ctx.NoRepairNormalizations() != want {
					t.Fatal("snapshot changed the context record")
				}
				if tc.effective == 7 {
					stream := ctx.Table[5].Object.(types.ObjectStreamDict)
					oldStream := legacy.Table[5].Object.(types.ObjectStreamDict)
					if !bytes.Equal(stream.Raw, oldStream.Raw) || !bytes.Equal(stream.Content, oldStream.Content) {
						t.Fatal("Size normalization changed object stream data")
					}
					xsd := ctx.Table[6].Object.(types.XRefStreamDict)
					if *xsd.Dict.IntEntry("Size") != 8 {
						t.Fatal("source xref stream Size was changed")
					}
				}
			})
		}
	}
}

func TestNoRepairXRefSizeRefusedClasses(t *testing.T) {
	unreferenced := discardClassic(discardObjects("(en)"), "", false, "")
	referenced := discardClassic(discardObjects("(en)"), " /Info 4 0 R", false, "")
	compressed := discardCompressed(discardObjects("(en)"), "")
	freeOnly := bytes.Replace(unreferenced, []byte("trailer\n"), []byte("8 1\n0000000000 65535 f \ntrailer\n"), 1)
	for _, tc := range []struct {
		name string
		data []byte
		site model.RepairSite
	}{
		{"b referenced at Size", bytes.Replace(referenced, []byte("/Size 5"), []byte("/Size 4"), 1), model.RepairXRefSize},
		{"b referenced above Size", bytes.Replace(referenced, []byte("/Size 5"), []byte("/Size 3"), 1), model.RepairXRefSize},
		{"c unreferenced at Size", bytes.Replace(unreferenced, []byte("/Size 5"), []byte("/Size 4"), 1), model.RepairXRefSize},
		{"c extra free entry only", bytes.Replace(freeOnly, []byte("/Size 5"), []byte("/Size 8"), 1), model.RepairXRefSize},
		{"d zero", bytes.Replace(unreferenced, []byte("/Size 5"), []byte("/Size 0"), 1), model.RepairXRefSize},
		{"d negative", bytes.Replace(unreferenced, []byte("/Size 5"), []byte("/Size -1"), 1), model.RepairXRefSize},
		{"d Index exceeds Size", bytes.Replace(compressed, []byte("/Size 7"), []byte("/Size 6 /Index [0 7]"), 1), model.RepairXRefSize},
		{"d oversized implicit Index lacks records", bytes.Replace(compressed, []byte("/Size 7"), []byte("/Size 8"), 1), model.RepairXRefReconstruction},
	} {
		for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
			t.Run(fmt.Sprintf("%s/mode=%d", tc.name, mode), func(t *testing.T) {
				_, err := noRepairRead(tc.data, true, mode)
				requireRepairRefused(t, err, tc.site)
				// Relaxed legacy reading still recovers the Size corrections.
				if mode == model.ValidationRelaxed && tc.site == model.RepairXRefSize {
					if _, err := noRepairRead(tc.data, false, mode); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestNoRepairOversizedXRefSizeKeepsLazyPolicy(t *testing.T) {
	data := noRepairLazyPDF("<< /Value <zz> >>")
	data = bytes.Replace(data, []byte("/Size 7"), []byte("/Size 8 /Index [0 7]"), 1)
	ctx, err := noRepairRead(data, true, model.ValidationStrict)
	if err != nil {
		t.Fatal(err)
	}
	if !ctx.NoRepairNormalizations().XRefSizeLowered {
		t.Fatal("missing Size normalization")
	}
	_, err = ctx.Dereference(*types.NewIndirectRef(4, 0))
	requireRepairRefused(t, err, model.RepairParserHex)
}

func TestNoRepairXRefSizeRequiresCompleteSelectedBound(t *testing.T) {
	for _, highestFree := range []bool{false, true} {
		t.Run(fmt.Sprintf("highestFree=%t", highestFree), func(t *testing.T) {
			data := discardClassic(discardObjects("(en)"), "", false, "")
			ctx, err := noRepairRead(data, true, model.ValidationStrict)
			if err != nil {
				t.Fatal(err)
			}
			declared := 10
			ctx.Size = &declared
			ctx.Table[8] = &model.XRefTableEntry{Free: highestFree}
			// Even when all in-use entries fit, a present free entry must not
			// be excluded from the bound used to classify the declaration.
			err = ctx.NormalizeReadXRefSize(5)
			requireRepairRefused(t, err, model.RepairXRefSize)
			if *ctx.Size != 10 || ctx.NoRepairNormalizations() != (model.NoRepairNormalizations{}) {
				t.Fatal("refused correction changed Size or its facts")
			}
			if err := ctx.NormalizeReadXRefSize(9); err != nil {
				t.Fatal(err)
			}
			want := model.NoRepairNormalizations{XRefSizeLowered: true, XRefSizeDeclared: 10, XRefSizeEffective: 9}
			if ctx.NoRepairNormalizations() != want || !reflect.DeepEqual(ctx.Table[8], &model.XRefTableEntry{Free: highestFree}) {
				t.Fatal("allowed correction changed an entry or lost its facts")
			}
		})
	}
}

func TestNoRepairXRefSizeAndHeadFactsAccumulate(t *testing.T) {
	data := discardClassic(discardObjects("(en)"), "", false, "")
	data = bytes.Replace(data, []byte("xref\n0 5\n0000000000 65535 f \n"), []byte("xref\n1 4\n"), 1)
	data = bytes.Replace(data, []byte("/Size 5"), []byte("/Size 8"), 1)
	ctx, err := noRepairRead(data, true, model.ValidationStrict)
	if err != nil {
		t.Fatal(err)
	}
	want := model.NoRepairNormalizations{FreeListHeadSynthesized: true, XRefSizeLowered: true, XRefSizeDeclared: 8, XRefSizeEffective: 5}
	if got := ctx.NoRepairNormalizations(); got != want {
		t.Fatalf("normalizations = %+v; want %+v", got, want)
	}
}
