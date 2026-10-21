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

package filter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func noRepairBits(bits string) []byte {
	b := make([]byte, (len(bits)+7)/8)
	for i, c := range bits {
		if c == '1' {
			b[i/8] |= 1 << (7 - i%8)
		}
	}
	return b
}

func TestNoRepairCodecTermination(t *testing.T) {
	refusal := errors.New("numeric repair refusal sentinel")
	for _, tc := range []struct {
		name, filter string
		parms        map[string]int
		raw          []byte
		want         []byte
		refuse       bool
	}{
		{"ASCIIHex missing EOD", ASCIIHex, nil, []byte("41"), []byte("A"), true},
		{"ASCIIHex partial nibble without EOD", ASCIIHex, nil, []byte("4"), []byte{0x40}, true},
		{"ASCIIHex empty without EOD", ASCIIHex, nil, nil, nil, true},
		{"ASCIIHex odd nibble at EOD", ASCIIHex, nil, []byte("4>"), []byte{0x40}, false},
		{"ASCIIHex whitespace", ASCIIHex, nil, []byte("4 \x00\t1>"), []byte("A"), false},
		{"LZW missing EOD", LZW, nil, []byte{0x80, 0x10, 0x40}, []byte("A"), true},
		{"LZW nonzero padding", LZW, nil, []byte{0x80, 0x10, 0x60, 0x21}, []byte("A"), true},
		{"LZW missing initial CLEAR", LZW, nil, noRepairBits("001000001100000001"), []byte("A"), true},
		{"LZW complete", LZW, nil, []byte{0x80, 0x10, 0x60, 0x20}, []byte("A"), false},
		{"CCITT missing EOFB", CCITTFax, map[string]int{"K": -1, "Columns": 8, "Rows": 2}, []byte{0xc0}, []byte{0xff, 0xff}, true},
		{"CCITT explicit EOFB", CCITTFax, map[string]int{"K": -1, "Columns": 8, "Rows": 2}, ccittTwoWhiteRows, []byte{0xff, 0xff}, false},
		{"CCITT EndOfBlock overrides Rows", CCITTFax, map[string]int{"K": -1, "Columns": 8, "Rows": 1}, ccittTwoWhiteRows, []byte{0xff, 0xff}, false},
		{"CCITT EndOfBlock false", CCITTFax, map[string]int{"K": -1, "Columns": 8, "Rows": 2, "EndOfBlock": 0}, []byte{0xc0}, []byte{0xff, 0xff}, false},
		{"CCITT inverted", CCITTFax, map[string]int{"K": -1, "Columns": 8, "Rows": 2, "BlackIs1": 1}, ccittTwoWhiteRows, []byte{0, 0}, false},
		{"CCITT byte aligned", CCITTFax, map[string]int{"K": -1, "Columns": 8, "Rows": 2, "EncodedByteAlign": 1}, noRepairBits("1000000010000000000000000001000000000001"), []byte{0xff, 0xff}, false},
		{"CCITT Group3 RTC", CCITTFax, map[string]int{"Columns": 8, "Rows": 1, "EndOfLine": 1}, noRepairBits("00000000000110011000000000001000000000001000000000001000000000001000000000001000000000001"), []byte{0xff}, false},
		{"CCITT Group3 missing RTC", CCITTFax, map[string]int{"Columns": 8, "Rows": 1, "EndOfLine": 1}, noRepairBits("00000000000110011"), []byte{0xff}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, strict := range []bool{false, true} {
				var repairError error
				if strict {
					repairError = refusal
				}
				f, err := NewFilterWithRepairError(tc.filter, tc.parms, 1024, repairError)
				if err != nil {
					t.Fatal(err)
				}
				r, err := f.Decode(bytes.NewReader(tc.raw))
				if strict && tc.refuse {
					if !errors.Is(err, refusal) {
						t.Fatalf("error=%v, want refusal", err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("strict=%v: %v", strict, err)
				}
				got, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				want := tc.want
				if !strict && tc.name == "CCITT EndOfBlock overrides Rows" {
					want = want[:1]
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("strict=%v: got %x want %x", strict, got, want)
				}
			}
		})
	}
}

func TestNoRepairCodecPrefixes(t *testing.T) {
	refusal := errors.New("numeric repair refusal sentinel")
	for _, tc := range []struct {
		name   string
		parms  map[string]int
		raw    []byte
		length int64
	}{
		{ASCIIHex, nil, []byte("41"), 1},
		{LZW, nil, []byte{0x80, 0x10, 0x40}, 1},
		{CCITTFax, map[string]int{"K": -1, "Columns": 8, "Rows": 2}, []byte{0xc0}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := NewFilterWithRepairError(tc.name, tc.parms, 1024, refusal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.DecodeLength(bytes.NewReader(tc.raw), tc.length); err != nil {
				t.Fatalf("prefix: %v", err)
			}
			if _, err := f.Decode(bytes.NewReader(tc.raw)); !errors.Is(err, refusal) {
				t.Fatalf("full decode: %v", err)
			}
		})
	}
	f, _ := NewFilterWithRepairError(ASCIIHex, nil, 1024, refusal)
	if _, err := f.DecodeLength(bytes.NewReader([]byte("4")), 1); !errors.Is(err, refusal) {
		t.Fatalf("prefix invented a nibble: %v", err)
	}
	f, _ = NewFilterWithRepairError(CCITTFax, map[string]int{"K": -1, "Columns": 8, "Rows": 2, "EndOfBlock": 0}, 1024, refusal)
	if _, err := f.DecodeLength(bytes.NewReader([]byte{0xc0}), 3); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("oversized CCITT prefix: %v", err)
	}
	f, _ = NewFilterWithRepairError(CCITTFax, map[string]int{"K": -1, "Columns": 8, "Rows": 2, "EndOfBlock": 0}, 1, refusal)
	if _, err := f.Decode(bytes.NewReader([]byte{0xc0})); !errors.Is(err, ErrDecodeLimitExceeded) {
		t.Fatalf("CCITT decode budget: %v", err)
	}
}

// A literal-only PDF code stream exercises every code width and fills the table.
// CLEAR and EOD remain legal at the table boundary; another literal does not.
func noRepairFullLZW(early int, clear bool) []byte {
	var bits strings.Builder
	bits.WriteString("100000000") // CLEAR at the initial 9-bit width.
	width := 9
	// The first literal adds no entry; the next 3838 fill codes 258..4095.
	for n := 0; n < 3839; n++ {
		if 257+n+early >= 1<<width && width < 12 {
			width++
		}
		fmt.Fprintf(&bits, "%0*b", width, 0)
	}
	if clear {
		bits.WriteString("000100000000" + "001000001" + "100000001")
	} else {
		bits.WriteString("000000000000" + "000100000001")
	}
	return noRepairBits(bits.String())
}

func TestNoRepairLZWFullTable(t *testing.T) {
	refusal := errors.New("numeric repair refusal sentinel")
	for _, early := range []int{0, 1} {
		for _, clear := range []bool{false, true} {
			raw := noRepairFullLZW(early, clear)
			for _, strict := range []bool{false, true} {
				var repairError error
				if strict {
					repairError = refusal
				}
				f, _ := NewFilterWithRepairError(LZW, map[string]int{"EarlyChange": early}, 10000, repairError)
				_, err := f.Decode(bytes.NewReader(raw))
				if strict && !clear {
					if !errors.Is(err, refusal) {
						t.Fatalf("early=%d error=%v", early, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("early=%d clear=%v strict=%v: %v", early, clear, strict, err)
				}
			}
		}
	}
}

func TestNoRepairLZWRoundTrip(t *testing.T) {
	data := make([]byte, 20000)
	for i := range data {
		data[i] = byte(i*i + 31*i + i/7)
	}
	for _, early := range []int{0, 1} {
		f, _ := NewFilterWithRepairError(LZW, map[string]int{"EarlyChange": early}, 100000, errors.New("refusal"))
		encoded, err := f.Encode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := f.Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(decoded)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("early=%d round trip: %v", early, err)
		}
	}
}
