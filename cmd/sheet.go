package cmd

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/sheets/v4"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

var sheetCmd = &cobra.Command{Use: "sheet", Short: "Read and edit one Google Sheet"}

var (
	sheetSetCSV    string
	sheetAppendCSV string
	getCSV         bool
	getCols        []string
	getWhere       []string
	getLimit       int
	getRecords     bool
)

var sheetGetCmd = &cobra.Command{
	Use:   "get <link> [range]",
	Short: "Read a range as TSV, or a whole tab with no range",
	Long: `Read a range as TSV, or a whole tab with no range.

With a range, the range goes to Google untouched (e.g. Sheet1!A1:D20).
With no range and a #gid in the link, gws reads that tab whole.
With no range and no gid, gws reads the first tab and names it on stderr.

TSV costs the fewest tokens for an agent. A tab, a new line or a backslash
inside a cell is written as \t, \n or \\, so one row stays one line.
--csv prints CSV instead. --json prints the raw ValueRange.

--cols, --where, --limit and --records read the tab by its header row and
return only what they pick. They take no range; use --tab for another tab.

  gws sheet get <link> --cols "Task ID,Status" --where Status=Todo
  gws sheet get <link> --where "Repo~=pwa" --where "Status!=Done" --limit 5
  gws sheet get <link> --cols "Task ID,Notes" --records

--where takes <header>=<v> (exact), <header>!=<v> (not equal) or <header>~=<v>
(substring); all match case-insensitively and every --where must hold.
--records, or --json with a pick flag, prints one JSON object per row in
column order, with its sheet row in "_row" and the empty cells left out.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSheetGet,
}

var sheetSetCmd = &cobra.Command{
	Use:   "set <link> <range> --csv <text|@file|->",
	Short: "Overwrite a range with CSV values (typed as USER_ENTERED)",
	Args:  cobra.ExactArgs(2),
	RunE:  runSheetSet,
}

var sheetAppendCmd = &cobra.Command{
	Use:   "append <link> <range> --csv <text|@file|->",
	Short: "Append CSV rows after a range (typed as USER_ENTERED)",
	Args:  cobra.ExactArgs(2),
	RunE:  runSheetAppend,
}

var sheetClearCmd = &cobra.Command{
	Use:   "clear <link> <range>",
	Short: "Clear the values in a range",
	Args:  cobra.ExactArgs(2),
	RunE:  runSheetClear,
}

var sheetTabsCmd = &cobra.Command{
	Use:   "tabs <link>",
	Short: "List the tabs of the spreadsheet",
	Args:  cobra.ExactArgs(1),
	RunE:  runSheetTabs,
}

var sheetAddTabCmd = &cobra.Command{
	Use:   "addtab <link> <title>",
	Short: "Add a new tab to the spreadsheet",
	Args:  cobra.ExactArgs(2),
	RunE:  runSheetAddTab,
}

func init() {
	sheetSetCmd.Flags().StringVar(&sheetSetCSV, "csv", "", "values to write: a literal CSV string, @file, or - for stdin (required)")
	sheetAppendCmd.Flags().StringVar(&sheetAppendCSV, "csv", "", "values to append: a literal CSV string, @file, or - for stdin (required)")
	sheetGetCmd.Flags().StringArrayVar(&getCols, "cols", nil, "columns to print, by header or letter, comma-separated or repeated")
	sheetGetCmd.Flags().StringArrayVar(&getWhere, "where", nil, "<header>=<v>, !=<v> or ~=<v> that a row must meet; repeatable, all must hold")
	sheetGetCmd.Flags().IntVar(&getLimit, "limit", 0, "print at most this many rows (0 = all)")
	sheetGetCmd.Flags().BoolVar(&getRecords, "records", false, "print JSON objects keyed by header, one per row")
	sheetGetCmd.Flags().BoolVar(&getCSV, "csv", false, "print CSV instead of TSV")
	_ = sheetSetCmd.MarkFlagRequired("csv")
	_ = sheetAppendCmd.MarkFlagRequired("csv")

	sheetCmd.AddCommand(sheetGetCmd, sheetSetCmd, sheetAppendCmd, sheetClearCmd, sheetTabsCmd, sheetAddTabCmd)
	rootCmd.AddCommand(sheetCmd)
}

func runSheetGet(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	if len(getCols) > 0 || len(getWhere) > 0 || getLimit != 0 || getRecords {
		if len(args) == 2 {
			return fmt.Errorf("--cols, --where, --limit and --records read a whole tab by its headers: drop the range, and use --tab to pick the tab")
		}
		return runSheetGetPicked(ref)
	}
	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return err
	}

	var rangeStr string
	switch {
	case len(args) == 2:
		rangeStr = args[1]
	case lookupTab != "":
		rangeStr = quoteTab(lookupTab)
	default:
		ss, err := svc.Spreadsheets.Get(ref.ID).Fields("sheets.properties").Context(c).Do()
		if err != nil {
			return gsvc.Explain(err)
		}
		title, err := tabTitle(ss, ref.GID)
		if err != nil {
			return err
		}
		if ref.GID < 0 {
			// stdout carries the rows, so the note about which tab was picked
			// goes to stderr and a pipe stays clean
			fmt.Fprintf(os.Stderr, "no range and no gid in the link: reading the first tab %q (gws sheet tabs lists them all)\n", title)
		}
		rangeStr = quoteTab(title)
	}

	vr, err := svc.Spreadsheets.Values.Get(ref.ID, rangeStr).ValueRenderOption("FORMATTED_VALUE").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(vr)
	}
	return writeRows(vr.Values)
}

func runSheetGetPicked(ref glink.Ref) error {
	if getLimit < 0 {
		return fmt.Errorf("--limit must be 0 or more")
	}
	g, err := loadGrid(ref)
	if err != nil {
		return err
	}
	cols, err := g.cols(getCols)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		cols = g.allCols()
	}
	conds, err := parseConds(g, getWhere)
	if err != nil {
		return err
	}
	rows := g.filter(conds, getLimit)

	if jsonOut || getRecords {
		out := make([]obj, 0, len(rows))
		for _, r := range rows {
			out = append(out, g.record(r, cols))
		}
		return printJSON(out)
	}
	table := make([][]any, 0, len(rows)+1)
	head := make([]any, len(cols))
	for i, c := range cols {
		head[i] = g.headerName(c)
	}
	table = append(table, head)
	for _, r := range rows {
		line := make([]any, len(cols))
		for i, c := range cols {
			line[i] = g.cell(r, c)
		}
		table = append(table, line)
	}
	return writeRows(table)
}

func runSheetSet(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	vals, err := readCSVValues(sheetSetCSV)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return err
	}
	resp, err := svc.Spreadsheets.Values.Update(ref.ID, args[1], &sheets.ValueRange{Values: vals}).
		ValueInputOption("USER_ENTERED").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(resp)
	}
	fmt.Printf("updated %s: %d cells, %d rows, %d columns\n", resp.UpdatedRange, resp.UpdatedCells, resp.UpdatedRows, resp.UpdatedColumns)
	return nil
}

func runSheetAppend(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	vals, err := readCSVValues(sheetAppendCSV)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return err
	}
	resp, err := svc.Spreadsheets.Values.Append(ref.ID, args[1], &sheets.ValueRange{Values: vals}).
		ValueInputOption("USER_ENTERED").InsertDataOption("INSERT_ROWS").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(resp)
	}
	if u := resp.Updates; u != nil {
		fmt.Printf("appended into %s: %d cells, %d rows, %d columns\n", u.UpdatedRange, u.UpdatedCells, u.UpdatedRows, u.UpdatedColumns)
		return nil
	}
	fmt.Printf("appended into %s\n", resp.TableRange)
	return nil
}

func runSheetClear(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return err
	}
	resp, err := svc.Spreadsheets.Values.Clear(ref.ID, args[1], &sheets.ClearValuesRequest{}).Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(resp)
	}
	fmt.Printf("cleared %s\n", resp.ClearedRange)
	return nil
}

func runSheetTabs(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return err
	}
	ss, err := svc.Spreadsheets.Get(ref.ID).Fields("sheets.properties").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(ss.Sheets)
	}
	return printTabs(ss)
}

func runSheetAddTab(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return err
	}
	resp, err := svc.Spreadsheets.BatchUpdate(ref.ID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{{
			AddSheet: &sheets.AddSheetRequest{
				Properties: &sheets.SheetProperties{Title: args[1]},
			},
		}},
	}).Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(resp)
	}
	if len(resp.Replies) == 0 || resp.Replies[0].AddSheet == nil || resp.Replies[0].AddSheet.Properties == nil {
		fmt.Printf("added tab %q\n", args[1])
		return nil
	}
	p := resp.Replies[0].AddSheet.Properties
	fmt.Printf("added tab %q (sheetId %d)\n", p.Title, p.SheetId)
	return nil
}

func printTabs(ss *sheets.Spreadsheet) error {
	w := tw()
	fmt.Fprintln(w, "TITLE\tSHEETID\tROWS\tCOLUMNS")
	for _, s := range ss.Sheets {
		p := s.Properties
		if p == nil {
			continue
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\n", p.Title, p.SheetId, rowCount(p), colCount(p))
	}
	return w.Flush()
}

// tabTitle names the tab the link points at, or the first tab when the link
// carries no gid.
func tabTitle(ss *sheets.Spreadsheet, gid int64) (string, error) {
	if len(ss.Sheets) == 0 || ss.Sheets[0].Properties == nil {
		return "", fmt.Errorf("this spreadsheet has no tabs")
	}
	if gid < 0 {
		return ss.Sheets[0].Properties.Title, nil
	}
	for _, s := range ss.Sheets {
		if s.Properties != nil && s.Properties.SheetId == gid {
			return s.Properties.Title, nil
		}
	}
	return "", fmt.Errorf("no tab with gid %d in this spreadsheet: gws sheet tabs lists them", gid)
}

// quoteTab wraps a tab title in A1-notation single quotes, since a title with
// a space or a symbol is not a valid range on its own.
func quoteTab(title string) string {
	return "'" + strings.ReplaceAll(title, "'", "''") + "'"
}

func rowCount(p *sheets.SheetProperties) int64 {
	if p.GridProperties == nil {
		return 0
	}
	return p.GridProperties.RowCount
}

func colCount(p *sheets.SheetProperties) int64 {
	if p.GridProperties == nil {
		return 0
	}
	return p.GridProperties.ColumnCount
}

func readCSVValues(arg string) ([][]any, error) {
	data, err := readInput(arg)
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(bytes.NewReader(data))
	// a spreadsheet paste is often ragged, and the Sheets API takes rows of
	// unequal length
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	out := make([][]any, len(rows))
	for i, row := range rows {
		r := make([]any, len(row))
		for j, v := range row {
			r[j] = v
		}
		out[i] = r
	}
	return out, nil
}

func writeRows(rows [][]any) error {
	if getCSV {
		return writeCSV(rows)
	}
	return writeTSV(rows)
}

var tsvEscaper = strings.NewReplacer("\\", `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)

func writeTSV(rows [][]any) error {
	w := bufio.NewWriter(os.Stdout)
	for _, row := range rows {
		for i, v := range row {
			if i > 0 {
				w.WriteByte('\t')
			}
			w.WriteString(tsvEscaper.Replace(fmt.Sprint(v)))
		}
		w.WriteByte('\n')
	}
	return w.Flush()
}

func writeCSV(rows [][]any) error {
	w := csv.NewWriter(os.Stdout)
	for _, row := range rows {
		rec := make([]string, len(row))
		for i, v := range row {
			rec[i] = fmt.Sprint(v)
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
