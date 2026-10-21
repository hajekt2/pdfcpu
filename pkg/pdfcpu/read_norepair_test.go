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

package pdfcpu

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func noRepairRead(data []byte, enabled bool, mode int) (*model.Context, error) {
	conf := model.NewStatelessConfiguration()
	conf.NoRepair, conf.ValidationMode = enabled, mode
	return Read(context.Background(), bytes.NewReader(data), conf)
}

func requireRepairRefused(t *testing.T, err error, site model.RepairSite) *model.RepairRefusedError {
	t.Helper()
	var refused *model.RepairRefusedError
	if !errors.As(err, &refused) || refused.Site != site {
		t.Fatalf("error = %v; want RepairRefusedError site %d", err, site)
	}
	return refused
}

func noRepairStream(stream string) []byte {
	objects := discardObjects("(en)")
	objects[2] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R >>"
	objects[3] = stream
	return discardClassic(objects, "", false, "")
}

// Ported from uacheck/internal/pdf/recovery_proof_test.go at origin/main.
// The control is clean. The other two load in strict mode with no old recovery signal.
func TestNoRepairUACheckRegressions(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		omitHead     bool
		site         model.RepairSite
		length       int64
	}{
		{"control", "<<  /Length 4 >>\nstream\nq Q\nendstream", false, 0, 4},
		{"stream length repaired", "<< /Length 0 >>\nstream\nq Q\nendstream", false, model.RepairStreamScan, 3},
		{"free head synthesized", "<<  /Length 4 >>\nstream\nq Q\nendstream", true, 0, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := discardClassic([]string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
				"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R >>",
				tc.stream,
			}, "", false, "")
			if tc.omitHead {
				data = bytes.Replace(data, []byte("xref\n0 5\n0000000000 65535 f \n"), []byte("xref\n1 4\n"), 1)
			}
			legacy, err := noRepairRead(data, false, model.ValidationStrict)
			if err != nil {
				t.Fatal(err)
			}
			sd := legacy.Table[4].Object.(types.StreamDict)
			if sd.StreamLength == nil || *sd.StreamLength != tc.length || legacy.Read.RepairOffset != 0 || !legacy.ValidationReport().Empty() {
				t.Fatal("legacy regression changed")
			}
			ctx, err := noRepairRead(data, true, model.ValidationStrict)
			if tc.site == 0 {
				if err != nil {
					t.Fatal(err)
				}
				want := model.NoRepairNormalizations{FreeListHeadSynthesized: tc.omitHead}
				if got := ctx.NoRepairNormalizations(); got != want {
					t.Fatalf("normalizations = %+v; want %+v", got, want)
				}
				if got := legacy.NoRepairNormalizations(); got != (model.NoRepairNormalizations{}) {
					t.Fatalf("legacy normalizations = %+v", got)
				}
				return
			}
			requireRepairRefused(t, err, tc.site)
		})
	}
}

func TestNoRepairFreeListHeadNormalizations(t *testing.T) {
	clean := discardClassic(discardObjects("(en)"), "", false, "")
	for _, tc := range []struct {
		name string
		data []byte
		want model.NoRepairNormalizations
	}{
		{"clean", clean, model.NoRepairNormalizations{}},
		{"missing head", bytes.Replace(clean, []byte("xref\n0 5\n0000000000 65535 f \n"), []byte("xref\n1 4\n"), 1), model.NoRepairNormalizations{FreeListHeadSynthesized: true}},
		{"wrong generation", bytes.Replace(clean, []byte("0000000000 65535 f"), []byte("0000000000 00000 f"), 1), model.NoRepairNormalizations{FreeListHeadGenerationFixed: true}},
	} {
		for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/mode=%d/NoRepair=%t", tc.name, mode, enabled), func(t *testing.T) {
					t.Parallel()
					ctx, err := noRepairRead(tc.data, enabled, mode)
					if err != nil {
						t.Fatal(err)
					}
					want := tc.want
					if !enabled {
						want = model.NoRepairNormalizations{}
					}
					if got := ctx.NoRepairNormalizations(); got != want {
						t.Fatalf("normalizations = %+v; want %+v", got, want)
					}
					head := ctx.Table[0]
					if !head.Free || *head.Generation != types.FreeHeadGeneration || *head.Offset != 0 {
						t.Fatalf("head = %+v", head)
					}
					// The accessor returns a copy; repeated checks retain the facts.
					snapshot := ctx.NoRepairNormalizations()
					snapshot.FreeListHeadSynthesized = !snapshot.FreeListHeadSynthesized
					if err := ctx.EnsureValidFreeList(); err != nil {
						t.Fatal(err)
					}
					if ctx.NoRepairNormalizations() != want {
						t.Fatal("normalization facts changed")
					}
				})
			}
		}
	}
}

func TestNoRepairFreeListNormalizationDoesNotAllowLinkRepair(t *testing.T) {
	ctx, err := noRepairRead(discardClassic(discardObjects("(en)"), "", false, ""), true, model.ValidationStrict)
	if err != nil {
		t.Fatal(err)
	}
	delete(ctx.Table, 0)
	if err := ctx.EnsureValidFreeList(); err != nil {
		t.Fatal(err)
	}
	if got := ctx.NoRepairNormalizations(); got != (model.NoRepairNormalizations{FreeListHeadSynthesized: true}) {
		t.Fatalf("synthesis facts = %+v", got)
	}
	*ctx.Table[0].Generation = 0
	link := int64(99)
	ctx.Table[0].Offset = &link
	requireRepairRefused(t, ctx.EnsureValidFreeList(), model.RepairFreeLink)
	if *ctx.Table[0].Generation != types.FreeHeadGeneration || *ctx.Table[0].Offset != 99 {
		t.Fatal("generation normalization allowed a link correction")
	}
	want := model.NoRepairNormalizations{FreeListHeadSynthesized: true, FreeListHeadGenerationFixed: true}
	if got := ctx.NoRepairNormalizations(); got != want {
		t.Fatalf("accumulated facts = %+v; want %+v", got, want)
	}
}

func TestNoRepairOtherFreeListRepairs(t *testing.T) {
	entry := func(free bool, next int64, generation int) *model.XRefTableEntry {
		return &model.XRefTableEntry{Free: free, Offset: &next, Generation: &generation}
	}
	for _, tc := range []struct {
		name               string
		headNext, freeNext int64
		freeGeneration     int
		hasFree, inUseHead bool
	}{
		{name: "reset nonzero head", headNext: 99},
		{name: "truncate broken link", headNext: 4, freeNext: 99, hasFree: true},
		{name: "truncate cycle", headNext: 4, freeNext: 4, hasFree: true},
		{name: "choose replacement link", headNext: 99, hasFree: true},
		{name: "link omitted entry", hasFree: true},
		{name: "reset permanently deleted link", hasFree: true, freeNext: 99, freeGeneration: types.FreeHeadGeneration},
		{name: "in-use head", inUseHead: true},
	} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/NoRepair=%t", tc.name, enabled), func(t *testing.T) {
				ctx, err := noRepairRead(discardClassic(discardObjects("(en)"), "", false, ""), enabled, model.ValidationStrict)
				if err != nil {
					t.Fatal(err)
				}
				ctx.Table = map[int]*model.XRefTableEntry{0: entry(!tc.inUseHead, tc.headNext, types.FreeHeadGeneration)}
				if tc.hasFree {
					ctx.Table[4] = entry(true, tc.freeNext, tc.freeGeneration)
				}
				err = ctx.EnsureValidFreeList()
				if !enabled {
					if err != nil {
						t.Fatalf("legacy free-list handling: %v", err)
					}
					return
				}
				site := model.RepairFreeLink
				if tc.inUseHead {
					site = model.RepairFreeHead
				}
				requireRepairRefused(t, err, site)
				if *ctx.Table[0].Offset != tc.headNext || ctx.Table[0].Free == tc.inUseHead ||
					(tc.hasFree && *ctx.Table[4].Offset != tc.freeNext) {
					t.Fatal("free-list entries changed before refusal")
				}
				if ctx.NoRepairNormalizations() != (model.NoRepairNormalizations{}) {
					t.Fatal("refused repair recorded as normalization")
				}
			})
		}
	}
}

func TestNoRepairFreeListShiftStillRefused(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		ctx, err := noRepairRead(discardClassic(discardObjects("(en)"), "", false, ""), enabled, model.ValidationStrict)
		if err != nil {
			t.Fatal(err)
		}
		delete(ctx.Table, 0)
		head := model.NewFreeHeadXRefTableEntry()
		ctx.Table[1] = head
		before := ctx.Table[2]
		err = postProcess(ctx, 1)
		if enabled {
			requireRepairRefused(t, err, model.RepairFreeHead)
			if ctx.Table[0] != nil || ctx.Table[1] != head || ctx.Table[2] != before {
				t.Fatal("objects shifted before refusal")
			}
		} else if err != nil || ctx.Table[0] != head || ctx.Table[1] != before {
			t.Fatalf("legacy shift changed: %v", err)
		}
		if ctx.NoRepairNormalizations() != (model.NoRepairNormalizations{}) {
			t.Fatal("shift recorded as normalization")
		}
	}
}

func TestNoRepairRecoveryFamilies(t *testing.T) {
	clean := discardClassic(discardObjects("(en)"), "", false, "")
	replace := func(old, new string) []byte { return bytes.Replace(clean, []byte(old), []byte(new), 1) }
	catalog := func(lang string) []byte { return discardClassic(discardObjects(lang), "", false, "") }
	shifted := func() []byte {
		i := bytes.LastIndex(clean, []byte("startxref\n")) + 10
		j := bytes.IndexByte(clean[i:], '\n') + i
		n, _ := strconv.Atoi(string(clean[i:j]))
		return bytes.Replace(clean, clean[i:j], []byte(strconv.Itoa(n-7)), 1)
	}
	for _, tc := range []struct {
		name string
		data []byte
		site model.RepairSite
	}{
		{"reconstruction strict", discardIncremental(discardObjects("(en)"), map[int]string{4: "<< /Title (new) >>"}, "", "", true), model.RepairXRefReconstruction},
		{"same line xref", replace("xref\n0 5", "xref 0 5"), model.RepairXRefSameLine},
		{"shifted xref", shifted(), model.RepairXRefShift},
		{"offset prefix", replace("0000000009 00000", "x000000009 00000"), model.RepairXRefEntry},
		{"generation substitution", replace("0000000009 00000", "0000000009 000000"), model.RepairXRefGeneration},
		{"in use zero", replace("0000000000 65535 f", "0000000000 65535 n"), model.RepairXRefEntry},
		{"truncated EOF", replace("%%EOF", "%%EO"), model.RepairEOF},
		{"trailing epilog", append(bytes.Clone(clean), []byte("junk")...), model.RepairEOF},
		{"trailer Size", replace("/Size 5", "/Size 0"), model.RepairXRefSize},
		{"trailer Size too small", replace("/Size 5", "/Size 4"), model.RepairXRefSize},
		{"ID duplication", discardClassic(discardObjects("(en)"), " /ID [<0102>]", false, ""), model.RepairTrailerID},
		{"filter alias", noRepairStream("<< /Length 3 /Filter /AHx >>\nstream\n71>\nendstream"), model.RepairFilterName},
		{"filter pipeline alias", noRepairStream("<< /Length 3 /Filter [/AHx] >>\nstream\n71>\nendstream"), model.RepairFilterPipeline},
		{"missing Length", noRepairStream("<< >>\nstream\nq Q\nendstream"), model.RepairStreamLength},
		{"oversized Length", noRepairStream("<< /Length 5000 >>\nstream\nq Q\nendstream"), model.RepairStreamLength},
		{"stream marker spaces", noRepairStream("<< /Length 4 >>\nstream \nq Q\nendstream"), model.RepairStreamMarker},
		{"invalid hex", catalog("<zz>"), model.RepairParserHex},
		{"bare token", catalog("foo"), model.RepairParserToken},
		{"integer range", catalog("999999999999999999999999999999999999"), model.RepairParserInteger},
		{"junk key fallback", malformedCatalogKeyPDF(t), model.RepairParserFallback},
		{"keyword case", catalog("TRUE"), model.RepairParserKeyword},
		{"duplicate key", catalog("(en) /Lang (fr)"), model.RepairParserDuplicateKey},
		{"numeric prefix", catalog("0+3"), model.RepairParserNumber},
		{"comma decimal", catalog("1,2"), model.RepairParserNumber},
		{"misplaced decimal sign", catalog(".-5"), model.RepairParserNumber},
		{"object header identity", replace("1 0 obj", "8 0 obj"), model.RepairObjectHeader},
		{"object header junk", replace("1 0 obj", "x1 0 bj"), model.RepairObjectHeader},
		{"empty object", discardClassic([]string{discardObjects("(en)")[0], discardObjects("(en)")[1], discardObjects("(en)")[2], ""}, "", false, ""), model.RepairObjectNull},
		{"free link", replace("0000000000 65535 f", "0000000999 65535 f"), model.RepairFreeLink},
		{"header offset", append([]byte("junk\n"), clean...), model.RepairHeader},
		{"root version", catalog("(en) /Version /1.8"), model.RepairRootVersion},
		{"xref Index Size", bytes.Replace(discardCompressed(discardObjects("(en)"), ""), []byte("/Size 7"), []byte("/Size 6 /Index [0 7]"), 1), model.RepairXRefSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := noRepairRead(tc.data, false, model.ValidationRelaxed)
			if err != nil {
				t.Fatalf("legacy read: %v", err)
			}
			_, err = noRepairRead(tc.data, true, model.ValidationRelaxed)
			requireRepairRefused(t, err, tc.site)
		})
	}
}

// Object 4 stays lazy after the catalog and page tree have loaded.
func noRepairLazyPDF(value string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offsets := map[int]int{}
	for i, o := range discardObjects("(en)")[:3] {
		offsets[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	content := "4 0 " + value
	offsets[5] = b.Len()
	fmt.Fprintf(&b, "5 0 obj\n<< /Type /ObjStm /N 1 /First 4 /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content)
	offsets[6] = b.Len()
	var records bytes.Buffer
	for id := 0; id <= 6; id++ {
		r := [7]byte{1}
		binary.BigEndian.PutUint32(r[1:5], uint32(offsets[id]))
		if id == 0 {
			r = [7]byte{0, 0, 0, 0, 0, 255, 255}
		}
		if id == 4 {
			r = [7]byte{2, 0, 0, 0, 5, 0, 0}
		}
		records.Write(r[:])
	}
	fmt.Fprintf(&b, "6 0 obj\n<< /Type /XRef /Size 7 /Root 1 0 R /W [1 4 2] /Length %d >>\nstream\n%s\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", records.Len(), records.Bytes(), offsets[6])
	return b.Bytes()
}

func TestNoRepairLazyAndConfigurationLifetime(t *testing.T) {
	data := noRepairLazyPDF("<< /Value <zz> >>")
	conf := model.NewStatelessConfiguration()
	conf.NoRepair = true
	ctx, err := Read(t.Context(), bytes.NewReader(data), conf)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ctx.Table[4].Object.(types.LazyObjectStreamObject); !ok {
		t.Fatal("member is no longer lazy")
	}
	conf.NoRepair = false
	_, err = ctx.Dereference(*types.NewIndirectRef(4, 0))
	refused := requireRepairRefused(t, err, model.RepairParserHex)
	if refused.Object != 4 || refused.Offset != -1 {
		t.Fatalf("location: %+v", refused)
	}
	legacy, err := noRepairRead(data, false, model.ValidationRelaxed)
	if err != nil {
		t.Fatal(err)
	}
	o, err := legacy.Dereference(*types.NewIndirectRef(4, 0))
	if err != nil || len(o.(types.Dict)) != 0 {
		t.Fatalf("legacy lazy parse: %v %v", o, err)
	}
}

func TestNoRepairCleanFormats(t *testing.T) {
	clean := discardClassic(discardObjects("(en)"), "", false, "")
	cases := map[string][]byte{
		"classic":                 clean,
		"object and xref streams": discardCompressed(discardObjects("(en)"), ""),
		"lazy":                    noRepairLazyPDF("<< /Value <a> /Flag true /Null null >>"),
		"incremental":             discardIncremental(discardObjects("(en)"), map[int]string{4: "<< /Title (new) >>"}, "", "", false),
		"empty stream":            noRepairStream("<< /Length 0 >>\nstream\n\nendstream"),
		"indirect length":         discardClassic([]string{discardObjects("(en)")[0], discardObjects("(en)")[1], discardObjects("(en)")[2], "<< /Length 5 0 R >>\nstream\nq Q\nendstream", "4"}, "", false, ""),
		"PDF 2.0 direct Info":     directInfoPDF20("2.0", false),
	}
	length := 16
	cases["encrypted RC4"] = cryptFilterLengthPDF(t, "V2", 4, "", false, &length)
	cases["encrypted AES128"] = cryptFilterLengthPDF(t, "AESV2", 4, "", false, &length)
	length = 32
	cases["encrypted AES256"] = cryptFilterLengthPDF(t, "AESV3", 6, "", false, &length)
	for _, file := range []string{"Hybrid-PDF.pdf", "signatures/ETSI.CAdES.detached/testPAdES_BT.pdf", "signatures/adbe.x509.rsa_sha1/sample01.pdf", "test.pdf", "pdf20/viaIncrementalSave.pdf"} {
		data, err := os.ReadFile(filepath.Join("..", "testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		cases[file] = data
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, err := noRepairRead(data, true, model.ValidationStrict)
			if err != nil {
				t.Fatal(err)
			}
			if name == "signatures/adbe.x509.rsa_sha1/sample01.pdf" && !ctx.Read.Linearized {
				t.Fatal("linearized control changed")
			}
			if name == "signatures/ETSI.CAdES.detached/testPAdES_BT.pdf" && !ctx.Read.Hybrid {
				t.Fatal("hybrid control changed")
			}
			if strings.HasPrefix(name, "encrypted ") {
				sd := ctx.Table[4].Object.(types.StreamDict)
				if err := sd.Decode(); err != nil || !bytes.Equal(sd.Content, []byte("q Q")) {
					t.Fatalf("standard decryption: %v", err)
				}
			}
			for n, e := range ctx.Table {
				if e.Free {
					continue
				}
				if _, err := ctx.Dereference(*types.NewIndirectRef(n, *e.Generation)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNoRepairConcurrentReads(t *testing.T) {
	conf := model.NewStatelessConfiguration()
	conf.NoRepair = true
	for i := 0; i < 16; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			for _, bad := range []bool{false, true, false} {
				value := "<< /Value (ok) >>"
				if bad {
					value = "<< /Value <zz> >>"
				}
				ctx, err := Read(t.Context(), bytes.NewReader(noRepairLazyPDF(value)), conf)
				if err != nil {
					t.Fatal(err)
				}
				_, err = ctx.Dereference(*types.NewIndirectRef(4, 0))
				if bad {
					requireRepairRefused(t, err, model.RepairParserHex)
				} else if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNoRepairErrorContainsOnlyNumericLocation(t *testing.T) {
	_, err := noRepairRead(discardClassic(discardObjects("<secret>"), "", false, ""), true, model.ValidationRelaxed)
	refused := requireRepairRefused(t, err, model.RepairParserHex)
	if refused.Object != 1 || refused.Offset != 9 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestNoRepairPlaintextMetadata(t *testing.T) {
	length := 16
	source := cryptFilterLengthPDF(t, "AESV2", 4, "", false, &length)
	ctx, err := noRepairRead(source, false, model.ValidationRelaxed)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := ctx.EncryptDict()
	if err != nil {
		t.Fatal(err)
	}
	content := `<x:xmpmeta xmlns:x="adobe:ns:meta/"></x:xmpmeta>` + "\n"
	objects := discardObjects("(en)")
	objects[0] = "<< /Type /Catalog /Pages 2 0 R /Metadata 4 0 R >>"
	objects[3] = fmt.Sprintf("<< /Type /Metadata /Subtype /XML /Length %d >>\nstream\n%s\nendstream", len(content), content)
	objects = append(objects, enc.PDFString())
	data := cryptFilterObjectsPDF(objects, ctx.E.ID)
	if _, err := noRepairRead(data, false, model.ValidationRelaxed); err != nil {
		t.Fatalf("legacy plaintext: %v", err)
	}
	_, err = noRepairRead(data, true, model.ValidationRelaxed)
	refused := requireRepairRefused(t, err, model.RepairPlaintextMetadata)
	if refused.Object != 4 {
		t.Fatalf("object: %d", refused.Object)
	}
}

func TestNoRepairFilterDataAfterRead(t *testing.T) {
	for _, tc := range []struct {
		name, filter string
		raw          []byte
	}{
		{"truncated flate", "FlateDecode", []byte{0x78, 0x9c, 0xcb, 0x48, 0xcd, 0xc9, 0xc9, 0x07, 0x00}},
		{"missing run length EOD", "RunLengthDecode", []byte{2, 'q', ' ', 'Q'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := noRepairStream(fmt.Sprintf("<< /Length %d /Filter /%s >>\nstream\n%s\nendstream", len(tc.raw), tc.filter, tc.raw))
			for _, enabled := range []bool{false, true} {
				ctx, err := noRepairRead(data, enabled, model.ValidationStrict)
				if err != nil {
					t.Fatal(err)
				}
				sd := ctx.Table[4].Object.(types.StreamDict)
				if enabled && tc.filter == "RunLengthDecode" {
					if _, err := sd.DecodeLength(3); err != nil {
						t.Fatalf("normal prefix decoding: %v", err)
					}
				}
				err = sd.Decode()
				if enabled {
					requireRepairRefused(t, err, model.RepairFilterData)
				} else if err != nil {
					t.Fatalf("legacy filter: %v", err)
				}
			}
		})
	}
}

func TestNoRepairRejectsBeforeChangingSource(t *testing.T) {
	data := discardClassic(discardObjects("(en)"), "", false, "")
	conf := model.NewStatelessConfiguration()
	conf.NoRepair = true
	ctx, err := Read(t.Context(), bytes.NewReader(data), conf)
	if err != nil {
		t.Fatal(err)
	}
	table := ctx.Table
	requireRepairRefused(t, bypassXrefSection(t.Context(), ctx, 0, nil, 1), model.RepairXRefReconstruction)
	if len(table) != len(ctx.Table) {
		t.Fatal("table replaced before refusal")
	}
	ctx.Table[99] = model.NewXRefTableEntryGen0(types.Integer(1))
	if table[99] == nil {
		t.Fatal("table replaced before refusal")
	}
}

func TestNoRepairStrictReconstructionAndIndexSize(t *testing.T) {
	for _, tc := range []struct {
		data []byte
		site model.RepairSite
	}{
		{discardIncremental(discardObjects("(en)"), map[int]string{4: "<< /Title (new) >>"}, "", "", true), model.RepairXRefReconstruction},
		{bytes.Replace(discardCompressed(discardObjects("(en)"), ""), []byte("/Size 7"), []byte("/Size 6 /Index [0 7]"), 1), model.RepairXRefSize},
	} {
		_, err := noRepairRead(tc.data, true, model.ValidationStrict)
		requireRepairRefused(t, err, tc.site)
	}
}

func TestNoRepairObjectStreamIndex(t *testing.T) {
	data := bytes.Replace(noRepairLazyPDF("<< /Value (ok) >>"), []byte("4 0 <<"), []byte("x 0 <<"), 1)
	if _, err := noRepairRead(data, false, model.ValidationRelaxed); err != nil {
		t.Fatal(err)
	}
	_, err := noRepairRead(data, true, model.ValidationRelaxed)
	requireRepairRefused(t, err, model.RepairObjectStreamIndex)
}

func TestNoRepairStandardObjectHeaders(t *testing.T) {
	for _, header := range []string{"1 0 obj", "1\t0\x00obj", "1% comment\n0 obj", "1 % digits 123\n0 % generation\nobj"} {
		line := header + "\n<< /Value (ok) >>"
		c := model.WithRepairScope(t.Context(), true, 1, 9)
		number, generation, err := readObjectAttributes(c, &line)
		if err != nil || *number != 1 || *generation != 0 || strings.TrimSpace(line) != "<< /Value (ok) >>" {
			t.Fatalf("standard header failed: %v", err)
		}
	}
}

func TestNoRepairMoreSourceRepairs(t *testing.T) {
	clean := discardClassic(discardObjects("(en)"), "", false, "")
	object4 := bytes.Index(clean, []byte("4 0 obj"))
	oldRow := []byte(fmt.Sprintf("%010d 00000 n", object4))
	for _, tc := range []struct {
		name string
		data []byte
		site model.RepairSite
	}{
		{"object EOF skip", bytes.Replace(clean, oldRow, fmt.Appendf(nil, "%010d 00000 n", len(clean)+10), 1), model.RepairObjectSkipped},
		{"dangling free insertion", bytes.Replace(clean, oldRow, []byte("0000000000 00000 f"), 1), model.RepairFreeLink},
		{"unresolved indirect Length", noRepairStream("<< /Length 0 65535 R >>\nstream\nq Q\nendstream"), model.RepairStreamLength},
		{"missing value substitution", discardClassic(discardObjects("(en) /Missing\n"), "", false, ""), model.RepairParserToken},
		{"single DecodeParms array", noRepairStream("<< /Length 3 /Filter /FlateDecode /DecodeParms [<< >>] >>\nstream\nabc\nendstream"), model.RepairFilterPipeline},
		{"pipeline DecodeParms dictionary", noRepairStream("<< /Length 3 /Filter [/FlateDecode] /DecodeParms << /Columns 1 >> >>\nstream\nabc\nendstream"), model.RepairFilterPipeline},
		{"invalid latest offset", append(bytes.Clone(clean), []byte("startxref\n99999999\n%%EOF\n")...), model.RepairEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := noRepairRead(tc.data, false, model.ValidationRelaxed); err != nil {
				t.Fatalf("legacy read: %v", err)
			}
			_, err := noRepairRead(tc.data, true, model.ValidationRelaxed)
			requireRepairRefused(t, err, tc.site)
		})
	}
}

func TestNoRepairEncryptionCompatibility(t *testing.T) {
	length := 128 // invalid legacy bit count in a byte-count field
	data := cryptFilterLengthPDF(t, "AESV2", 4, "", false, &length)
	if _, err := noRepairRead(data, false, model.ValidationRelaxed); err != nil {
		t.Fatal(err)
	}
	_, err := noRepairRead(data, true, model.ValidationRelaxed)
	requireRepairRefused(t, err, model.RepairEncryption)
	length = 16
	data = cryptFilterLengthPDF(t, "AESV2", 4, "", false, &length)
	// With a single encrypted block, changing the last IV byte flips the
	// last padding byte without changing the preceding plaintext bytes.
	start := bytes.Index(data, []byte("stream\n")) + len("stream\n")
	data[start+15] ^= 1
	if _, err := noRepairRead(data, false, model.ValidationStrict); err != nil {
		t.Fatal(err)
	}
	_, err = noRepairRead(data, true, model.ValidationStrict)
	requireRepairRefused(t, err, model.RepairEncryption)
}

func TestNoRepairTruncatedFlatePredictor(t *testing.T) {
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	if _, err := zw.Write([]byte{0, 'q', ' ', 'Q'}); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()[:b.Len()-4] // omit the zlib checksum after a complete predictor row
	data := noRepairStream(fmt.Sprintf("<< /Length %d /Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 3 >> >>\nstream\n%s\nendstream", len(raw), raw))
	for _, enabled := range []bool{false, true} {
		ctx, err := noRepairRead(data, enabled, model.ValidationStrict)
		if err != nil {
			t.Fatal(err)
		}
		sd := ctx.Table[4].Object.(types.StreamDict)
		err = sd.Decode()
		if enabled {
			requireRepairRefused(t, err, model.RepairFilterData)
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoRepairCancellationAndLimits(t *testing.T) {
	data := discardClassic(discardObjects("(en)"), "", false, "")
	conf := model.NewStatelessConfiguration()
	conf.NoRepair = true
	c, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Read(c, bytes.NewReader(data), conf); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	conf.Limits.MaxObjectCount = 2
	if _, err := Read(t.Context(), bytes.NewReader(data), conf); err == nil || model.IsRepairRefused(err) {
		t.Fatalf("resource limit: %v", err)
	}
}

func TestNoRepairLazyTrailingTokens(t *testing.T) {
	data := noRepairLazyPDF("<< /Value (ok) >> junk")
	for _, enabled := range []bool{false, true} {
		ctx, err := noRepairRead(data, enabled, model.ValidationStrict)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ctx.Dereference(*types.NewIndirectRef(4, 0))
		if enabled {
			requireRepairRefused(t, err, model.RepairParserToken)
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoRepairStreamMutationGuards(t *testing.T) {
	refusal := &model.RepairRefusedError{Site: model.RepairStreamLength, Object: 4, Offset: 9}
	_, err := readStreamContent(bytes.NewReader([]byte("q Q\nendstream")), 5000, 10000, refusal)
	requireRepairRefused(t, err, model.RepairStreamLength)
	_, err = readStreamContentBlindly(bytes.NewReader([]byte("q Q\nendstream")), 10000, refusal)
	requireRepairRefused(t, err, model.RepairStreamLength)
	length := int64(5)
	sd := types.StreamDict{Dict: types.Dict{"Length": types.Integer(5)}, StreamLength: &length, Raw: []byte("q Q")}
	requireRepairRefused(t, ensureStreamLength(&sd, false, refusal), model.RepairStreamLength)
	if *sd.StreamLength != 5 || sd.Dict["Length"] != types.Integer(5) {
		t.Fatal("Length changed before refusal")
	}
}
