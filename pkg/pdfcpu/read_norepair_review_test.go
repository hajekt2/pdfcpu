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
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func TestNoRepairReviewXRefStreamZeroOffset(t *testing.T) {
	data := discardCompressed(discardObjects("(en)"), "")
	start := bytes.Index(data, []byte("6 0 obj\n"))
	start += bytes.Index(data[start:], []byte("stream\n")) + len("stream\n")
	for i := 1; i <= 4; i++ {
		data[start+4*7+i] = 0
	}
	for _, enabled := range []bool{false, true} {
		ctx, err := noRepairRead(data, enabled, model.ValidationStrict)
		if enabled {
			requireRepairRefused(t, err, model.RepairXRefEntry)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		o, err := ctx.Dereference(*types.NewIndirectRef(4, 0))
		t.Logf("NoRepair=%v in-use offset=%d object=%v dereference=%v err=%v", enabled, *ctx.Table[4].Offset, ctx.Table[4].Object, o, err)
		if o != nil || err != nil {
			t.Fatalf("legacy null = %v, %v", o, err)
		}
	}
}

func TestNoRepairReviewXRefUnknownType(t *testing.T) {
	data := discardCompressed(discardObjects("(en)"), "")
	start := bytes.Index(data, []byte("6 0 obj\n"))
	start += bytes.Index(data[start:], []byte("stream\n")) + len("stream\n")
	data[start+4*7] = 42
	ctx, err := noRepairRead(data, true, model.ValidationStrict)
	if err != nil {
		t.Fatalf("normal unknown-type null decoding refused: %v", err)
	}
	o, err := ctx.Dereference(*types.NewIndirectRef(4, 0))
	if o != nil || err != nil {
		t.Fatalf("unknown-type null = %v, %v", o, err)
	}
	if got := ctx.NoRepairNormalizations(); got != (model.NoRepairNormalizations{}) {
		t.Fatalf("normal decoding recorded a repair: %+v", got)
	}
	if *ctx.Table[0].Offset != 0 || *ctx.Table[4].Offset != 0 {
		t.Fatal("null placeholder entered free list")
	}
}

func TestNoRepairReviewDeferredFilterEOD(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"LZWDecode", []byte{0x80, 0x10, 0x40}},
		{"LZWDecode", []byte{0x80, 0x10, 0x60, 0x21}}, // nonzero final padding
		{"LZWDecode", nil},
		{"ASCIIHexDecode", []byte("41")},
		{"ASCIIHexDecode", nil},
	} {
		data := noRepairStream(fmt.Sprintf("<< /Length %d /Filter /%s >>\nstream\n%s\nendstream", len(tc.raw), tc.name, tc.raw))
		ctx, err := noRepairRead(data, true, model.ValidationStrict)
		if err != nil {
			t.Fatal(err)
		}
		sd := ctx.Table[4].Object.(types.StreamDict)
		err = sd.Decode()
		t.Logf("filter=%s decoded=%q err=%v", tc.name, sd.Content, err)
		requireRepairRefused(t, err, model.RepairFilterData)
	}
}

func TestNoRepairReviewParserErrorPrivacy(t *testing.T) {
	for _, value := range []string{"<< /PrivateDocumentKey <zz> >>", "<< /PrivateDocumentKey [foo] >>", "<< /PrivateDocumentKey << /Nested <zz> >> >>"} {
		data := discardClassic(discardObjects(value), "", false, "")
		_, err := noRepairRead(data, true, model.ValidationRelaxed)
		if !model.IsRepairRefused(err) || strings.Contains(err.Error(), "PrivateDocumentKey") {
			t.Fatalf("unsafe refusal: %v", err)
		}
	}
}

func TestNoRepairReviewCCITTTruncation(t *testing.T) {
	raw := []byte{0xc0}
	data := noRepairStream(fmt.Sprintf("<< /Length %d /Filter /CCITTFaxDecode /DecodeParms << /K -1 /Columns 8 /Rows 2 >> >>\nstream\n%s\nendstream", len(raw), raw))
	ctx, err := noRepairRead(data, true, model.ValidationStrict)
	if err != nil {
		t.Fatal(err)
	}
	sd := ctx.Table[4].Object.(types.StreamDict)
	err = sd.Decode()
	t.Logf("CCITT decoded=%x err=%v", sd.Content, err)
	requireRepairRefused(t, err, model.RepairFilterData)
}

func TestNoRepairReviewEncryptedRefusalPrivacy(t *testing.T) {
	length := 16
	source := cryptFilterLengthPDF(t, "AESV2", 4, "", false, &length)
	ctx, err := noRepairRead(source, false, model.ValidationStrict)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ctx.EncryptDict()
	if err != nil {
		t.Fatal(err)
	}
	d["StrF"] = types.Name("StdCF")
	raw, err := encryptBytes([]byte("A"), 1, 0, ctx.EncKey, true, 4)
	if err != nil {
		t.Fatal(err)
	}
	raw[15] ^= 1
	stream, err := encryptStream([]byte("q Q"), 4, 0, ctx.EncKey, true, 4)
	if err != nil {
		t.Fatal(err)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R /PrivateDocumentKey <" + hex.EncodeToString(raw) + "> >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream), d.PDFString(),
	}
	data := cryptFilterObjectsPDF(objects, ctx.E.ID)
	_, err = noRepairRead(data, true, model.ValidationStrict)
	requireRepairRefused(t, err, model.RepairEncryption)
	t.Logf("refusal includes document key=%v; error=%v", strings.Contains(err.Error(), "PrivateDocumentKey"), err)
	if strings.Contains(err.Error(), "PrivateDocumentKey") {
		t.Error("repair refusal exposes a source dictionary key")
	}
}

func TestNoRepairReviewObjectStreamIdentity(t *testing.T) {
	data := bytes.Replace(noRepairLazyPDF("<< /Value <a> >>"), []byte("4 0 <<"), []byte("7 0 <<"), 1)
	legacy, err := noRepairRead(data, false, model.ValidationStrict)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Dereference(*types.NewIndirectRef(4, 0)); err != nil {
		t.Fatal(err)
	}
	_, err = noRepairRead(data, true, model.ValidationStrict)
	requireRepairRefused(t, err, model.RepairObjectStreamIndex)
}
