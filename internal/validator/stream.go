// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package validator

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/sebastienrousseau/askiso/internal/codes"
	"github.com/sebastienrousseau/askiso/internal/xsd"
)

// Validate reads the whole document into memory, which is fine for a payment
// and wrong for a statement. A camt.053 covering a corporate's month can run to
// gigabytes, and a tool that needs all of it resident is a tool nobody can put
// in a nightly job.
//
// ValidateReader validates the same documents without holding them. The insight
// is that a content model only needs its children's names, in order -- not their
// subtrees. So each repeating transaction is validated as it completes and then
// released, leaving behind a stub carrying its name and position. The parent's
// own content model is checked against those stubs at the end, and the verdict
// is identical to Validate's.
//
// The cost is a small fixed amount per transaction -- about 120 bytes, measured
// by TestStreamingHeapDoesNotGrowWithTransactionSize -- rather than the
// transaction itself. It is the count that matters and not the size: a
// statement whose entries carry full remittance data costs the same as one
// whose entries carry none, and neither costs anything like the document.
//
// This is asserted, not assumed: TestStreamingAgreesWithValidate runs both over
// every sample message in an installed catalogue and requires the same result.

// StreamDepth is the depth at which subtrees are released. Depth 0 is the
// document element, 1 its single wrapper (FIToFICstmrCdtTrf, BkToCstmrStmt),
// and 2 the repeating transactions -- which is where the volume is.
const StreamDepth = 2

// ValidateReader checks an XML instance read from r against a schema.
//
// The verdict matches Validate's. Memory is bounded by the largest single
// element below StreamDepth, so a statement with a million entries costs the
// same as one with ten.
func ValidateReader(r io.Reader, schema *xsd.Schema) *Result {
	return ValidateReaderWithExternalSets(r, schema, nil)
}

// ValidateReaderWithExternalSets is the bounded-memory validator with locally
// imported Registration Authority code-set enforcement enabled.
func ValidateReaderWithExternalSets(r io.Reader, schema *xsd.Schema, external *codes.ExternalSets) *Result {
	res := &Result{Valid: true}
	v := &validation{schema: schema, res: res, external: external}

	root, err := streamParse(r, schema, v)
	if err != nil {
		res.Valid = false
		res.Errors = append(res.Errors, Error{
			Path:    "/",
			Rule:    "well-formedness",
			Message: err.Error(),
		})
		return res
	}

	decl, ok := schema.Elements[root.Name]
	if !ok {
		names := make([]string, 0, len(schema.Elements))
		for n := range schema.Elements {
			names = append(names, n)
		}
		v.fail(root, "/"+root.Name, "root element", joinNames(names), root.Name,
			fmt.Sprintf("no global element named %q is declared in this schema", root.Name))
		res.Valid = false
		return res
	}

	if schema.TargetNamespace != "" && root.Space != schema.TargetNamespace {
		found := root.Space
		if found == "" {
			found = "(none)"
		}
		v.fail(root, "/"+root.Name, "namespace", schema.TargetNamespace, found,
			"the document is in the wrong namespace for this schema")
	}

	v.element(decl, root, "/"+root.Name)

	// Errors from released subtrees are recorded as those subtrees close, and
	// errors in the surrounding structure only afterwards. Sorting by position
	// puts them back into document order, which is the order Validate reports
	// them in and the order a reader expects.
	sort.SliceStable(res.Errors, func(i, j int) bool {
		if res.Errors[i].Line != res.Errors[j].Line {
			return res.Errors[i].Line < res.Errors[j].Line
		}
		return res.Errors[i].Column < res.Errors[j].Column
	})

	res.Valid = len(res.Errors) == 0
	return res
}

var streamBufReaderPool = sync.Pool{
	New: func() any {
		return bufio.NewReaderSize(nil, 64<<10)
	},
}

// streamParse builds the document tree, validating and discarding each subtree
// at StreamDepth as it closes.
func streamParse(r io.Reader, schema *xsd.Schema, v *validation) (*node, error) {
	br := streamBufReaderPool.Get().(*bufio.Reader)
	br.Reset(r)
	defer func() {
		br.Reset(nil)
		streamBufReaderPool.Put(br)
	}()
	lines := newLineTracker(br)
	dec := xml.NewDecoder(lines)
	dec.Strict = true
	// Reject DTD entity references outright, exactly as the buffered parser
	// does: encoding/xml never fetches external entities, and this stops
	// internal ones being expanded too.
	dec.Entity = xml.HTMLEntity

	// Element names and the namespace repeat on every element in the document.
	// encoding/xml allocates a fresh string for each, which on a statement with
	// a million entries is millions of small allocations that are all equal.
	// Interning them costs one map of a few hundred entries.
	intern := map[string]string{}
	keep := func(s string) string {
		if got, ok := intern[s]; ok {
			return got
		}
		intern[s] = s
		return s
	}

	var root *node
	var stack []*node
	// paths[i] is the element path of stack[i], built once rather than joined
	// again for every child.
	var paths []string

	for {
		offset := dec.InputOffset()
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			line, col := lines.at(offset)
			n := &node{
				Name:   keep(t.Name.Local),
				Space:  keep(t.Name.Space),
				Attrs:  append([]xml.Attr(nil), t.Attr...),
				Line:   line,
				Column: col,
			}

			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("more than one root element")
				}
				root = n
				paths = append(paths, "/"+n.Name)
			} else {
				parent := stack[len(stack)-1]
				// Repeated siblings are indexed, exactly as the buffered
				// validator indexes them, so the two report the same paths.
				repeat := consecutiveRun(parent.Children, n.Name)
				parent.Children = append(parent.Children, n)

				child := paths[len(paths)-1] + "/" + n.Name
				if repeat > 0 {
					child = fmt.Sprintf("%s/%s[%d]", paths[len(paths)-1], n.Name, repeat+1)
				}
				paths = append(paths, child)
			}
			stack = append(stack, n)

		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			depth := len(stack) - 1
			closing := stack[depth]
			path := paths[depth]

			stack = stack[:depth]
			paths = paths[:depth]

			if depth != StreamDepth || v.tooMany() {
				continue
			}
			// The subtree is complete. Validate it now against its declaration,
			// then drop its children so the memory goes back.
			trimText(closing)
			if decl, ok := declarationAt(schema, path); ok {
				v.element(decl, closing, path)
			}
			// Keep only what the parent's content model needs: the name, so the
			// sequence can be matched, and the position, so an error about the
			// sequence can be located. Everything else goes.
			closing.Children = nil
			closing.Attrs = nil
			closing.Text = ""
			closing.done = true

		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			current := stack[len(stack)-1]
			// ISO 20022 has no mixed content, so whitespace between child
			// elements is formatting rather than a value. The buffered parser
			// trims it once at the end; a streaming one has to drop it as it
			// goes, or the wrapper element accumulates one newline per
			// transaction and grows with the file.
			if len(current.Children) > 0 && isSpaceOnly(t) {
				continue
			}
			current.Text += string(t)
		}
	}

	if root == nil {
		return nil, fmt.Errorf("document has no elements")
	}
	trimText(root)
	return root, nil
}

// isSpaceOnly reports whether a chunk of character data is only whitespace.
func isSpaceOnly(data []byte) bool {
	for _, c := range data {
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			return false
		}
	}
	return true
}

// consecutiveRun counts how many siblings immediately before the end of the
// list share a name. The buffered validator numbers a repeated element by its
// position within an unbroken run, and this reproduces that.
func consecutiveRun(siblings []*node, name string) int {
	run := 0
	for i := len(siblings) - 1; i >= 0; i-- {
		if siblings[i].Name != name {
			break
		}
		run++
	}
	return run
}

// declarationAt resolves an instance path to the element declaration that
// governs it, so a released subtree can be checked before it is dropped.
func declarationAt(schema *xsd.Schema, path string) (*xsd.Element, bool) {
	names := splitPath(path)
	if len(names) == 0 {
		return nil, false
	}

	current, ok := schema.Elements[names[0]]
	if !ok {
		return nil, false
	}
	for _, name := range names[1:] {
		ct, ok := schema.ResolveComplex(current.Type)
		if !ok || ct.Content == nil {
			return nil, false
		}
		next, ok := lookupParticle(ct.Content, name)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

// lookupParticle finds a named element among a content model's alternatives.
func lookupParticle(p xsd.Particle, name string) (*xsd.Element, bool) {
	switch t := p.(type) {
	case *xsd.Element:
		if t.Name == name {
			return t, true
		}
	case *xsd.Sequence:
		for _, c := range t.Particles {
			if el, ok := lookupParticle(c, name); ok {
				return el, true
			}
		}
	case *xsd.Choice:
		for _, c := range t.Particles {
			if el, ok := lookupParticle(c, name); ok {
				return el, true
			}
		}
	}
	return nil, false
}

// splitPath turns "/Document/Tx[2]/Amt" into its element names, dropping the
// occurrence indexes: a declaration is the same whichever repeat it governs.
func splitPath(path string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			if i > start {
				name := path[start:i]
				if b := indexByte(name, '['); b >= 0 {
					name = name[:b]
				}
				out = append(out, name)
			}
			start = i + 1
		}
	}
	return out
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// lineTracker counts newlines as bytes flow to the decoder, so a streaming run
// still reports real line and column numbers.
//
// Keeping every line start would grow with the file, which is the thing this
// whole path exists to avoid. It is not needed: the decoder reads ahead by at
// most its buffer, so an error is always reported for an offset within the last
// few thousand lines. A ring of that size is enough, and it costs a fixed
// amount however long the document is.
type lineTracker struct {
	r io.Reader
	// starts is a ring of recent line-start offsets.
	starts []int64
	// first is the line number of starts[head], one-based.
	first int
	head  int
	count int
	pos   int64
}

// lineWindow is how many recent line starts are kept. The decoder buffers 64
// KiB; ISO 20022 lines are far shorter than eight bytes apart, so this window
// always covers the read-ahead.
const lineWindow = 16384

func newLineTracker(r io.Reader) *lineTracker {
	lt := &lineTracker{r: r, starts: make([]int64, lineWindow), first: 1}
	// Line 1 starts at offset 0.
	lt.starts[0] = 0
	lt.count = 1
	return lt
}

func (lt *lineTracker) Read(p []byte) (int, error) {
	n, err := lt.r.Read(p)
	for i := 0; i < n; i++ {
		if p[i] != '\n' {
			continue
		}
		lt.push(lt.pos + int64(i) + 1)
	}
	lt.pos += int64(n)
	return n, err
}

func (lt *lineTracker) push(offset int64) {
	if lt.count < lineWindow {
		lt.starts[(lt.head+lt.count)%lineWindow] = offset
		lt.count++
		return
	}
	// The window is full: the oldest line start scrolls off.
	lt.starts[lt.head] = offset
	lt.head = (lt.head + 1) % lineWindow
	lt.first++
}

// at reports a one-based line and column for an offset.
func (lt *lineTracker) at(offset int64) (int, int) {
	// The last line start at or before the offset, found by walking back from
	// the newest: an error is reported close to where the decoder is reading.
	for i := lt.count - 1; i >= 0; i-- {
		start := lt.starts[(lt.head+i)%lineWindow]
		if start <= offset {
			return lt.first + i, int(offset-start) + 1
		}
	}
	// Older than the window. Reporting the offset is honest; claiming a line
	// number that is not known would not be.
	return 0, int(offset) + 1
}

// indexByte is strings.IndexByte, kept local so this file adds no import for
// one call.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
