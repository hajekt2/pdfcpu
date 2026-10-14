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
	result, err := ParseObjectWithPolicy(WithDiscardScope(context.Background(), log, 7), &line, 0, ValidationRelaxed)
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
	if WithDiscardScope(c, nil, 1) != c {
		t.Fatal("a nil log must not install a scope")
	}
	var none *DiscardLog
	none.BeginTrailer()
	none.EndTrailer(0, map[string]bool{"Info": true})
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
	log.EndTrailer(0, map[string]bool{"Info": true, "Root": false})
	marks := []bool{}
	for _, r := range log.Records {
		marks = append(marks, r.Trailer)
	}
	if fmt.Sprint(marks) != "[false true false false]" {
		t.Fatalf("trailer marks = %v", marks)
	}
}
