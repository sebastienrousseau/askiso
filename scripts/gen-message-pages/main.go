// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Command gen-message-pages writes one content page per ISO 20022 message
// definition, for askiso.io.
//
// There are 2,845 of them, and each one is a query somebody types: a developer
// searching "pacs.008.001.10", an analyst asking what camt.053 is for. The
// pages exist so those searches land somewhere that answers rather than on a
// PDF behind a portal.
//
// Every fact on a generated page is derived from the embedded registry or from
// this codebase: which business area the message belongs to, which message sets
// publish it, which other versions exist, where the Registration Authority
// hosts the download, and what AskISO itself can do with it. Nothing describes
// what the message *means* — that is specification content, AskISO does not
// redistribute it, and inventing a plausible-sounding summary for 2,845
// messages would be the single fastest way to make the project untrustworthy.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sebastienrousseau/askiso/internal/generator"
	"github.com/sebastienrousseau/askiso/internal/registry"
	"github.com/sebastienrousseau/askiso/internal/swift"
	"github.com/sebastienrousseau/askiso/internal/translator"
	"github.com/sebastienrousseau/askiso/pkg/iso20022"
)

func main() {
	os.Exit(mainExit(os.Args[1:], os.Stderr))
}

func mainExit(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("gen-message-pages", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", "web/content/messages", "directory to write pages into")
	date := flags.String("date", "2026-08-25", "publication date for the front matter")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if err := run(*out, *date); err != nil {
		_, _ = fmt.Fprintf(stderr, "gen-message-pages: %v\n", err)
		return 1
	}
	return 0
}

func run(outDir, date string) error {
	reg, err := registry.Load()
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Version lineage: every message sharing a base code is a version of the
	// same definition, and "which version replaced this one" is one of the
	// questions the pages exist to answer.
	byBase := map[string][]string{}
	for _, m := range reg.Messages {
		byBase[m.BaseCode] = append(byBase[m.BaseCode], m.ID)
	}
	for k := range byBase {
		sort.Strings(byBase[k])
	}

	mtFor := mtSources()
	mxSupported := map[string]bool{}
	for _, id := range swift.SupportedMX() {
		mxSupported[id] = true
	}

	for _, m := range reg.Messages {
		page := buildPage(reg, m, byBase[m.BaseCode], mtFor[m.BaseCode],
			mxSupported[m.BaseCode], generator.HasTemplate(m.BaseCode), date)
		path := filepath.Join(outDir, m.ID+".md")
		if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
			return err
		}
	}

	// The 2,845 pages were reachable only from search and the sitemap. An index
	// is what makes the catalogue browsable by somebody who does not already
	// know the identifier they are looking for -- which is most people arriving
	// at a standards reference for the first time.
	if err := writeIndex(reg, outDir, date); err != nil {
		return fmt.Errorf("writing the message index: %w", err)
	}

	fmt.Printf("messages: %d page(s) written to %s\n", len(reg.Messages), outDir)
	return nil
}

// writeIndex builds the browsable front door for the message catalogue,
// grouped by business area because that is how the standard is organised and
// how somebody looking for "the one that moves money between banks" narrows
// down without knowing it is called pacs.008.
func writeIndex(reg *registry.Registry, outDir, date string) error {
	// Group by domain, then by base code, keeping only the latest version in
	// the headline listing: a reader wants "pacs.008", not eleven rows of it.
	byDomain := map[string]map[string][]string{}
	for _, m := range reg.Messages {
		if byDomain[m.Domain] == nil {
			byDomain[m.Domain] = map[string][]string{}
		}
		byDomain[m.Domain][m.BaseCode] = append(byDomain[m.Domain][m.BaseCode], m.ID)
	}

	domains := make([]string, 0, len(byDomain))
	for d := range byDomain {
		domains = append(domains, d)
	}
	sort.Slice(domains, func(i, j int) bool {
		// Payments first: it is what most visitors came for. Everything else
		// alphabetically, so the order is predictable rather than arbitrary.
		pi, pj := domainRank(domains[i]), domainRank(domains[j])
		if pi != pj {
			return pi < pj
		}
		return domains[i] < domains[j]
	})

	var b strings.Builder
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "name: %q\n", "AskISO")
	fmt.Fprintf(&b, "short_name: %q\n", "AskISO")
	fmt.Fprintf(&b, "title: %q\n", "ISO 20022 message reference")
	fmt.Fprintf(&b, "description: %q\n",
		fmt.Sprintf("Browse all %s ISO 20022 message definitions by business area: "+
			"every version, what replaced it, and where to get the schema.",
			commas(len(reg.Messages))))
	fmt.Fprintf(&b, "keywords: %q\n",
		"ISO 20022 message reference, ISO 20022 message types, pacs, pain, camt, "+
			"seev, sese, ISO 20022 message list")
	fmt.Fprintf(&b, "author: %q\n", "Sebastien Rousseau")
	fmt.Fprintf(&b, "date: %q\n", date)
	fmt.Fprintf(&b, "layout: %q\n", "page")
	fmt.Fprintf(&b, "language: %q\n", "en-GB")
	fmt.Fprintf(&b, "schema: %q\n", "page")
	fmt.Fprintf(&b, "changefreq: %q\n", "weekly")
	fmt.Fprintf(&b, "copyright_year: %q\n", "2026")
	fmt.Fprintf(&b, "form_origin: %q\n", "https://askiso.io")
	fmt.Fprintf(&b, "news_publication_date: %q\n", date)
	fmt.Fprintf(&b, "nav_messages: %q\n", "true")
	fmt.Fprintf(&b, "banner: %q\n", "digital-constellation")
	fmt.Fprintf(&b, "banner_alt: %q\n",
		"A network of connected points, drawn in blue and cyan.")
	fmt.Fprintf(&b, "eyebrow: %q\n", "Reference")
	fmt.Fprintf(&b, "headline: %q\n", "Every ISO 20022 message")
	fmt.Fprintf(&b, "lead: %q\n", fmt.Sprintf(
		"%s definitions across %d business areas. Find the one you need, see every "+
			"version of it, and go straight to the Registration Authority for the schema.",
		commas(len(reg.Messages)), len(domains)))
	fmt.Fprintf(&b, "---\n\n")

	fmt.Fprintf(&b, "## Find a message\n\n")
	fmt.Fprintf(&b, "If you already know the identifier, search is faster: press "+
		"<kbd>K</kbd> anywhere on the site and type it. If you do not, the business "+
		"areas below are how ISO 20022 organises the catalogue.\n\n")

	fmt.Fprintf(&b, "| Business area | Code | Definitions | Versions |\n")
	fmt.Fprintf(&b, "| --- | --- | ---: | ---: |\n")
	for _, d := range domains {
		families := byDomain[d]
		total := 0
		for _, v := range families {
			total += len(v)
		}
		fmt.Fprintf(&b, "| [%s](#%s) | `%s` | %d | %d |\n",
			iso20022.DomainName(d), d, d, len(families), total)
	}
	fmt.Fprintf(&b, "\n")

	for _, d := range domains {
		families := byDomain[d]
		bases := make([]string, 0, len(families))
		for base := range families {
			bases = append(bases, base)
		}
		sort.Strings(bases)

		fmt.Fprintf(&b, "## %s\n\n", iso20022.DomainName(d))
		total := 0
		for _, ids := range families {
			total += len(ids)
		}
		fmt.Fprintf(&b, "`%s` — %d %s, %d %s in total.\n\n",
			d, len(bases), plural(len(bases), "definition", "definitions"),
			total, plural(total, "version", "versions"))

		// One line per family, pointing at the current version. Listing all
		// fourteen versions of pacs.002 inline makes the index unreadable, and
		// the version lineage already lives on the message page itself -- which
		// is the right place for a reader who needs the one their counterparty
		// actually sends.
		for _, base := range bases {
			ids := families[base]
			sort.Strings(ids)
			latest := ids[len(ids)-1]
			// The full identifier, not the bare version number: version numbers
			// are not contiguous, so "14 versions, current is 15" reads as an
			// arithmetic mistake when it is simply how the standard numbers them.
			// Root-relative, not document-relative. GitHub Pages serves
			// /messages without redirecting to /messages/, so a bare
			// "pacs.008.001.13/" resolved against the wrong base and every link
			// on this page 404ed for anyone who arrived without the slash.
			// The identifier appeared twice on every line: once in the link
			// and once after it. On an index of 664 families that repetition
			// was 23KB of the page, and the link already goes to the version
			// it names.
			fmt.Fprintf(&b, "- [%s](/messages/%s/) — %d %s\n",
				base, latest, len(ids), plural(len(ids), "version", "versions"))
		}
		fmt.Fprintf(&b, "\n")
	}

	return os.WriteFile(filepath.Join(outDir, "index.md"), []byte(b.String()), 0o644)
}

// domainRank puts the business areas most visitors arrive for at the top. Every
// other area sorts alphabetically below them.
func domainRank(domain string) int {
	switch domain {
	case "pacs":
		return 0
	case "pain":
		return 1
	case "camt":
		return 2
	case "head":
		return 3
	}
	return 4
}

// mtSources reports, for each MX base code, the MT messages that convert into
// it. It is inverted from the translator's own mapping table rather than
// hand-maintained here, so a conversion added there appears on these pages
// without anyone remembering to update a second list.
func mtSources() map[string][]translator.Mapping {
	out := map[string][]translator.Mapping{}
	for _, m := range translator.GetAllMappings() {
		base := baseCode(m.MXCode)
		if base == "" {
			continue
		}
		out[base] = append(out[base], m)
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool { return out[k][i].MTCode < out[k][j].MTCode })
	}
	return out
}

// baseCode reduces "pacs.008.001.10" to "pacs.008".
func baseCode(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

func buildPage(reg *registry.Registry, m registry.Message, versions []string,
	mtSrc []translator.Mapping, mxToMT, hasTemplate bool, date string) string {

	domain := iso20022.DomainName(m.Domain)
	sets := reg.SetsFor(m.ID)

	// Search results truncate a title past roughly 60 characters and a
	// description past roughly 160, so both are built to fit rather than
	// written long and cut off by the engine.
	title := fmt.Sprintf("%s — ISO 20022 message definition", m.ID)
	desc := fmt.Sprintf(
		"%s is an ISO 20022 message in the %s area. Validate, lint and generate "+
			"it with AskISO, and get the schema from the Registration Authority.",
		m.ID, domain)
	if len(desc) > 160 {
		desc = fmt.Sprintf(
			"%s is an ISO 20022 message definition. Validate, lint and generate it "+
				"with AskISO, and get the schema from the Registration Authority.", m.ID)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "name: %q\n", "AskISO")
	fmt.Fprintf(&b, "short_name: %q\n", "AskISO")
	fmt.Fprintf(&b, "title: %q\n", title)
	fmt.Fprintf(&b, "description: %q\n", desc)
	fmt.Fprintf(&b, "keywords: %q\n", strings.Join([]string{
		m.ID, m.BaseCode, m.Domain, "ISO 20022", domain,
		m.BaseCode + " schema", m.BaseCode + " xsd", "validate " + m.BaseCode,
	}, ", "))
	fmt.Fprintf(&b, "author: %q\n", "Sebastien Rousseau")
	fmt.Fprintf(&b, "date: %q\n", date)
	fmt.Fprintf(&b, "layout: %q\n", "page")
	fmt.Fprintf(&b, "language: %q\n", "en-GB")
	fmt.Fprintf(&b, "schema: %q\n", "page")
	fmt.Fprintf(&b, "changefreq: %q\n", "monthly")
	fmt.Fprintf(&b, "copyright_year: %q\n", "2026")
	fmt.Fprintf(&b, "form_origin: %q\n", "https://askiso.io")
	// Without this the news-sitemap generator warns once per page and falls
	// back to the build time, which would date all 2,845 pages to whenever CI
	// last ran rather than to when they were published.
	fmt.Fprintf(&b, "news_publication_date: %q\n", date)
	fmt.Fprintf(&b, "eyebrow: %q\n", domain)
	fmt.Fprintf(&b, "headline: %q\n", m.ID)
	fmt.Fprintf(&b, "lead: %q\n", fmt.Sprintf(
		"Version %s of %s, in the %s business area.", versionOf(m.ID), m.BaseCode, domain))
	fmt.Fprintf(&b, "---\n\n")

	// --- what is it -------------------------------------------------------
	fmt.Fprintf(&b, "## What %s is\n\n", m.ID)
	fmt.Fprintf(&b, "`%s` is an ISO 20022 message definition. Its business area is "+
		"**%s** (`%s`), and `%s` is the definition it versions.\n\n",
		m.ID, domain, m.Domain, m.BaseCode)
	// Short sentences here matter more than anywhere else on the site: this
	// paragraph is repeated on 2,845 pages, so its reading level is effectively
	// the site's reading level.
	fmt.Fprintf(&b, "AskISO does not copy the specification. The Registration "+
		"Authority publishes the message definition report and the schema, free of "+
		"charge. The links below go straight there.\n\n")

	// --- how this version sits in its family ------------------------------
	//
	// Written from the registry rather than from a fixed template, so each page
	// says something true about its own message instead of repeating the same
	// paragraph 2,845 times. It is also the prose that makes these pages worth
	// reading: an identifier, a table and four commands answer "what is this",
	// but not "is this the one I should be sending".
	if len(versions) > 1 {
		latest := versions[len(versions)-1]
		switch m.ID {
		case latest:
			fmt.Fprintf(&b, "This is the newest published version of `%s`, out of %d "+
				"in total. Older versions remain valid, and plenty of institutions "+
				"still send them, so receiving one is normal rather than a problem.\n\n",
				m.BaseCode, len(versions))
		default:
			fmt.Fprintf(&b, "This is one of %d published versions of `%s`, and it is "+
				"not the newest. The current version is [`%s`](/messages/%s/). That "+
				"does not make this page obsolete: a counterparty running an older "+
				"integration may still send you exactly this version, and you will "+
				"need to read it.\n\n", len(versions), m.BaseCode, latest, latest)
		}
	}

	// --- versions ---------------------------------------------------------
	if len(versions) > 1 {
		fmt.Fprintf(&b, "## Versions of %s\n\n", m.BaseCode)
		fmt.Fprintf(&b, "The standard publishes %d versions of this definition. A "+
			"newer version does not automatically replace an older one. The scheme "+
			"or market infrastructure you send to decides which version you should "+
			"be using.\n\n", len(versions))
		for _, v := range versions {
			marker := ""
			if v == m.ID {
				marker = " — this page"
			}
			fmt.Fprintf(&b, "- [`%s`](/messages/%s/)%s\n", v, v, marker)
		}
		fmt.Fprintf(&b, "\n")
	}

	// --- where to get it --------------------------------------------------
	fmt.Fprintf(&b, "## Where to get the schema\n\n")
	if len(sets) == 0 {
		fmt.Fprintf(&b, "The registry records no message set publishing this definition.\n\n")
	} else {
		fmt.Fprintf(&b, "`%s` is published in %d %s. Download from the Registration Authority, "+
			"then import:\n\n", m.ID, len(sets),
			plural(len(sets), "message set", "message sets"))
		fmt.Fprintf(&b, "```bash\naskiso catalog fetch %s\n```\n\n", m.BaseCode)
		fmt.Fprintf(&b, "| Message set | Version | Download |\n| :--- | :--- | :--- |\n")
		for _, s := range sets {
			fmt.Fprintf(&b, "| %s | %s | [iso20022.org](%s) |\n", s.Name, s.Version, s.URL)
		}
		fmt.Fprintf(&b, "\n")
	}

	// --- what askiso does with it ----------------------------------------
	fmt.Fprintf(&b, "## What AskISO does with %s\n\n", m.ID)
	fmt.Fprintf(&b, "```bash\n")
	// The comment column is aligned on the longest command so the block reads
	// as a table rather than as ragged output.
	cmds := [][2]string{
		{"askiso info " + m.ID, "metadata and schema paths"},
		{"askiso validate message.xml", "full XSD validation, needs the schema"},
		{"askiso lint message.xml", "business rules, needs no schema"},
	}
	if hasTemplate {
		cmds = append(cmds, [2]string{
			"askiso generate " + m.BaseCode, "from a template, needs no schema"})
	} else {
		cmds = append(cmds, [2]string{
			"askiso generate " + m.ID + " --from-schema", "walks the schema"})
	}
	width := 0
	for _, c := range cmds {
		if len(c[0]) > width {
			width = len(c[0])
		}
	}
	for _, c := range cmds {
		fmt.Fprintf(&b, "%-*s  # %s\n", width, c[0], c[1])
	}
	fmt.Fprintf(&b, "```\n\n")

	if hasTemplate {
		fmt.Fprintf(&b, "This message has a hand-written template with rail-aware defaults, "+
			"so a sample can be generated with no catalogue installed.\n\n")
	}

	// --- MT relationship --------------------------------------------------
	if len(mtSrc) > 0 || mxToMT {
		fmt.Fprintf(&b, "## SWIFT MT equivalence\n\n")
		if len(mtSrc) > 0 {
			fmt.Fprintf(&b, "%s converts into `%s`:\n\n",
				plural(len(mtSrc), "One MT message", "These MT messages"), m.BaseCode)
			for _, mt := range mtSrc {
				fmt.Fprintf(&b, "- **%s** — %s. %s\n", mt.MTCode, mt.MTTitle, mt.Description)
			}
			fmt.Fprintf(&b, "\n```bash\naskiso translate payment.mt%s\n```\n\n",
				strings.TrimPrefix(mtSrc[0].MTCode, "MT"))
		}
		if mxToMT {
			fmt.Fprintf(&b, "`%s` also converts back to MT. Both directions carry a fidelity "+
				"report naming every field that was mapped, derived, truncated or lost, "+
				"because conversion between the two is lossy in both directions.\n\n",
				m.BaseCode)
		}
	}

	// --- FAQ --------------------------------------------------------------
	// Answer-shaped, because that is the shape a search engine or an assistant
	// lifts into a result. Each answer is a fact this repository can stand
	// behind, not a paraphrase of a specification nobody here is licensed to
	// paraphrase.
	fmt.Fprintf(&b, "## Questions\n\n")
	fmt.Fprintf(&b, "### What business area does %s belong to?\n\n", m.ID)
	fmt.Fprintf(&b, "%s (`%s`).\n\n", domain, m.Domain)

	fmt.Fprintf(&b, "### How do I validate a %s message?\n\n", m.BaseCode)
	fmt.Fprintf(&b, "Run `askiso validate message.xml`. The schema is found from "+
		"the document's own namespace, so you do not name it. Validation needs that "+
		"schema installed. To check the business rules without one, run "+
		"`askiso lint` instead.\n\n")

	fmt.Fprintf(&b, "### Does AskISO include the %s schema?\n\n", m.ID)
	fmt.Fprintf(&b, "No. AskISO ships no ISO 20022 specification content at all. "+
		"You download the message set from the Registration Authority, then import it "+
		"with `askiso catalog add`. The binary carries only an index: what exists, and "+
		"where to find it.\n\n")

	if len(versions) > 1 {
		fmt.Fprintf(&b, "### Which version of %s should I send?\n\n", m.BaseCode)
		fmt.Fprintf(&b, "The scheme or market infrastructure you send to decides "+
			"that, not the standard. There are %d published versions. Run "+
			"`askiso diff <from> <to>` to see every structural difference between two "+
			"of them, marked as breaking or compatible.\n\n", len(versions))
	}

	// --- Related ------------------------------------------------------------
	// These 2,845 pages linked only to each other, which made the largest set of
	// entities on the site an island: a reader arriving from a search for one
	// message identifier had no route to what the tool does with it, and nothing
	// tied the message to the rules that govern it. The links are chosen from
	// what is true of this message rather than pasted onto every page — a
	// payments message gets the address rule, one with an MT counterpart gets
	// the conversion, and neither appears where it does not apply.
	fmt.Fprintf(&b, "## Related\n\n")
	fmt.Fprintf(&b, "- [Check a %s message](/workspace/) — paste one and see every "+
		"finding, with the rule and the path behind it\n", m.BaseCode)
	if governedByAddressRule(m.Domain, m.BaseCode) {
		fmt.Fprintf(&b, "- [Structured addresses](/deadline/) — the CBPR+ rule that "+
			"governs every address in a %s, and where its timing now stands\n", m.BaseCode)
	}
	if len(mtSrc) > 0 || mxToMT {
		fmt.Fprintf(&b, "- [Converting between MT and ISO 20022](/solutions/) — what "+
			"survives the trip in each direction, and what does not\n")
	}
	fmt.Fprintf(&b, "- [All %s messages](/messages/) — the rest of the %s area, and "+
		"every other ISO 20022 message\n", domain, domain)
	fmt.Fprintf(&b, "- [Documentation](/docs/) — installing AskISO, and every command "+
		"it offers\n\n")

	fmt.Fprintf(&b, "---\n\n")
	fmt.Fprintf(&b, "*AskISO generates this page from its built-in index of the "+
		"standard. The source of truth for ISO 20022 is "+
		"[iso20022.org](https://www.iso20022.org/).*\n")

	return b.String()
}

func versionOf(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// commas renders 2845 as "2,845". Large counts are a claim about scale, and a
// claim about scale that is hard to read at a glance undersells itself.
func commas(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// addressRuleExempt lists the message identifiers Swift names as outside the
// structured address requirement. Four of the six are camt, so a rule applied by
// domain alone points four of the most-read cash management pages at a briefing
// that does not govern them.
//
// https://www.swift.com/news-events/news/iso-20022-milestone-november-2026-unstructured-addresses-be-removed
var addressRuleExempt = map[string]bool{
	"admi.024": true,
	"camt.025": true,
	"camt.052": true,
	"camt.053": true,
	"camt.054": true,
	"camt.060": true,
}

// governedByAddressRule reports whether the CBPR+ structured address
// requirement reaches this message. pacs is interbank settlement, pain is
// customer initiation, camt is the cash management that reports on both; the
// securities and card domains carry addresses the rule does not reach, and six
// identifiers are exempt by name.
func governedByAddressRule(domain, baseCode string) bool {
	if addressRuleExempt[baseCode] {
		return false
	}
	switch domain {
	case "pacs", "pain", "camt":
		return true
	}
	return false
}
