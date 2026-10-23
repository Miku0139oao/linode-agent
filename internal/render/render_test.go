package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFieldNestedAndIndex(t *testing.T) {
	obj := map[string]any{
		"id":     float64(1),
		"ipv4":   []any{"192.0.2.10", "10.0.0.2"},
		"entity": map[string]any{"label": "web1", "type": "linode"},
	}
	if got := Field(obj, "entity.label"); got != "web1" {
		t.Fatalf("entity.label = %v", got)
	}
	if got := Field(obj, "ipv4.1"); got != "10.0.0.2" {
		t.Fatalf("ipv4.1 = %v", got)
	}
	if got := Field(obj, "ipv4.9"); got != nil {
		t.Fatalf("out of range = %v", got)
	}
	if got := Field(obj, "missing.path"); got != nil {
		t.Fatalf("missing = %v", got)
	}
}

// Project stores dotted keys literally; Field must prefer the literal key.
func TestFieldPrefersLiteralDottedKey(t *testing.T) {
	row := map[string]any{"entity.label": "web1", "entity": map[string]any{"label": "other"}}
	if got := Field(row, "entity.label"); got != "web1" {
		t.Fatalf("literal key lost: %v", got)
	}
	projected := Project([]map[string]any{row}, []string{"entity.label"})
	if projected[0]["entity.label"] != "web1" {
		t.Fatalf("project = %v", projected[0])
	}
	// Round-trip through Table: a projected row must still render its value.
	var buf bytes.Buffer
	if err := Table(&buf, projected, []string{"entity.label"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "web1") {
		t.Fatalf("table = %q", buf.String())
	}
}

func TestColumnTitle(t *testing.T) {
	cases := map[string]string{
		"ipv4.0":          "IPV4",
		"entity.label":    "LABEL",
		"filesystem_path": "FILESYSTEM PATH",
		"id":              "ID",
	}
	for in, want := range cases {
		if got := ColumnTitle(in); got != want {
			t.Errorf("ColumnTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCellFormats(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "-"},
		{"", "-"},
		{"web1", "web1"},
		{float64(1001), "1001"},
		{float64(12.5), "12.5"},
		{true, "true"},
		{[]any{"192.0.2.10", "10.0.0.2"}, "192.0.2.10,10.0.0.2"},
	}
	for _, c := range cases {
		if got := Cell(c.in); got != c.want {
			t.Errorf("Cell(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestJSONKeepsUnicode(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, map[string]string{"label": "機器"}); err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["label"] != "機器" {
		t.Fatalf("label = %q", out["label"])
	}
}
