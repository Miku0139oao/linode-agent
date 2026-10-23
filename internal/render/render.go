package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Field resolves a dotted path against a decoded API object.
// Supports numeric segments for arrays: "ipv4.0", "entity.label".
// A literal key that contains dots wins (already-project rows store "entity.label").
func Field(obj map[string]any, path string) any {
	if v, ok := obj[path]; ok {
		return v
	}
	var cur any = obj
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[seg]
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil
			}
			cur = node[idx]
		default:
			return nil
		}
		if cur == nil {
			return nil
		}
	}
	return cur
}

// Cell renders a value for a table cell: scalars plain, arrays/objects as compact JSON.
func Cell(v any) string {
	switch val := v.(type) {
	case nil:
		return "-"
	case string:
		if val == "" {
			return "-"
		}
		return truncate(val, 60)
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			parts = append(parts, Cell(item))
		}
		return truncate(strings.Join(parts, ","), 60)
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return truncate(string(b), 60)
	}
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// ColumnTitle turns a dotted field path into a table header.
// "ipv4.0" -> "IPV4", "entity.label" -> "ENTITY", "filesystem_path" -> "FILESYSTEM PATH".
func ColumnTitle(field string) string {
	switch field {
	case "entity.label", "entity.type", "entity.url":
		return strings.ToUpper(strings.TrimPrefix(field, "entity."))
	}
	f := strings.TrimSuffix(field, ".0")
	if i := strings.LastIndex(f, "."); i >= 0 {
		f = f[i+1:]
	}
	return strings.ToUpper(strings.ReplaceAll(f, "_", " "))
}

// Table renders rows as an aligned text table.
func Table(w io.Writer, rows []map[string]any, fields []string) error {
	if len(fields) == 0 {
		return fmt.Errorf("render: no fields requested")
	}
	headers := make([]string, len(fields))
	for i, f := range fields {
		headers[i] = ColumnTitle(f)
	}

	cells := make([][]string, 0, len(rows))
	for _, row := range rows {
		line := make([]string, len(fields))
		for i, f := range fields {
			line[i] = Cell(Field(row, f))
		}
		cells = append(cells, line)
	}

	widths := make([]int, len(fields))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, line := range cells {
		for i, c := range line {
			if n := utf8.RuneCountInString(c); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b strings.Builder
	for i, h := range headers {
		b.WriteString(pad(h, widths[i]))
		if i < len(headers)-1 {
			b.WriteString("  ")
		}
	}
	if _, err := fmt.Fprintln(w, strings.TrimRight(b.String(), " ")); err != nil {
		return err
	}
	for _, line := range cells {
		var lb strings.Builder
		for i, c := range line {
			lb.WriteString(pad(c, widths[i]))
			if i < len(line)-1 {
				lb.WriteString("  ")
			}
		}
		if _, err := fmt.Fprintln(w, strings.TrimRight(lb.String(), " ")); err != nil {
			return err
		}
	}
	return nil
}

func pad(s string, width int) string {
	n := width - utf8.RuneCountInString(s)
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}

// JSON writes indented JSON.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Project keeps only the requested dotted fields in each row (nested fields are flattened).
func Project(rows []map[string]any, fields []string) []map[string]any {
	if len(fields) == 0 {
		return rows
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m := make(map[string]any, len(fields))
		for _, f := range fields {
			m[f] = Field(row, f)
		}
		out = append(out, m)
	}
	return out
}
