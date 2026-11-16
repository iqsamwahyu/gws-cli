package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// cond is one --where condition: <header>=<v>, <header>!=<v> or <header>~=<v>.
type cond struct {
	col int
	op  string
	val string
}

func parseConds(g *grid, raw []string) ([]cond, error) {
	out := make([]cond, 0, len(raw))
	for _, w := range raw {
		i := strings.Index(w, "=")
		if i <= 0 {
			return nil, fmt.Errorf("--where takes <header>=<value>, <header>!=<value> or <header>~=<value>, got %q", w)
		}
		name, op := w[:i], "="
		if strings.HasSuffix(name, "!") || strings.HasSuffix(name, "~") {
			op = name[len(name)-1:] + "="
			name = name[:len(name)-1]
		}
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("--where %q names no column", w)
		}
		col, err := g.col(name)
		if err != nil {
			return nil, err
		}
		out = append(out, cond{col: col, op: op, val: strings.TrimSpace(w[i+1:])})
	}
	return out, nil
}

func (c cond) ok(got string) bool {
	got = strings.TrimSpace(got)
	switch c.op {
	case "~=":
		return strings.Contains(strings.ToLower(got), strings.ToLower(c.val))
	case "!=":
		return !strings.EqualFold(got, c.val)
	default:
		return strings.EqualFold(got, c.val)
	}
}

// filter returns the non-blank row indexes below the header row that meet
// every condition, stopping at limit when limit is above 0.
func (g *grid) filter(conds []cond, limit int) []int {
	var out []int
	for i := lookupHeaderRow; i < len(g.rows); i++ {
		if g.blank(i) {
			continue
		}
		hit := true
		for _, c := range conds {
			if !c.ok(g.cell(i, c.col)) {
				hit = false
				break
			}
		}
		if !hit {
			continue
		}
		out = append(out, i)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

func (g *grid) blank(row int) bool {
	for _, v := range g.rows[row] {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// cols resolves --cols values in the order given; each value may itself hold
// a comma-separated list.
func (g *grid) cols(raw []string) ([]int, error) {
	var out []int
	for _, r := range raw {
		for _, name := range strings.Split(r, ",") {
			if strings.TrimSpace(name) == "" {
				continue
			}
			i, err := g.col(name)
			if err != nil {
				return nil, err
			}
			out = append(out, i)
		}
	}
	return out, nil
}

func (g *grid) allCols() []int {
	out := make([]int, len(g.headers))
	for i := range out {
		out[i] = i
	}
	return out
}

// record builds one row as a JSON object in column order. Empty cells are
// left out, since a hand-kept sheet is mostly empty cells.
func (g *grid) record(row int, cols []int) obj {
	o := obj{{"_row", row + 1}}
	for _, c := range cols {
		if v := g.cell(row, c); v != "" {
			o = append(o, kv{g.headerName(c), v})
		}
	}
	return o
}

type kv struct {
	K string
	V any
}

// obj is a JSON object that keeps its key order, so a record reads in the
// sheet's column order and not sorted by header name.
type obj []kv

func (o obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := encodeNoEscape(&b, e.K); err != nil {
			return nil, err
		}
		b.WriteByte(':')
		if err := encodeNoEscape(&b, e.V); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func encodeNoEscape(b *bytes.Buffer, v any) error {
	var t bytes.Buffer
	enc := json.NewEncoder(&t)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	b.Write(bytes.TrimRight(t.Bytes(), "\n"))
	return nil
}

func stdoutTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
