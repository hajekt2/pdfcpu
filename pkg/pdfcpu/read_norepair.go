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

package pdfcpu

import (
	"bytes"
	"context"
	"io"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Check the declared extent without scanning for an alternative end marker.
// Restore the shared seek position before the caller reads the stream.
func checkSourceStreamExtent(c context.Context, ctx *model.Context, sd *types.StreamDict, length int) error {
	refuse := func(site model.RepairSite) error {
		if err := model.RefuseRepair(c, site); err != nil {
			return err
		}
		return ctx.XRefTable.RefuseRepair(site, -1, sd.StreamOffset)
	}
	if sd.StreamLength == nil || length < 0 {
		return refuse(model.RepairStreamScan)
	}
	if int64(length) > streamLimit(ctx) {
		return nil
	} // the ordinary resource check rejects this
	rs := ctx.Read.RS
	old, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err = rs.Seek(sd.StreamOffset+int64(length), io.SeekStart); err != nil {
		return err
	}
	var marker [11]byte // optional CR LF followed by endstream
	n, readErr := io.ReadFull(rs, marker[:])
	_, seekErr := rs.Seek(old, io.SeekStart)
	if seekErr != nil {
		return seekErr
	}
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return readErr
	}
	p := marker[:n]
	if len(p) > 0 && p[0] == '\r' {
		p = p[1:]
	}
	if len(p) > 0 && p[0] == '\n' {
		p = p[1:]
	}
	if !bytes.HasPrefix(p, []byte("endstream")) {
		if length == 0 {
			return refuse(model.RepairStreamScan)
		}
		return refuse(model.RepairStreamLength)
	}
	return nil
}

func refuseFilterAliases(c context.Context, ctx *model.Context, d types.Dict) error {
	o := d["Filter"]
	if ir, ok := o.(types.IndirectRef); ok {
		var err error
		o, err = dereferencedObject(c, ctx, ir.ObjectNumber.Value())
		if err != nil {
			return err
		}
	}
	site := model.RepairFilterName
	names := []string{}
	if name, ok := o.(types.Name); ok {
		names = append(names, name.Value())
	}
	if a, ok := o.(types.Array); ok {
		site = model.RepairFilterPipeline
		var err error
		names, err = filterNames(c, ctx, a)
		if err != nil {
			return err
		}
	}
	for _, name := range names {
		if _, alias := inlineImageFilterAliases[name]; alias {
			if err := model.RefuseRepair(c, site); err != nil {
				return err
			}
			return ctx.XRefTable.RefuseRepair(site, -1, -1)
		}
	}
	return nil
}

// ParseObjectAttributes historically drops nonnumeric prolog bytes. In NoRepair
// mode accept exactly two unsigned decimal tokens, PDF whitespace and comments.
func readObjectAttributes(c context.Context, line *string) (*int, *int, error) {
	if !model.RepairsForbidden(c) {
		return model.ParseObjectAttributes(line)
	}
	p := *line
	var numbers [2]int
	for token := range numbers {
		p = trimPDFSpaceAndComments(p)
		n := 0
		for n < len(p) && p[n] >= '0' && p[n] <= '9' {
			n++
		}
		if n == 0 || n == len(p) || (!pdfSpace(p[n]) && p[n] != '%') {
			return nil, nil, model.RefuseRepair(c, model.RepairObjectHeader)
		}
		value, err := strconv.Atoi(p[:n])
		if err != nil {
			return nil, nil, model.RefuseRepair(c, model.RepairObjectHeader)
		}
		numbers[token] = value
		p = p[n:]
	}
	p = trimPDFSpaceAndComments(p)
	if len(p) < 3 || p[:3] != "obj" || (len(p) > 3 && !pdfSpace(p[3]) && !bytes.ContainsRune([]byte("()<>[]{}/%"), rune(p[3]))) {
		return nil, nil, model.RefuseRepair(c, model.RepairObjectHeader)
	}
	*line = p[3:]
	return &numbers[0], &numbers[1], nil
}

func trimPDFSpaceAndComments(p string) string {
	for {
		p = trimPDFSpace(p)
		if len(p) == 0 || p[0] != '%' {
			return p
		}
		i := bytes.IndexAny([]byte(p), "\r\n")
		if i < 0 {
			return ""
		}
		p = p[i:]
	}
}

func pdfSpace(b byte) bool {
	return b == 0 || b == '\t' || b == '\n' || b == '\f' || b == '\r' || b == ' '
}

func trimPDFSpace(s string) string {
	for len(s) > 0 && pdfSpace(s[0]) {
		s = s[1:]
	}
	return s
}

// The low-level stream helpers also guard their actual recovery sites, so a
// changing or failing ReadSeeker cannot bypass the declared-extent preflight.
func readRepairRefusal(errs []error) error {
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
