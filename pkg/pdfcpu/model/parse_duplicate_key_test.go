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
	"reflect"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// A repeated dictionary key is accepted without an error or message: the later
// non-null value replaces the earlier one, and a null value removes the key.
func TestParseDictDuplicateKeySemantics(t *testing.T) {
	for _, tc := range []struct {
		name, dict string
		want       types.Object
		removed    bool
	}{
		{"later value wins", "<</A 1 /A 2>>", types.Integer(2), false},
		{"later dictionary wins", "<</A 1 /A <</B 3>>>>", types.Dict{"B": types.Integer(3)}, false},
		{"null removes the key", "<</A 1 /A null>>", nil, true},
		{"first null is not stored", "<</A null /B 2>>", nil, true},
		{"repeated null keeps the key removed", "<</A 1 /A null /A 4 /A null>>", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []int{ValidationRelaxed, ValidationStrict} {
				s := tc.dict
				got, err := ParseObjectWithPolicy(t.Context(), &s, 0, mode)
				if err != nil {
					t.Fatalf("mode %d: %v", mode, err)
				}
				d, ok := got.Object.(types.Dict)
				if !ok {
					t.Fatalf("mode %d: not a dict: %T", mode, got.Object)
				}
				val, found := d["A"]
				if tc.removed {
					if found {
						t.Fatalf("mode %d: key A = %v; want it removed", mode, val)
					}
					continue
				}
				if !found || !reflect.DeepEqual(val, tc.want) {
					t.Fatalf("mode %d: key A = %v (found %t); want %v", mode, val, found, tc.want)
				}
			}
		})
	}
}

// A repeated key is not a repair, so it carries no refusal site under either
// validation mode.
func TestParseDictDuplicateKeyIsNoRepairSite(t *testing.T) {
	s := "<</A 1 /A 2>>"
	c := WithRepairScope(t.Context(), true, 1, 0)
	if _, err := ParseObjectWithPolicy(c, &s, 0, ValidationStrict); err != nil {
		t.Fatalf("duplicate key refused under a repair scope: %v", err)
	}
}
