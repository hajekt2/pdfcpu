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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// This optional census observes Size before Read's final replacement. It does
// not relax the production policy or interpret references inside stream data.
func TestNoRepairXRefSizeCensus(t *testing.T) {
	root := os.Getenv("PDFCPU_NOREPAIR_CORPUS")
	if root == "" {
		t.Skip("set PDFCPU_NOREPAIR_CORPUS to run the optional corpus census")
	}
	counts := map[string]int{"a_larger": 0, "b_smaller_referenced": 0, "c_smaller_unreferenced": 0, "d_other": 0}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".pdf") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		counts["files"]++
		_, err = noRepairRead(data, true, model.ValidationStrict)
		var refused *model.RepairRefusedError
		if !errors.As(err, &refused) || refused.Site != model.RepairXRefSize {
			return nil
		}
		counts["size_refusals"]++
		if refused.Object != -1 || refused.Offset != -1 {
			counts["d_other"]++
			return nil
		}

		conf := model.NewStatelessConfiguration()
		conf.ValidationMode = model.ValidationStrict
		ctx, err := model.NewContext(bytes.NewReader(data), conf)
		if err != nil {
			return err
		}
		if err := readXRefTable(t.Context(), ctx); err != nil {
			return err
		}
		if ctx.Size == nil {
			counts["d_other"]++
			return nil
		}
		declared, highest := *ctx.Size, maxObjectNumber(ctx.Table)
		if declared > highest+1 {
			counts["a_larger"]++
			return nil
		}
		if declared <= 0 || declared == highest+1 {
			counts["d_other"]++
			return nil
		}

		if err := dereferenceXRefTable(t.Context(), ctx); err != nil {
			return err
		}
		referenced := types.IntSet{}
		addReference := func(ir types.IndirectRef) {
			n := ir.ObjectNumber.Value()
			e := ctx.Table[n]
			if e != nil && !e.Free && e.Generation != nil && *e.Generation == ir.GenerationNumber.Value() {
				referenced[n] = true
			}
		}
		for _, ir := range []*types.IndirectRef{ctx.Root, ctx.Info, ctx.Encrypt} {
			if ir != nil {
				addReference(*ir)
			}
		}
		visitSizeReferences(ctx.ID, addReference)
		visitSizeReferences(ctx.DirectInfoDict, addReference)
		for n, e := range ctx.Table {
			if e.Free {
				continue
			}
			o, err := ctx.Dereference(*types.NewIndirectRef(n, *e.Generation))
			if err != nil {
				return err
			}
			visitSizeReferences(o, addReference)
		}
		hasReferenced, hasInUse := false, false
		for n, e := range ctx.Table {
			if n < declared || e.Free {
				continue
			}
			hasInUse = true
			if n == declared {
				counts["in_use_at_size"]++
			} else {
				counts["in_use_above_size"]++
			}
			if referenced[n] {
				hasReferenced = true
			}
		}
		if hasReferenced {
			counts["b_smaller_referenced"]++
		} else {
			counts["c_smaller_unreferenced"]++
		}
		if !hasInUse {
			counts["smaller_free_only"]++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(counts)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(out))
	if counts["size_refusals"] != counts["a_larger"]+counts["b_smaller_referenced"]+counts["c_smaller_unreferenced"]+counts["d_other"] {
		t.Fatal("incomplete classification")
	}
}

func visitSizeReferences(o types.Object, add func(types.IndirectRef)) {
	switch o := o.(type) {
	case types.IndirectRef:
		add(o)
	case types.Dict:
		for _, v := range o {
			visitSizeReferences(v, add)
		}
	case types.Array:
		for _, v := range o {
			visitSizeReferences(v, add)
		}
	case types.StreamDict:
		visitSizeReferences(o.Dict, add)
	case types.ObjectStreamDict:
		visitSizeReferences(o.Dict, add)
	case types.XRefStreamDict:
		visitSizeReferences(o.Dict, add)
	}
}
