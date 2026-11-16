package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/sheets/v4"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

var (
	lookupTab       string
	lookupHeaderRow int
	findCol         string
	findValue       string
	findContains    bool
	findShow        []string
	findCols        []string
	findWhere       []string
	findLimit       int
	cellWhere       []string
	cellCol         []string
	cellSet         string
	cellForce       bool
	tallyCol        string
)

var sheetColsCmd = &cobra.Command{
	Use:   "cols <link>",
	Short: "List the header row: column letter, index and name",
	Args:  cobra.ExactArgs(1),
	RunE:  runSheetCols,
}

var sheetFindCmd = &cobra.Command{
	Use:   "find <link> --col <header|letter> --value <text>",
	Short: "Find the rows whose column holds a value, and print their row numbers",
	Long: `Find the rows whose column holds a value, and print their row numbers.

--col with --value is one condition; --where adds more, and every condition
must hold. --where takes <header>=<v>, <header>!=<v> or <header>~=<v>.

  gws sheet find <link> --col Status --value Todo --show "Task name"
  gws sheet find <link> --where Status=Todo --where "Repo~=pwa" --cols "Task ID,Task name"

The table prints ROW and the --show/--cols columns, or the matched columns
when none is named. A cell is cut at 60 characters only on a terminal and
without --full. --json prints the --show/--cols columns, or every column when
none is named, and leaves the empty cells out.`,
	Args: cobra.ExactArgs(1),
	RunE: runSheetFind,
}

var sheetCellCmd = &cobra.Command{
	Use:   "cell <link> --where <header>=<value> --col <header> [--set <text>]",
	Short: "Read one cell addressed by a key column, or write it with --set",
	Long: `Read one cell addressed by a key column, or write it with --set.

The cell is named by data, not by A1. --where picks the row, --col picks the
column, and both take a header name from the header row.

  gws sheet cell <link> --where "Bug ID=B-007" --col "Fix Description"
  gws sheet cell <link> --where "Bug ID=B-007" --col "Fix Description" --set fixed

A write stops when the key matches no row or more than one row, so a wrong key
cannot overwrite the wrong cell. A write also stops when the cell is a dropdown
that does not list the value; a comma-separated value is checked part by part.`,
	Args: cobra.ExactArgs(1),
	RunE: runSheetCell,
}

var sheetTallyCmd = &cobra.Command{
	Use:   "tally <link> --col <header|letter>",
	Short: "Count the rows per distinct value of one column",
	Args:  cobra.ExactArgs(1),
	RunE:  runSheetTally,
}

func init() {
	for _, c := range []*cobra.Command{sheetColsCmd, sheetFindCmd, sheetCellCmd, sheetTallyCmd, sheetGetCmd} {
		c.Flags().StringVar(&lookupTab, "tab", "", "tab title to work on (default: the tab in the link #gid, else the first tab)")
		c.Flags().IntVar(&lookupHeaderRow, "header-row", 1, "sheet row that holds the headers")
	}
	sheetFindCmd.Flags().StringVar(&findCol, "col", "", "column to match, by header name or by letter (with --value)")
	sheetFindCmd.Flags().StringVar(&findValue, "value", "", "value to match in --col")
	sheetFindCmd.Flags().BoolVar(&findContains, "contains", false, "match --value as a substring instead of the whole cell")
	sheetFindCmd.Flags().StringArrayVar(&findShow, "show", nil, "column to print, by header name or letter; repeatable or comma-separated")
	sheetFindCmd.Flags().StringArrayVar(&findCols, "cols", nil, "same as --show")
	sheetFindCmd.Flags().StringArrayVar(&findWhere, "where", nil, "<header>=<v>, !=<v> or ~=<v> that a row must meet; repeatable")
	sheetFindCmd.Flags().IntVar(&findLimit, "limit", 0, "print at most this many rows (0 = all)")
	sheetFindCmd.Flags().BoolVar(&fullOut, "full", false, "never cut a long cell in the table")
	sheetFindCmd.MarkFlagsRequiredTogether("col", "value")

	sheetCellCmd.Flags().StringArrayVar(&cellWhere, "where", nil, "<header>=<v> that picks the row; repeatable, all must hold (required)")
	sheetCellCmd.Flags().StringArrayVar(&cellCol, "col", nil, "column to read or write, by header name or letter; a read takes several, comma-separated or repeated (required)")
	sheetCellCmd.Flags().StringVar(&cellSet, "set", "", "write this value into the cell instead of reading it")
	sheetCellCmd.Flags().BoolVar(&cellForce, "force", false, "write --set even when the cell's dropdown does not list the value")
	_ = sheetCellCmd.MarkFlagRequired("where")
	_ = sheetCellCmd.MarkFlagRequired("col")

	sheetTallyCmd.Flags().StringVar(&tallyCol, "col", "", "column to count, by header name or letter (required)")
	_ = sheetTallyCmd.MarkFlagRequired("col")

	sheetCmd.AddCommand(sheetColsCmd, sheetFindCmd, sheetCellCmd, sheetTallyCmd)
}

// grid is one tab read whole, with the header row split out.
type grid struct {
	tab     string
	headers []string
	rows    [][]string // index 0 is sheet row 1
}

func loadGrid(ref glink.Ref) (*grid, error) {
	if lookupHeaderRow < 1 {
		return nil, fmt.Errorf("--header-row must be 1 or more")
	}
	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return nil, err
	}
	title := lookupTab
	if title == "" {
		ss, err := svc.Spreadsheets.Get(ref.ID).Fields("sheets.properties").Context(c).Do()
		if err != nil {
			return nil, gsvc.Explain(err)
		}
		if title, err = tabTitle(ss, ref.GID); err != nil {
			return nil, err
		}
	}
	vr, err := svc.Spreadsheets.Values.Get(ref.ID, quoteTab(title)).
		ValueRenderOption("FORMATTED_VALUE").Context(c).Do()
	if err != nil {
		return nil, gsvc.Explain(err)
	}
	rows := make([][]string, len(vr.Values))
	for i, r := range vr.Values {
		row := make([]string, len(r))
		for j, v := range r {
			row[j] = fmt.Sprint(v)
		}
		rows[i] = row
	}
	if len(rows) < lookupHeaderRow {
		return nil, fmt.Errorf("tab %q has %d rows, so row %d holds no header", title, len(rows), lookupHeaderRow)
	}
	return &grid{tab: title, headers: rows[lookupHeaderRow-1], rows: rows}, nil
}

// col resolves a header name, or a column letter, to a zero-based index.
func (g *grid) col(want string) (int, error) {
	w := strings.TrimSpace(want)
	for i, h := range g.headers {
		if strings.EqualFold(strings.TrimSpace(h), w) {
			return i, nil
		}
	}
	if i, ok := letterIndex(w); ok {
		return i, nil
	}
	return 0, fmt.Errorf("tab %q has no column %q: gws sheet cols lists the headers", g.tab, want)
}

// cell reads a cell without panicking on a short row, which a hand-kept sheet
// is full of.
func (g *grid) cell(row, col int) string {
	if row < 0 || row >= len(g.rows) || col < 0 || col >= len(g.rows[row]) {
		return ""
	}
	return g.rows[row][col]
}

func (g *grid) a1(rowIdx, col int) string {
	return fmt.Sprintf("%s!%s%d", quoteTab(g.tab), colLetter(col), rowIdx+1)
}

// match returns the row indexes below the header row whose column holds value.
func (g *grid) match(col int, value string, contains bool) []int {
	var out []int
	want := strings.TrimSpace(value)
	for i := lookupHeaderRow; i < len(g.rows); i++ {
		got := strings.TrimSpace(g.cell(i, col))
		if contains {
			if strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
				out = append(out, i)
			}
			continue
		}
		if strings.EqualFold(got, want) {
			out = append(out, i)
		}
	}
	return out
}

func (g *grid) headerName(col int) string {
	if col < len(g.headers) && strings.TrimSpace(g.headers[col]) != "" {
		return g.headers[col]
	}
	return "column " + colLetter(col)
}

func colLetter(i int) string {
	s := ""
	for n := i + 1; n > 0; {
		n--
		s = string(rune('A'+n%26)) + s
		n /= 26
	}
	return s
}

func letterIndex(s string) (int, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" || len(s) > 3 {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return 0, false
		}
		n = n*26 + int(r-'A') + 1
	}
	return n - 1, true
}

func runSheetCols(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	g, err := loadGrid(ref)
	if err != nil {
		return err
	}
	if jsonOut {
		out := make([]map[string]any, 0, len(g.headers))
		for i, h := range g.headers {
			out = append(out, map[string]any{"letter": colLetter(i), "index": i + 1, "header": h})
		}
		return printJSON(map[string]any{"tab": g.tab, "columns": out})
	}
	w := tw()
	fmt.Fprintln(w, "LETTER\tINDEX\tHEADER")
	for i, h := range g.headers {
		fmt.Fprintf(w, "%s\t%d\t%s\n", colLetter(i), i+1, h)
	}
	return w.Flush()
}

func runSheetFind(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	if findCol == "" && len(findWhere) == 0 {
		return fmt.Errorf("find needs a condition: --col with --value, or --where")
	}
	if findLimit < 0 {
		return fmt.Errorf("--limit must be 0 or more")
	}
	g, err := loadGrid(ref)
	if err != nil {
		return err
	}
	conds, err := parseConds(g, findWhere)
	if err != nil {
		return err
	}
	if findCol != "" {
		key, err := g.col(findCol)
		if err != nil {
			return err
		}
		op := "="
		if findContains {
			op = "~="
		}
		conds = append([]cond{{col: key, op: op, val: strings.TrimSpace(findValue)}}, conds...)
	}
	show, err := g.cols(append(append([]string{}, findShow...), findCols...))
	if err != nil {
		return err
	}
	key := conds[0].col

	hits := g.filter(conds, findLimit)
	if jsonOut {
		cols := show
		if len(cols) == 0 {
			cols = g.allCols()
		}
		out := make([]map[string]any, 0, len(hits))
		for _, r := range hits {
			out = append(out, map[string]any{"row": r + 1, "cell": g.a1(r, key), "values": g.record(r, cols)[1:]})
		}
		return printJSON(map[string]any{"tab": g.tab, "matches": out})
	}
	if len(hits) == 0 {
		return fmt.Errorf("no row in tab %q meets %s", g.tab, describeConds(g, conds))
	}
	if len(show) == 0 {
		seen := map[int]bool{}
		for _, c := range conds {
			if !seen[c.col] {
				seen[c.col] = true
				show = append(show, c.col)
			}
		}
	}
	w := tw()
	head := "ROW"
	for _, i := range show {
		head += "\t" + g.headerName(i)
	}
	fmt.Fprintln(w, head)
	for _, r := range hits {
		line := fmt.Sprint(r + 1)
		for _, i := range show {
			line += "\t" + oneLine(g.cell(r, i))
		}
		fmt.Fprintln(w, line)
	}
	return w.Flush()
}

func describeConds(g *grid, conds []cond) string {
	parts := make([]string, 0, len(conds))
	for _, c := range conds {
		parts = append(parts, fmt.Sprintf("%s%s%q", g.headerName(c.col), c.op, c.val))
	}
	return strings.Join(parts, " and ")
}

func runSheetCell(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	g, err := loadGrid(ref)
	if err != nil {
		return err
	}
	conds, err := parseConds(g, cellWhere)
	if err != nil {
		return err
	}
	targets, err := g.cols(cellCol)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("--col names no column")
	}
	if len(targets) > 1 && cmd.Flags().Changed("set") {
		return fmt.Errorf("--set writes one cell, so --col takes one column with it")
	}
	key := conds[0].col

	hits := g.filter(conds, 0)
	switch {
	case len(hits) == 0:
		return fmt.Errorf("no row in tab %q meets %s", g.tab, describeConds(g, conds))
	case len(hits) > 1:
		addrs := make([]string, 0, len(hits))
		for _, r := range hits {
			addrs = append(addrs, g.a1(r, key))
		}
		return fmt.Errorf("%s matches %d rows (%s): narrow the key, gws refuses an ambiguous cell",
			describeConds(g, conds), len(hits), strings.Join(addrs, ", "))
	}
	row := hits[0]

	if len(targets) > 1 {
		if jsonOut {
			return printJSON(map[string]any{"tab": g.tab, "row": row + 1, "values": g.record(row, targets)[1:]})
		}
		for _, c := range targets {
			fmt.Printf("%s: %s\n", g.headerName(c), g.cell(row, c))
		}
		return nil
	}
	target := targets[0]
	addr := g.a1(row, target)
	old := g.cell(row, target)

	if !cmd.Flags().Changed("set") {
		if jsonOut {
			return printJSON(map[string]any{"tab": g.tab, "row": row + 1, "cell": addr, "column": g.headerName(target), "value": old})
		}
		fmt.Println(old)
		return nil
	}

	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return err
	}
	if !cellForce {
		dd, ok, err := cellDropdown(svc, ref.ID, addr, c)
		if err != nil {
			return err
		}
		if !ok {
			if dd, ok, err = tableDropdown(svc, ref.ID, g.tab, row, target, c); err != nil {
				return err
			}
		}
		if bad := dd.disallowed(cellSet); ok && len(bad) > 0 {
			return fmt.Errorf("%s is a dropdown and does not allow %q; allowed: %s (--force writes it anyway)",
				g.headerName(target), strings.Join(bad, ", "), strings.Join(dd.Values, " | "))
		}
	}
	resp, err := svc.Spreadsheets.Values.Update(ref.ID, addr,
		&sheets.ValueRange{Values: [][]any{{cellSet}}}).
		ValueInputOption("USER_ENTERED").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(map[string]any{"cell": addr, "column": g.headerName(target), "was": old, "now": cellSet, "updated": resp.UpdatedCells})
	}
	fmt.Printf("updated %s (%s, row %d): %q -> %q\n", addr, g.headerName(target), row+1, old, cellSet)
	return nil
}

func runSheetTally(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	g, err := loadGrid(ref)
	if err != nil {
		return err
	}
	col, err := g.col(tallyCol)
	if err != nil {
		return err
	}

	counts := map[string]int{}
	total, short := 0, 0
	for i := lookupHeaderRow; i < len(g.rows); i++ {
		if len(g.rows[i]) == 0 {
			continue
		}
		total++
		// a hand-kept sheet holds rows that stop before this column; they are
		// blank, not absent, and a silent skip would understate every count
		if col >= len(g.rows[i]) {
			short++
		}
		v := strings.TrimSpace(g.cell(i, col))
		if v == "" {
			v = "(blank)"
		}
		counts[v]++
	}

	type kv struct {
		Value string `json:"value"`
		Count int    `json:"count"`
	}
	out := make([]kv, 0, len(counts))
	for k, v := range counts {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})

	if jsonOut {
		return printJSON(map[string]any{
			"tab": g.tab, "column": g.headerName(col), "rows": total, "short_rows": short, "counts": out,
		})
	}
	w := tw()
	fmt.Fprintf(w, "%s\tROWS\n", strings.ToUpper(g.headerName(col)))
	for _, e := range out {
		fmt.Fprintf(w, "%s\t%d\n", e.Value, e.Count)
	}
	fmt.Fprintf(w, "TOTAL\t%d\n", total)
	if err := w.Flush(); err != nil {
		return err
	}
	if short > 0 {
		fmt.Printf("%d of those rows stop before column %s and count as (blank).\n", short, colLetter(col))
	}
	return nil
}

// oneLine keeps a table row on one line when a cell holds newlines, which a
// steps-to-reproduce column usually does. The cut is for a terminal only: a
// pipe or an agent gets the whole value.
func oneLine(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " ")
	if r := []rune(s); len(r) > 60 && !fullOut && stdoutTTY() {
		s = string(r[:57]) + "..."
	}
	return s
}
