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

// Added in the hajekt2/pdfcpu fork: opt-in refusal of reader repairs.

// Read each PDF with default strict loading and NoRepair strict loading.
// Materialize every nonfree object to include lazy parser refusals.
// Usage: go run ./_scripts/norepair-corpus.go /path/to/corpus

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func read(data []byte, noRepair bool) error {
	conf := model.NewStatelessConfiguration()
	conf.ValidationMode = model.ValidationStrict
	conf.NoRepair = noRepair
	ctx, err := pdfcpu.Read(context.Background(), bytes.NewReader(data), conf)
	if err != nil {
		return err
	}
	keys := make([]int, 0, len(ctx.Table))
	for n := range ctx.Table {
		keys = append(keys, n)
	}
	sort.Ints(keys)
	for _, n := range keys {
		e := ctx.Table[n]
		if e.Free {
			continue
		}
		if _, err := ctx.Dereference(*types.NewIndirectRef(n, *e.Generation)); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: norepair-corpus <directory>")
		os.Exit(2)
	}
	counts := map[string]int{}
	sites := map[model.RepairSite]int{}
	err := filepath.WalkDir(os.Args[1], func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".pdf") {
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		counts["files"]++
		legacy := read(data, false)
		if legacy == nil {
			counts["default_ok"]++
		} else {
			counts["default_error"]++
		}
		e = read(data, true)
		if e == nil {
			counts["norepair_ok"]++
			if legacy != nil {
				counts["norepair_only_ok"]++
			}
		} else {
			var refused *model.RepairRefusedError
			if errors.As(e, &refused) {
				counts["refused"]++
				sites[refused.Site]++
			} else {
				counts["norepair_other_error"]++
			}
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	out, _ := json.Marshal(struct {
		Counts map[string]int
		Sites  map[model.RepairSite]int
	}{counts, sites})
	fmt.Println(string(out))
}
