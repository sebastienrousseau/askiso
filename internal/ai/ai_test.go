package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sebastienrousseau/askiso/internal/catalog"
)

func TestAIEngine(t *testing.T) {
	idx := fixtureIndex(t)

	engine := New(idx)

	ans := engine.Query("what is the pacs008")
	if !strings.Contains(ans.Summary, "pacs.008") && !strings.Contains(ans.Details, "pacs.008") {
		t.Errorf("expected pacs.008 in response for pacs008 query, got: %s", ans.Summary)
	}

	ansComp := engine.Query("compare pacs.008 vs pacs.009")
	if !strings.Contains(ansComp.Summary, "Comparison") {
		t.Errorf("expected comparison response, got: %s", ansComp.Summary)
	}

	ans2 := engine.Query("credit transfer")
	if !strings.Contains(ans2.Details, "pacs.008") && !strings.Contains(ans2.Details, "pain.001") {
		t.Errorf("expected credit transfer details, got: %s", ans2.Details)
	}

	ans3 := engine.Query("how does camt053 work")
	if !strings.Contains(ans3.Details, "camt.053") {
		t.Errorf("expected camt.053 in statement query, got: %s", ans3.Details)
	}
}

// fixtureIndex builds a small in-memory catalogue. AskISO no longer ships
// specifications, so tests must not depend on one being installed.
func fixtureIndex(t *testing.T) *catalog.Index {
	t.Helper()
	root := t.TempDir()
	schemas := filepath.Join(root, "Payments Clearing and Settlement", "Version 11.0", "Schemas")
	if err := os.MkdirAll(schemas, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"pacs.008.001.10", "pacs.009.001.10", "pacs.002.001.12",
	} {
		if err := os.WriteFile(filepath.Join(schemas, id+".xsd"), []byte("<xs:schema/>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pain := filepath.Join(root, "Payments Initiation", "Version 13.0", "Schemas")
	if err := os.MkdirAll(pain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pain, "pain.001.001.11.xsd"), []byte("<xs:schema/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := catalog.Load(root)
	if err != nil {
		t.Fatalf("building fixture catalogue: %v", err)
	}
	return idx
}

// fixtureIndexWithLongSample writes a sample long enough to exercise the
// preview truncation in the answer renderer.
func fixtureIndexWithLongSample(t *testing.T) *catalog.Index {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "Payments Clearing and Settlement", "Version 11.0")
	schemas := filepath.Join(base, "Schemas")
	samples := filepath.Join(base, "Sample Messages")
	for _, d := range []string{schemas, samples} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(schemas, "pacs.008.001.10.xsd"),
		[]byte("<xs:schema/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("<Document>\n")
	for i := 0; i < 60; i++ {
		b.WriteString("  <Line>value</Line>\n")
	}
	b.WriteString("</Document>\n")
	if err := os.WriteFile(filepath.Join(samples, "pacs.008.001.10.xml"),
		[]byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := catalog.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestNormalizeQuery_Formats(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain question without codes", "plain question without codes"},
		{"explain pacs008 and pain_001", "explain pacs.008 and pain.001"},
		{"check pacs 008 001 10 schema", "check pacs.008.001.10 schema"},
	}
	for _, tc := range cases {
		if got := NormalizeQuery(tc.in); got != tc.want {
			t.Errorf("NormalizeQuery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
