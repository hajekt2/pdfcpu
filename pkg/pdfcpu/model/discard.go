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
	"slices"
)

// DiscardedToken records a token the relaxed parser replaced instead of failing:
// an invalid hex literal or an unparseable bare token read as null, or an
// out-of-range integer read as 0. Normalized objects are unchanged.
type DiscardedToken struct {
	ObjectNumber int      // 0 for a trailer dictionary
	Path         []string // dictionary keys and array indexes inside the object
	Kind         string   // "hex", "token" or "integer-range"

	// Trailer marks a trailer-dictionary entry the reader consulted. Root, Info,
	// ID and Size are taken from the newest trailer that still leaves them
	// unset; Encrypt, Prev and XRefStm are read from every trailer. Entries of
	// superseded trailers are therefore not marked.
	Trailer bool
}

// DiscardLog collects DiscardedToken records. It is nil unless
// Configuration.RecordDiscardedTokens is set.
type DiscardLog struct {
	Records []DiscardedToken

	trailerStart int
}

// BeginTrailer is called by the reader before it parses a trailer dictionary
// or an xref stream dictionary.
func (l *DiscardLog) BeginTrailer() {
	if l != nil {
		l.trailerStart = len(l.Records)
	}
}

// EndTrailer marks the records of the trailer dictionary parsed as object
// source (0 for a classic trailer) whose top-level key was consulted.
func (l *DiscardLog) EndTrailer(source int, consulted map[string]bool) {
	if l == nil {
		return
	}
	for i := l.trailerStart; i < len(l.Records); i++ {
		r := &l.Records[i]
		if r.ObjectNumber == source && len(r.Path) > 0 && consulted[r.Path[0]] {
			r.Trailer = true
		}
	}
	l.trailerStart = len(l.Records)
}

type discardScope struct {
	log    *DiscardLog
	object int
	path   []string
}

type discardScopeKey struct{}

// WithDiscardScope returns a context that records discards of object objNr into log.
func WithDiscardScope(c context.Context, log *DiscardLog, objNr int) context.Context {
	if log == nil || c == nil {
		return c
	}
	return context.WithValue(c, discardScopeKey{}, &discardScope{log: log, object: objNr})
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
		s.log.Records = append(s.log.Records, DiscardedToken{ObjectNumber: s.object, Path: slices.Clone(s.path), Kind: kind})
	}
}

func pushDiscardPath(c context.Context, segment string) func() {
	s := discardScopeOf(c)
	if s == nil {
		return func() {}
	}
	s.path = append(s.path, segment)
	return func() { s.path = s.path[:len(s.path)-1] }
}
