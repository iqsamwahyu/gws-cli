package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/sheets/v4"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

var (
	noteWhere string
	noteCol   string
	noteSet   string
)

var sheetNoteCmd = &cobra.Command{
	Use:   "note <link> --where <header>=<value> --col <header> [--set <text>]",
	Short: "Read or write the note on one cell",
	Long: `Read or write the note on one cell.

A note is the small marker that sits on a single cell. It is not a Drive
comment thread: it carries no author, no replies and no mention. Use
gws comment for a thread, and this for a note pinned to one cell.

An empty --set clears the note.`,
	Args: cobra.ExactArgs(1),
	RunE: runSheetNote,
}

func init() {
	sheetNoteCmd.Flags().StringVar(&lookupTab, "tab", "", "tab title to work on (default: the tab in the link #gid, else the first tab)")
	sheetNoteCmd.Flags().IntVar(&lookupHeaderRow, "header-row", 1, "sheet row that holds the headers")
	sheetNoteCmd.Flags().StringVar(&noteWhere, "where", "", "<header>=<value> that picks the row (required)")
	sheetNoteCmd.Flags().StringVar(&noteCol, "col", "", "column to read or write, by header name or letter (required)")
	sheetNoteCmd.Flags().StringVar(&noteSet, "set", "", "write this note instead of reading it; an empty value clears it")
	_ = sheetNoteCmd.MarkFlagRequired("where")
	_ = sheetNoteCmd.MarkFlagRequired("col")
	sheetCmd.AddCommand(sheetNoteCmd)
}

func runSheetNote(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	keyName, keyValue, ok := strings.Cut(noteWhere, "=")
	if !ok || strings.TrimSpace(keyName) == "" {
		return fmt.Errorf("--where takes <header>=<value>, for example --where \"Bug ID=B-007\"")
	}
	g, err := loadGrid(ref)
	if err != nil {
		return err
	}
	key, err := g.col(keyName)
	if err != nil {
		return err
	}
	target, err := g.col(noteCol)
	if err != nil {
		return err
	}

	hits := g.match(key, keyValue, false)
	switch {
	case len(hits) == 0:
		return fmt.Errorf("no row in tab %q has %q in %s", g.tab, keyValue, g.headerName(key))
	case len(hits) > 1:
		addrs := make([]string, 0, len(hits))
		for _, r := range hits {
			addrs = append(addrs, g.a1(r, key))
		}
		return fmt.Errorf("%q matches %d rows (%s): narrow the key, gws refuses an ambiguous cell",
			keyValue, len(hits), strings.Join(addrs, ", "))
	}
	row := hits[0]
	addr := g.a1(row, target)

	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return err
	}

	if !cmd.Flags().Changed("set") {
		ss, err := svc.Spreadsheets.Get(ref.ID).Ranges(addr).
			Fields("sheets.data.rowData.values.note").Context(c).Do()
		if err != nil {
			return gsvc.Explain(err)
		}
		note := firstNote(ss)
		if jsonOut {
			return printJSON(map[string]any{"cell": addr, "column": g.headerName(target), "row": row + 1, "note": note})
		}
		fmt.Println(note)
		return nil
	}

	sheetID, err := tabID(svc, ref.ID, g.tab, c)
	if err != nil {
		return err
	}
	// repeatCell with the mask "note" touches the note and nothing else.
	// updateCells with the same mask replaced the whole cell and dropped its
	// value, so it must not be used here.
	req := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{{
			RepeatCell: &sheets.RepeatCellRequest{
				Fields: "note",
				Cell:   &sheets.CellData{Note: noteSet, ForceSendFields: []string{"Note"}},
				Range: &sheets.GridRange{
					SheetId:          sheetID,
					StartRowIndex:    int64(row),
					EndRowIndex:      int64(row + 1),
					StartColumnIndex: int64(target),
					EndColumnIndex:   int64(target + 1),
					ForceSendFields:  []string{"StartRowIndex", "StartColumnIndex"},
				},
			},
		}},
	}
	if _, err := svc.Spreadsheets.BatchUpdate(ref.ID, req).Context(c).Do(); err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(map[string]any{"cell": addr, "column": g.headerName(target), "row": row + 1, "note": noteSet})
	}
	if noteSet == "" {
		fmt.Printf("cleared the note on %s (%s, row %d)\n", addr, g.headerName(target), row+1)
		return nil
	}
	fmt.Printf("noted %s (%s, row %d): %q\n", addr, g.headerName(target), row+1, noteSet)
	return nil
}

func firstNote(ss *sheets.Spreadsheet) string {
	for _, s := range ss.Sheets {
		for _, d := range s.Data {
			for _, r := range d.RowData {
				for _, v := range r.Values {
					if v != nil && v.Note != "" {
						return v.Note
					}
				}
			}
		}
	}
	return ""
}

func tabID(svc *sheets.Service, id, title string, c context.Context) (int64, error) {
	ss, err := svc.Spreadsheets.Get(id).Fields("sheets.properties(sheetId,title)").Context(c).Do()
	if err != nil {
		return 0, gsvc.Explain(err)
	}
	for _, s := range ss.Sheets {
		if s.Properties != nil && s.Properties.Title == title {
			return s.Properties.SheetId, nil
		}
	}
	return 0, fmt.Errorf("no tab named %q in this spreadsheet", title)
}
