// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package codes

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/sebastienrousseau/askiso/internal/catalog"
)

// Most ISO 20022 code sets are enumerated inside the schemas themselves. Reading
// them from the catalogue the user downloaded gives exact, complete coverage
// without AskISO redistributing anything: the codes come from their own copy of
// the specification.
//
// The remaining sets are "external" -- maintained separately by the Registration
// Authority on a quarterly cycle and referenced by name only. Those are not in
// the schemas and must be synced separately.

// SchemaCode is one enumerated value read from a schema.
type SchemaCode struct {
	Code        string   `json:"code"`
	Set         string   `json:"set"`         // e.g. "ChargeBearerType1Code"
	Description string   `json:"description"` // from xs:documentation, when present
	Messages    []string `json:"messages"`    // message identifiers that use the set
}

// SchemaIndex holds every code set found in a catalogue.
type SchemaIndex struct {
	// Sets maps a code-set name to its members, keyed by code.
	Sets map[string]map[string]*SchemaCode
	// Codes counts distinct code values.
	Codes int
	// Source is the catalogue the index was built from.
	Source string
}

// Total reports how many code sets were found.
func (idx *SchemaIndex) Total() int {
	if idx == nil {
		return 0
	}
	return len(idx.Sets)
}

// Lookup finds every occurrence of a code across all sets.
func (idx *SchemaIndex) Lookup(code string) []SchemaCode {
	if idx == nil {
		return nil
	}
	want := strings.ToUpper(strings.TrimSpace(code))

	var out []SchemaCode
	for _, members := range idx.Sets {
		if c, ok := members[want]; ok {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Set < out[j].Set })
	return out
}

// Search matches a code, a set name, or descriptive text.
func (idx *SchemaIndex) Search(query string) []SchemaCode {
	if idx == nil {
		return nil
	}
	q := strings.ToUpper(strings.TrimSpace(query))
	if q == "" {
		return nil
	}

	var exact, partial []SchemaCode
	for setName, members := range idx.Sets {
		setUpper := strings.ToUpper(setName)
		for code, c := range members {
			switch {
			case code == q:
				exact = append(exact, *c)
			case strings.Contains(code, q) ||
				strings.Contains(setUpper, q) ||
				strings.Contains(strings.ToUpper(c.Description), q):
				partial = append(partial, *c)
			}
		}
	}

	sortCodes(exact)
	sortCodes(partial)
	return append(exact, partial...)
}

// Set returns the members of one code set, sorted.
func (idx *SchemaIndex) Set(name string) []SchemaCode {
	if idx == nil {
		return nil
	}
	members, ok := idx.Sets[name]
	if !ok {
		// Try a case-insensitive match, so "chargebearertype1code" works.
		for k, v := range idx.Sets {
			if strings.EqualFold(k, name) {
				members = v
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil
	}

	out := make([]SchemaCode, 0, len(members))
	for _, c := range members {
		out = append(out, *c)
	}
	sortCodes(out)
	return out
}

// SetNames lists every code set found, sorted.
func (idx *SchemaIndex) SetNames() []string {
	if idx == nil {
		return nil
	}
	out := make([]string, 0, len(idx.Sets))
	for name := range idx.Sets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortCodes(list []SchemaCode) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Code != list[j].Code {
			return list[i].Code < list[j].Code
		}
		return list[i].Set < list[j].Set
	})
}

// BuildIndex reads every schema in a catalogue and extracts its code sets.
//
// Scanning several thousand schemas takes a moment, so callers that need it more
// than once should hold on to the result; LoadIndex caches per catalogue root.
func BuildIndex(idx *catalog.Index) (*SchemaIndex, error) {
	if idx == nil {
		return nil, fmt.Errorf("no catalogue")
	}

	out := &SchemaIndex{Sets: map[string]map[string]*SchemaCode{}, Source: idx.RootDir}

	// One schema per path is enough; several message identifiers can share one.
	type job struct{ path, msgID string }
	seen := map[string]bool{}
	var jobs []job
	for _, m := range idx.Messages {
		if m.XSDPath == "" || seen[m.XSDPath] {
			continue
		}
		seen[m.XSDPath] = true
		jobs = append(jobs, job{m.XSDPath, m.ID})
	}

	// Several thousand schemas is enough work that a serial scan is noticeable
	// on the command line, and each file is independent.
	workers := runtime.NumCPU()
	if workers > len(jobs) {
		workers = len(jobs)
	}
	if workers < 1 {
		workers = 1
	}

	partials := make([]*SchemaIndex, workers)
	jobCh := make(chan job)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			local := &SchemaIndex{Sets: map[string]map[string]*SchemaCode{}}
			for j := range jobCh {
				// A single unreadable schema must not abort the whole scan.
				_ = extractInto(local, j.path, j.msgID)
			}
			partials[slot] = local
		}(w)
	}
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)
	wg.Wait()

	for _, part := range partials {
		if part == nil {
			continue
		}
		mergeInto(out, part)
	}

	for _, members := range out.Sets {
		out.Codes += len(members)
	}
	return out, nil
}

// mergeInto folds one worker's partial index into the result.
func mergeInto(dst, src *SchemaIndex) {
	for setName, members := range src.Sets {
		target, ok := dst.Sets[setName]
		if !ok {
			target = map[string]*SchemaCode{}
			dst.Sets[setName] = target
		}
		for key, c := range members {
			existing, ok := target[key]
			if !ok {
				copied := *c
				target[key] = &copied
				continue
			}
			if existing.Description == "" {
				existing.Description = c.Description
			}
			for _, m := range c.Messages {
				if len(existing.Messages) >= 8 {
					break
				}
				if !contains(existing.Messages, m) {
					existing.Messages = append(existing.Messages, m)
				}
			}
		}
	}
}

// extractInto pulls enumerations out of one schema.
func extractInto(idx *SchemaIndex, path, msgID string) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	dec := xml.NewDecoder(f)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	w := &schemaWalker{idx: idx, msgID: msgID}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		w.token(tok)
	}
	return nil
}

// schemaWalker carries what extractInto has seen so far in one schema: the
// code set it is inside, the code awaiting a description, and that text.
type schemaWalker struct {
	idx             *SchemaIndex
	msgID           string
	currentSet      string
	pendingCode     string
	inDocumentation bool
	docText         strings.Builder
}

// token advances the walk by one XML token.
func (w *schemaWalker) token(tok xml.Token) {
	switch t := tok.(type) {
	case xml.StartElement:
		w.start(t)
	case xml.CharData:
		if w.inDocumentation {
			w.docText.Write(t)
		}
	case xml.EndElement:
		w.end(t)
	}
}

// start opens a code set, records an enumeration in it, or begins collecting
// the description of the pending code.
func (w *schemaWalker) start(t xml.StartElement) {
	switch t.Name.Local {
	case "simpleType":
		w.currentSet = ""
		name := attrValue(t, "name")
		if strings.HasSuffix(name, "Code") {
			w.currentSet = name
		}
	case "enumeration":
		if w.currentSet == "" {
			return
		}
		w.pendingCode = attrValue(t, "value")
		w.docText.Reset()
		addCode(w.idx, w.currentSet, w.pendingCode, "", w.msgID)
	case "documentation":
		w.inDocumentation = w.pendingCode != ""
	}
}

// end records a finished description and closes the code or set it ends.
func (w *schemaWalker) end(t xml.EndElement) {
	switch t.Name.Local {
	case "documentation":
		if w.inDocumentation && w.pendingCode != "" {
			if desc := strings.Join(strings.Fields(w.docText.String()), " "); desc != "" {
				addCode(w.idx, w.currentSet, w.pendingCode, desc, w.msgID)
			}
		}
		w.inDocumentation = false
	case "enumeration":
		w.pendingCode = ""
	case "simpleType":
		w.currentSet = ""
		w.pendingCode = ""
	}
}

// addCode records a code, keeping the first description found and accumulating
// the message types that reference the set.
func addCode(idx *SchemaIndex, set, code, description, msgID string) {
	if set == "" || code == "" {
		return
	}
	members, ok := idx.Sets[set]
	if !ok {
		members = map[string]*SchemaCode{}
		idx.Sets[set] = members
	}

	key := strings.ToUpper(code)
	c, ok := members[key]
	if !ok {
		c = &SchemaCode{Code: code, Set: set}
		members[key] = c
	}
	if c.Description == "" && description != "" {
		c.Description = description
	}
	if base := baseMessageCode(msgID); base != "" && !contains(c.Messages, base) {
		// Cap the list: a common code set appears in hundreds of messages and
		// printing them all helps nobody.
		if len(c.Messages) < 8 {
			c.Messages = append(c.Messages, base)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// baseMessageCode reduces "pacs.008.001.10" to "pacs.008".
func baseMessageCode(msgID string) string {
	parts := strings.Split(msgID, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return msgID
}

func attrValue(start xml.StartElement, name string) string {
	for _, a := range start.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

var (
	indexOnce  sync.Mutex
	indexCache = map[string]*SchemaIndex{}
)

// LoadIndex builds, or returns a cached, code-set index for a catalogue.
func LoadIndex(idx *catalog.Index) (*SchemaIndex, error) {
	if idx == nil {
		return nil, fmt.Errorf("no catalogue")
	}

	indexOnce.Lock()
	defer indexOnce.Unlock()

	key := filepath.Clean(idx.RootDir)
	if cached, ok := indexCache[key]; ok {
		return cached, nil
	}
	built, err := BuildIndex(idx)
	if err != nil {
		return nil, err
	}
	indexCache[key] = built
	return built, nil
}
