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

// Added in the hajekt2/pdfcpu fork (branch uacheck/discard-log): opt-in
// recording of tokens the relaxed parser replaces. See README.md, "Fork note".

package model

import (
	"context"
	"strconv"
	"strings"
)

// DiscardedToken records a token the relaxed parser replaced instead of failing:
// an invalid hex literal or an unparseable bare token read as null, or an
// out-of-range integer read as 0. Normalized objects are unchanged.
type DiscardedToken struct {
	ObjectNumber int      // 0 for a classic trailer dictionary
	Offset       int64    // file offset of the parsed classic object; -1 otherwise
	Path         []string // dictionary keys and array indexes inside the object
	Kind         string   // "hex", "token" or "integer-range"

	// Trailer marks a trailer-dictionary entry the reader consulted. Root, Info,
	// ID and Size are taken from the newest trailer that still leaves them
	// unset; Encrypt, Prev and XRefStm are read from every trailer. Entries of
	// superseded trailers are therefore not marked.
	Trailer bool
}

// DiscardLog collects DiscardedToken records. It is nil unless
// Configuration.RecordDiscardedTokens is set. With MaxRecords > 0 it keeps at
// most MaxRecords distinct records; further replacements only mark their
// object in Saturated, so memory stays proportional to the number of objects.
type DiscardLog struct {
	Records []DiscardedToken

	// Saturated lists objects with replacements that were not recorded
	// individually because the log was full.
	Saturated map[int]bool

	// TrailerSaturated is set when a trailer or xref stream dictionary lost
	// replacements to the cap.
	TrailerSaturated bool

	// MaxRecords caps len(Records); 0 means no cap.
	MaxRecords int

	seen         map[string]struct{}
	trailerStart int
	inTrailer    bool
}

// BeginTrailer is called by the reader before it parses a trailer dictionary
// or an xref stream dictionary.
func (l *DiscardLog) BeginTrailer() {
	if l != nil {
		l.trailerStart = len(l.Records)
		l.inTrailer = true
	}
}

// EndTrailer marks the records of the trailer dictionary parsed as object
// source (0 for a classic trailer) whose top-level key was consulted. While
// repairing, the reader reads trailers oldest first, so a key this newer
// trailer supplied supersedes the marks of earlier trailers.
func (l *DiscardLog) EndTrailer(source int, consulted, supplied map[string]bool, repairing bool) {
	if l == nil {
		return
	}
	if repairing {
		for i := 0; i < l.trailerStart; i++ {
			r := &l.Records[i]
			if r.Trailer && supplied[r.Path[0]] {
				r.Trailer = false
			}
		}
	}
	for i := l.trailerStart; i < len(l.Records); i++ {
		r := &l.Records[i]
		if r.ObjectNumber == source && len(r.Path) > 0 && consulted[r.Path[0]] {
			r.Trailer = true
		}
	}
	l.trailerStart = len(l.Records)
	l.inTrailer = false
}

type discardSegment struct {
	key   string
	index int // >= 0 for an array member
}

type discardScope struct {
	log       *DiscardLog
	object    int
	offset    int64
	path      []discardSegment
	saturated bool
}

type discardScopeKey struct{}

// WithDiscardScope returns a context that records discards of object objNr,
// parsed from file offset (-1 when not a classic object), into log.
func WithDiscardScope(c context.Context, log *DiscardLog, objNr int, offset int64) context.Context {
	if log == nil || c == nil {
		return c
	}
	return context.WithValue(c, discardScopeKey{}, &discardScope{log: log, object: objNr, offset: offset})
}

func discardScopeOf(c context.Context) *discardScope {
	if c == nil {
		return nil
	}
	s, _ := c.Value(discardScopeKey{}).(*discardScope)
	return s
}

func recordDiscard(c context.Context, kind string) {
	if s := discardScopeOf(c); s != nil {
		s.record(kind)
	}
}

func (s *discardScope) record(kind string) {
	l := s.log
	if s.saturated {
		return
	}
	if l.MaxRecords > 0 && len(l.Records) >= l.MaxRecords {
		s.saturated = true
		if l.Saturated == nil {
			l.Saturated = map[int]bool{}
		}
		l.Saturated[s.object] = true
		if l.inTrailer {
			l.TrailerSaturated = true
		}
		return
	}
	path := make([]string, len(s.path))
	for i, segment := range s.path {
		path[i] = segment.key
		if segment.index >= 0 {
			path[i] = strconv.Itoa(segment.index)
		}
	}
	key := strconv.Itoa(s.object) + "\x00" + strconv.FormatInt(s.offset, 10) + "\x00" + strings.Join(path, "\x00")
	if l.seen == nil {
		l.seen = map[string]struct{}{}
	}
	if _, duplicate := l.seen[key]; duplicate {
		return
	}
	l.seen[key] = struct{}{}
	l.Records = append(l.Records, DiscardedToken{ObjectNumber: s.object, Offset: s.offset, Path: path, Kind: kind})
}

func (s *discardScope) pushKey(key string) {
	s.path = append(s.path, discardSegment{key: key, index: -1})
}

func (s *discardScope) pushIndex(index int) {
	s.path = append(s.path, discardSegment{index: index})
}

func (s *discardScope) pop() {
	s.path = s.path[:len(s.path)-1]
}
