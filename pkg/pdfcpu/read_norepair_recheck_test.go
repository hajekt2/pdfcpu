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

// Added in the hajekt2/pdfcpu fork: independent NoRepair recheck regressions.

package pdfcpu

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func TestNoRepairRecheckTrailerPrefix(t *testing.T) {
	clean := discardClassic(discardObjects("(en)"), "", false, "")
	for _, prefix := range []string{"junk\n", "null\n", "/PrivateTrailerKey 42\n", "junk ", "% comment\njunk\n", "\v", "\u00a0", "\u2003"} {
		for _, sameLine := range []bool{false, true} {
			// Three fields on the trailer line already fail as a malformed
			// xref row in the legacy reader, before trailer scanning begins.
			if sameLine && len(strings.Fields(strings.Split(prefix, "\n")[0])) > 1 {
				continue
			}
			for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
				t.Run(fmt.Sprintf("%q/sameLine=%t/mode=%d", prefix, sameLine, mode), func(t *testing.T) {
					separator := "\n"
					if sameLine {
						separator = " "
					}
					data := bytes.Replace(clean, []byte("trailer\n<<"), []byte("trailer"+separator+prefix+"<<"), 1)
					if _, err := noRepairRead(data, false, mode); err != nil {
						t.Fatalf("legacy: %v", err)
					}
					_, err := noRepairRead(data, true, mode)
					refused := requireRepairRefused(t, err, model.RepairParserToken)
					if refused.Object != 0 || refused.Offset != -1 || strings.Contains(err.Error(), "PrivateTrailerKey") {
						t.Fatalf("unsafe trailer refusal: %v", err)
					}
				})
			}
		}
	}
	for _, prefix := range []string{"", "\x00\t\n\f\r ", "% comment\n", "% << ignored dictionary delimiter\n", "% comment\r% another comment\n"} {
		for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
			data := bytes.Replace(clean, []byte("trailer\n<<"), []byte("trailer\n"+prefix+"<<"), 1)
			if _, err := noRepairRead(data, true, mode); err != nil {
				t.Fatalf("normal trailer prefix %q, mode=%d: %v", prefix, mode, err)
			}
		}
	}
}

func TestNoRepairRecheckXRefWhitespace(t *testing.T) {
	clean := discardClassic(discardObjects("(en)"), "", false, "")
	for _, prefix := range []string{"\x00", "\t", "\n", "\f", "\r", " ", "\v", "\u0085", "\u00a0", "\u2003"} {
		for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
			t.Run(fmt.Sprintf("%q/mode=%d", prefix, mode), func(t *testing.T) {
				data := bytes.Replace(clean, []byte("xref\n0 5"), []byte(prefix+"xref\n0 5"), 1)
				_, err := noRepairRead(data, true, mode)
				refused := requireRepairRefused(t, err, model.RepairXRefShift)
				if refused.Offset != int64(bytes.Index(clean, []byte("xref\n"))) {
					t.Fatalf("shift location = %d", refused.Offset)
				}
			})
		}
	}
}

func TestNoRepairRecheckTrailerEpilogTokens(t *testing.T) {
	for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
		clean := discardClassic(discardObjects("(en)"), "", false, "")
		for _, junk := range []string{"junk", "null", "/PrivateTrailerKey 42"} {
			data := bytes.Replace(clean, []byte("\nstartxref\n"), []byte("\n% startxref inside a comment\n"+junk+"\nstartxref\n"), 1)
			if _, err := noRepairRead(data, false, mode); err != nil {
				t.Fatalf("legacy: %v", err)
			}
			_, err := noRepairRead(data, true, mode)
			requireRepairRefused(t, err, model.RepairParserToken)
		}
		for _, trailer := range []string{" /Label (startxref)", " /startxref (valid name)", " /Label (first\nstartxref\nlast)", " /Label (100% startxref)"} {
			data := discardClassic(discardObjects("(en)"), trailer, false, "")
			data = bytes.Replace(data, []byte("\nstartxref\n"), []byte("\n% startxref inside a comment\nstartxref\n"), 1)
			if _, err := noRepairRead(data, true, mode); err != nil {
				t.Fatalf("normal trailer %q, mode=%d: %v", trailer, mode, err)
			}
		}
	}
}

func TestNoRepairRecheckWhitespaceSiblings(t *testing.T) {
	clean := discardClassic(discardObjects("(en)"), "", false, "")
	for _, space := range []string{"\v", "\u0085", "\u00a0", "\u2003"} {
		objects := discardObjects("(en)")
		objects[3] = space + "<< /Title (t) >>" + space
		for _, tc := range []struct {
			name string
			data []byte
		}{
			{"epilog leading", bytes.Replace(clean, []byte("startxref\n"), []byte("startxref\n"+space), 1)},
			{"epilog trailing", bytes.Replace(clean, []byte("\n%%EOF"), []byte(space+"\n%%EOF"), 1)},
			{"object boundary", discardClassic(objects, "", false, "")},
			{"hex boundary", noRepairLazyPDF("<< /Value <" + space + "41" + space + "> >>")},
			{"xref row", bytes.Replace(clean, []byte(" 65535 f"), []byte(space+"65535 f"), 1)},
			{"xref subsection", bytes.Replace(clean, []byte("xref\n0 5"), []byte("xref\n0"+space+"5"), 1)},
			{"object stream prolog", noRepairLazyPDFWithProlog("<< /Value 1 >>", "4"+space+"0 ")},
		} {
			for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
				t.Run(fmt.Sprintf("%s/%q/mode=%d", tc.name, space, mode), func(t *testing.T) {
					legacy, err := noRepairRead(tc.data, false, mode)
					if err == nil {
						_, err = legacy.Dereference(*types.NewIndirectRef(4, 0))
					}
					if err != nil {
						t.Fatalf("legacy control: %v", err)
					}
					ctx, err := noRepairRead(tc.data, true, mode)
					if err == nil {
						_, err = ctx.Dereference(*types.NewIndirectRef(4, 0))
					}
					if err == nil {
						t.Fatal("non-PDF whitespace accepted without refusal")
					}
				})
			}
		}
	}
}

func TestNoRepairRecheckObjectStreamPrologComments(t *testing.T) {
	for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
		for _, junk := range []string{"junk", "5 2"} {
			data := noRepairLazyPDFWithProlog("<< /Value 1 >>", "4 0 % comment\n"+junk+"\n")
			if _, err := noRepairRead(data, false, mode); err != nil {
				t.Fatalf("legacy: %v", err)
			}
			_, err := noRepairRead(data, true, mode)
			requireRepairRefused(t, err, model.RepairObjectStreamIndex)
		}
		for _, prolog := range []string{"4 % comment\n0 ", "4\x00% comment\r0\x00", "4 0 % trailing comment\n"} {
			ctx, err := noRepairRead(noRepairLazyPDFWithProlog("<< /Value 1 >>", prolog), true, mode)
			if err != nil {
				t.Fatalf("normal prolog %q: %v", prolog, err)
			}
			if _, err := ctx.Dereference(*types.NewIndirectRef(4, 0)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestNoRepairRecheckPDFWhitespaceControls(t *testing.T) {
	for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
		for _, space := range []string{"\x00", "\t", "\n", "\f", "\r", " "} {
			objects := discardObjects("(en)")
			objects[3] = space + "<< /Value <" + space + "41" + space + "> >>" + space
			classic := discardClassic(objects, "", false, "")
			classic = bytes.Replace(classic, []byte("startxref\n"), []byte("startxref\n"+space), 1)
			classic = bytes.Replace(classic, []byte("\n%%EOF"), []byte(space+"\n%%EOF"), 1)
			for _, data := range [][]byte{classic, noRepairLazyPDFWithProlog("<< /Value <41> >>", "4"+space+"0 ")} {
				ctx, err := noRepairRead(data, true, mode)
				if err != nil {
					t.Fatalf("normal whitespace %q, mode=%d: %v", space, mode, err)
				}
				o, err := ctx.Dereference(*types.NewIndirectRef(4, 0))
				if err != nil || o.(types.Dict)["Value"] != types.HexLiteral("41") {
					t.Fatalf("whitespace changed the value: %v, %v", o, err)
				}
			}
		}
	}
}

func TestNoRepairRecheckUnknownTypeHead(t *testing.T) {
	for _, kind := range []byte{3, 42, 255} {
		for _, mode := range []int{model.ValidationStrict, model.ValidationRelaxed} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("type=%d/mode=%d/NoRepair=%t", kind, mode, enabled), func(t *testing.T) {
					data := discardCompressed(discardObjects("(en)"), "")
					start := bytes.Index(data, []byte("6 0 obj\n"))
					start += bytes.Index(data[start:], []byte("stream\n")) + len("stream\n")
					data[start] = kind
					ctx, err := noRepairRead(data, enabled, mode)
					if err != nil {
						t.Fatal(err)
					}
					want := model.NoRepairNormalizations{FreeListHeadSynthesized: enabled}
					if got := ctx.NoRepairNormalizations(); got != want {
						t.Fatalf("facts = %+v; want %+v", got, want)
					}
					if enabled && ctx.Table[0].Null {
						t.Fatal("synthesized head still marked as null")
					}
					if err := ctx.EnsureValidFreeList(); err != nil || ctx.NoRepairNormalizations() != want {
						t.Fatalf("repeat normalization changed facts: %v", err)
					}
				})
			}
		}
	}
}
