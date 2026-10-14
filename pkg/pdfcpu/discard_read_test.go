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
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func discardObjects(lang string) []string {
	return []string{
		"<< /Type /Catalog /Pages 2 0 R /Lang " + lang + " >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] >>",
		"<< /Title (t) >>",
	}
}

// discardClassic writes a classic xref file and, optionally, an incremental
// section that rewrites object 4 with its own trailer entries.
func discardClassic(objects []string, trailer string, update bool, updateTrailer string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offsets := []int{}
	for i, o := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, trailer, xref)
	if update {
		off := b.Len()
		fmt.Fprintf(&b, "4 0 obj\n<< /Title (u) >>\nendobj\n")
		next := b.Len()
		fmt.Fprintf(&b, "xref\n4 1\n%010d 00000 n \ntrailer\n<< /Size %d /Root 1 0 R%s /Prev %d >>\nstartxref\n%d\n%%%%EOF\n", off, len(objects)+1, updateTrailer, xref, next)
	}
	return b.Bytes()
}

// discardCompressed stores object 1 in an object stream (object 5) and uses an
// xref stream (object 6) whose dictionary carries the trailer entries.
func discardCompressed(objects []string, trailer string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offsets := map[int]int{}
	for i, o := range objects[1:] {
		offsets[i+2] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+2, o)
	}
	content := "1 0 " + objects[0]
	offsets[5] = b.Len()
	fmt.Fprintf(&b, "5 0 obj\n<< /Type /ObjStm /N 1 /First 4 /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content)
	offsets[6] = b.Len()
	var records bytes.Buffer
	for id := 0; id <= 6; id++ {
		record := [7]byte{1}
		binary.BigEndian.PutUint32(record[1:5], uint32(offsets[id]))
		switch id {
		case 0:
			record = [7]byte{0, 0, 0, 0, 0, 0xff, 0xff}
		case 1:
			record = [7]byte{2, 0, 0, 0, 5, 0, 0}
		}
		records.Write(record[:])
	}
	fmt.Fprintf(&b, "6 0 obj\n<< /Type /XRef /Size 7 /Root 1 0 R%s /W [1 4 2] /Length %d >>\nstream\n%s\nendstream\nendobj\n", trailer, records.Len(), records.Bytes())
	fmt.Fprintf(&b, "startxref\n%d\n%%%%EOF\n", offsets[6])
	return b.Bytes()
}

func discardRead(t *testing.T, data []byte, record bool) (*model.Context, string) {
	return discardReadLimited(t, data, record, 0)
}

func discardReadLimited(t *testing.T, data []byte, record bool, limit int) (*model.Context, string) {
	t.Helper()
	conf := model.NewStatelessConfiguration()
	conf.ValidationMode = model.ValidationStrict
	conf.RecordDiscardedTokens = record
	conf.DiscardRecordLimit = limit
	ctx, err := Read(context.Background(), bytes.NewReader(data), conf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	numbers := []int{}
	for n := range ctx.XRefTable.Table {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	var objects strings.Builder
	for _, n := range numbers {
		e := ctx.XRefTable.Table[n]
		if e == nil || e.Free || e.Generation == nil {
			continue
		}
		o, err := ctx.XRefTable.Dereference(*types.NewIndirectRef(n, *e.Generation))
		if err != nil {
			t.Fatalf("dereference %d: %v", n, err)
		}
		if o != nil {
			fmt.Fprintf(&objects, "%d=%s;", n, o.PDFString())
		}
	}
	return ctx, objects.String()
}

func TestReadRecordsDiscardedTokens(t *testing.T) {
	for _, tt := range []struct {
		name string
		data []byte
		want string
	}{
		{"classic", discardClassic(discardObjects("<zz>"), " /Info 4 0 R", false, ""), "1:Lang:hex:false"},
		{"bare token", discardClassic(discardObjects("foo"), " /Info 4 0 R", false, ""), "1:Lang:token:false"},
		{"object stream", discardCompressed(discardObjects("<zz>"), " /Info 4 0 R"), "1:Lang:hex:false"},
		{"newest trailer Info", discardClassic(discardObjects("(en)"), " /Info 4 0 R", true, " /Info <zz>"), "0:Info:hex:true"},
		{"superseded trailer Info", discardClassic(discardObjects("(en)"), " /Info <zz>", true, " /Info 4 0 R"), "0:Info:hex:false"},
		{"xref stream Info", discardCompressed(discardObjects("(en)"), " /Info <zz>"), "6:Info:hex:true"},
		{"clean", discardClassic(discardObjects("(en)"), " /Info 4 0 R", false, ""), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			on, recorded := discardRead(t, tt.data, true)
			off, plain := discardRead(t, tt.data, false)
			if recorded != plain {
				t.Fatalf("recording changed normalized objects:\n%s\n%s", recorded, plain)
			}
			if off.XRefTable.Discards != nil {
				t.Fatal("discard log allocated while recording is off")
			}
			got := []string{}
			for _, r := range on.XRefTable.Discards.Records {
				got = append(got, fmt.Sprintf("%d:%s:%s:%t", r.ObjectNumber, strings.Join(r.Path, "/"), r.Kind, r.Trailer))
			}
			if strings.Join(got, " ") != tt.want {
				t.Fatalf("records = %v, want %q", got, tt.want)
			}
		})
	}
}

// discardIncremental writes objects, then an update section that rewrites the
// objects in newer, each section with its own trailer entries. With corrupt,
// every startxref is off by three bytes, so the reader rebuilds the xref by
// scanning every object version and trailer in file order.
func discardIncremental(objects []string, newer map[int]string, trailer, newerTrailer string, corrupt bool) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offsets := []int{}
	for i, o := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	shift := 0
	if corrupt {
		shift = 3
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, trailer, xref+shift)
	ids := []int{}
	for id := range newer {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	updated := map[int]int{}
	for _, id := range ids {
		updated[id] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", id, newer[id])
	}
	next := b.Len()
	b.WriteString("xref\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "%d 1\n%010d 00000 n \n", id, updated[id])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R%s /Prev %d >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, newerTrailer, xref, next+shift)
	return b.Bytes()
}

func TestReadRecordsOnlyLoadedVersionsWhenRepairing(t *testing.T) {
	good, bad := discardObjects("(en)"), discardObjects("<zz>")
	for _, tt := range []struct {
		name string
		data []byte
		live bool   // the Lang record belongs to the loaded catalog version
		info string // expected trailer Info record, if any
	}{
		{"older catalog version bad", discardIncremental(bad, map[int]string{1: good[0]}, " /Info 4 0 R", " /Info 4 0 R", true), false, ""},
		{"newest catalog version bad", discardIncremental(good, map[int]string{1: bad[0]}, " /Info 4 0 R", " /Info 4 0 R", true), true, ""},
		{"older trailer Info bad", discardIncremental(good, map[int]string{4: "<< /Title (u) >>"}, " /Info <zz>", " /Info 4 0 R", true), false, "0:Info:hex:false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			on, recorded := discardRead(t, tt.data, true)
			_, plain := discardRead(t, tt.data, false)
			if recorded != plain {
				t.Fatal("recording changed normalized objects")
			}
			entry := on.XRefTable.Table[1]
			langLive, langSuperseded, trailer := false, false, ""
			for _, r := range on.XRefTable.Discards.Records {
				if r.ObjectNumber == 1 && r.Offset == *entry.Offset {
					langLive = true
				}
				if r.ObjectNumber == 1 && r.Offset >= 0 && r.Offset != *entry.Offset {
					langSuperseded = true
				}
				if r.ObjectNumber == 0 {
					trailer = fmt.Sprintf("%d:%s:%s:%t", r.ObjectNumber, strings.Join(r.Path, "/"), r.Kind, r.Trailer)
				}
			}
			// The repair scan parses the superseded version too; its record
			// carries that version's offset, not the loaded one.
			if tt.name == "older catalog version bad" && !langSuperseded {
				t.Fatal("repair scan did not record the superseded version at its own offset")
			}
			if langLive != tt.live || trailer != tt.info {
				t.Fatalf("live Lang record=%t trailer=%q records=%+v", langLive, trailer, on.XRefTable.Discards.Records)
			}
		})
	}
}

func TestReadCapsDiscardRecords(t *testing.T) {
	objects := discardObjects("<zz>")
	objects[3] = "<< /Title (t) /A [<zz> foo 1.2.3 <zz>] >>"
	on, _ := discardReadLimited(t, discardClassic(objects, " /Info 4 0 R", false, ""), true, 2)
	log := on.XRefTable.Discards
	if len(log.Records) != 2 || !log.Saturated[4] {
		t.Fatalf("records=%+v saturated=%v", log.Records, log.Saturated)
	}
}
