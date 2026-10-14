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

package model

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func parseWithDiscards(t *testing.T, s string, log *DiscardLog) string {
	t.Helper()
	line := s
	result, err := ParseObjectWithPolicy(WithDiscardScope(context.Background(), log, 7, -1), &line, 0, ValidationRelaxed)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	if result.Object == nil {
		return "null"
	}
	return result.Object.PDFString()
}

func TestDiscardLogRecordsReplacedTokens(t *testing.T) {
	input := "<< /Lang <zz> /A [1 <zz> foo] /B 1.2.3 /C 99999999999999999999 /D << /E <zz> >> /N null /H <656e> >>"
	log := &DiscardLog{}
	recorded := parseWithDiscards(t, input, log)
	plain := parseWithDiscards(t, input, nil)
	if recorded != plain {
		t.Fatalf("recording changed the normalized object:\n%s\n%s", recorded, plain)
	}
	got := []string{}
	for _, r := range log.Records {
		got = append(got, fmt.Sprintf("%d:%s:%s", r.ObjectNumber, strings.Join(r.Path, "/"), r.Kind))
	}
	want := "7:Lang:hex 7:A/1:hex 7:A/2:token 7:B:token 7:C:integer-range 7:D/E:hex"
	if strings.Join(got, " ") != want {
		t.Fatalf("records = %v, want %s", got, want)
	}
}

func TestDiscardLogIgnoresValidTokensAndNilLog(t *testing.T) {
	log := &DiscardLog{}
	parseWithDiscards(t, "<< /N null /H <656e> /I 12 /R 1.5 /S (x) /X 3 0 R /A [null true] >>", log)
	if len(log.Records) != 0 {
		t.Fatalf("valid tokens recorded: %+v", log.Records)
	}
	c := context.Background()
	if WithDiscardScope(c, nil, 1, -1) != c {
		t.Fatal("a nil log must not install a scope")
	}
	var none *DiscardLog
	none.BeginTrailer()
	none.EndTrailer(0, map[string]bool{"Info": true}, nil, false)
}

func TestDiscardLogMarksConsultedTrailerEntries(t *testing.T) {
	log := &DiscardLog{}
	log.Records = append(log.Records, DiscardedToken{ObjectNumber: 0, Path: []string{"Info"}})
	log.BeginTrailer()
	log.Records = append(log.Records,
		DiscardedToken{ObjectNumber: 0, Path: []string{"Info"}},
		DiscardedToken{ObjectNumber: 0, Path: []string{"Root"}},
		DiscardedToken{ObjectNumber: 5, Path: []string{"Info"}},
	)
	log.EndTrailer(0, map[string]bool{"Info": true, "Root": false}, nil, false)
	marks := []bool{}
	for _, r := range log.Records {
		marks = append(marks, r.Trailer)
	}
	if fmt.Sprint(marks) != "[false true false false]" {
		t.Fatalf("trailer marks = %v", marks)
	}
}

func TestDiscardLogCapsRecordsAndMarksSaturation(t *testing.T) {
	log := &DiscardLog{MaxRecords: 2}
	parseWithDiscards(t, "<< /A [<zz> <zz> foo 1.2.3] /B <zz> >>", log)
	if len(log.Records) != 2 || !log.Saturated[DiscardSite{7, -1}] {
		t.Fatalf("records=%d saturated=%v", len(log.Records), log.Saturated)
	}
	// The same object and path is recorded once, so a re-parse adds nothing.
	again := &DiscardLog{}
	parseWithDiscards(t, "<< /A <zz> >>", again)
	parseWithDiscards(t, "<< /A <zz> >>", again)
	if len(again.Records) != 1 {
		t.Fatalf("duplicate records: %+v", again.Records)
	}
}

func TestDiscardLogRepairSupersedesOlderTrailerMarks(t *testing.T) {
	log := &DiscardLog{}
	log.BeginTrailer()
	log.Records = append(log.Records, DiscardedToken{ObjectNumber: 0, Path: []string{"Info"}})
	log.EndTrailer(0, map[string]bool{"Info": true}, map[string]bool{}, true)
	if !log.Records[0].Trailer {
		t.Fatal("older trailer entry not consulted")
	}
	log.BeginTrailer()
	log.EndTrailer(0, map[string]bool{"Info": true}, map[string]bool{"Info": true}, true)
	if log.Records[0].Trailer {
		t.Fatal("a newer repaired trailer supplying Info did not supersede the older mark")
	}
}

// With recording off, the discard path adds no allocation per array member:
// the parse costs what upstream's does (one allocation per member here).
func TestDiscardPathIsFreeWhenRecordingIsOff(t *testing.T) {
	allocs := func(members int) float64 {
		input := "[" + strings.Repeat("0 ", members) + "]"
		return testing.AllocsPerRun(10, func() {
			line := input
			if _, err := ParseObjectWithPolicy(context.Background(), &line, 0, ValidationRelaxed); err != nil {
				t.Fatal(err)
			}
		})
	}
	perMember := (allocs(1001) - allocs(1)) / 1000
	if perMember > 1.05 {
		t.Fatalf("recording off allocates %.2f times per array member", perMember)
	}
}

func TestDiscardLogSaturatesOnlyConsultedTrailerKeys(t *testing.T) {
	log := &DiscardLog{MaxRecords: 1, Records: []DiscardedToken{{ObjectNumber: 3}}}
	log.BeginTrailer()
	parseWithDiscards(t, "<< /Foo foo /Info <zz> >>", log)
	log.EndTrailer(7, map[string]bool{"Info": true}, nil, false)
	if !log.SaturatedTrailerKeys["Info"] || log.SaturatedTrailerKeys["Foo"] {
		t.Fatalf("saturated trailer keys = %v", log.SaturatedTrailerKeys)
	}
	log.BeginTrailer()
	log.EndTrailer(0, map[string]bool{"Info": true}, map[string]bool{"Info": true}, true)
	if log.SaturatedTrailerKeys["Info"] {
		t.Fatal("a newer repaired trailer supplying Info did not supersede the saturated key")
	}
}
