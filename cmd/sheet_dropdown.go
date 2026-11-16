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
	dropdownCol     string
	dropdownSet     string
	dropdownLenient bool
)

var sheetDropdownCmd = &cobra.Command{
	Use:   "dropdown <link> --col <header|letter> [--set <a,b,c>]",
	Short: "List the values a dropdown column allows, or set them with --set",
	Long: `List the values a dropdown column allows, or set them with --set.

A dropdown is a data validation rule on the cells of a column: a typed list of
values, or a range the values come from. A Sheets Table keeps the rule on the
table column instead. The rule is read from the cells below the header row; a
column whose cells carry different rules prints each one.

--set puts a list dropdown on every row below the header and replaces the rule
that was there. The values are comma-separated. The dropdown rejects any other
value unless --lenient is given, which only marks it.

Caution: chip style, per-value colors and "allow multiple selections" exist
only in the Sheets UI. The API cannot read or send them, so --set on a column
that has them drops them. Set them again in the UI after --set.

gws sheet cell --set checks the same rule before it writes.`,
	Args: cobra.ExactArgs(1),
	RunE: runSheetDropdown,
}

func init() {
	sheetDropdownCmd.Flags().StringVar(&lookupTab, "tab", "", "tab title to work on (default: the tab in the link #gid, else the first tab)")
	sheetDropdownCmd.Flags().IntVar(&lookupHeaderRow, "header-row", 1, "sheet row that holds the headers")
	sheetDropdownCmd.Flags().StringVar(&dropdownCol, "col", "", "column to read or set, by header name or letter (required)")
	sheetDropdownCmd.Flags().StringVar(&dropdownSet, "set", "", "comma-separated values: put this list dropdown on the column")
	sheetDropdownCmd.Flags().BoolVar(&dropdownLenient, "lenient", false, "with --set, mark a value outside the list instead of rejecting it")
	_ = sheetDropdownCmd.MarkFlagRequired("col")
	sheetCmd.AddCommand(sheetDropdownCmd)
}

// dropdown is one validation rule reduced to what a caller needs.
type dropdown struct {
	Kind    string   `json:"kind"`
	Source  string   `json:"source,omitempty"`
	Strict  bool     `json:"strict"`
	Values  []string `json:"values"`
	FirstAt string   `json:"first_cell"`
}

func runSheetDropdown(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindSheet)
	if err != nil {
		return err
	}
	g, err := loadGrid(ref)
	if err != nil {
		return err
	}
	col, err := g.col(dropdownCol)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Sheets(c)
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("set") {
		return setDropdown(svc, ref.ID, g, col, c)
	}
	// the grid stops at the last row with a value, but a dropdown is usually set
	// on empty rows too, so the range runs to the end of the column
	rng := fmt.Sprintf("%s!%s%d:%s", quoteTab(g.tab), colLetter(col), lookupHeaderRow+1, colLetter(col))
	rules, err := columnRules(svc, ref.ID, g.tab, rng, lookupHeaderRow+1, col, c)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		dd, ok, err := tableDropdown(svc, ref.ID, g.tab, -1, col, c)
		if err != nil {
			return err
		}
		if ok {
			dd.FirstAt = fmt.Sprintf("%s!%s", quoteTab(g.tab), colLetter(col))
			rules = append(rules, dd)
		}
	}
	if jsonOut {
		return printJSON(map[string]any{"tab": g.tab, "column": g.headerName(col), "rules": rules})
	}
	if len(rules) == 0 {
		return fmt.Errorf("column %s in tab %q has no dropdown rule", g.headerName(col), g.tab)
	}
	for i, r := range rules {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s (%s) from %s, strict=%v\n", g.headerName(col), r.Kind, r.FirstAt, r.Strict)
		if r.Source != "" {
			fmt.Printf("source: %s\n", r.Source)
		}
		for _, v := range r.Values {
			fmt.Println(v)
		}
	}
	return nil
}

func setDropdown(svc *sheets.Service, id string, g *grid, col int, c context.Context) error {
	var vals []*sheets.ConditionValue
	var list []string
	for _, v := range strings.Split(dropdownSet, ",") {
		if v = strings.TrimSpace(v); v != "" {
			vals = append(vals, &sheets.ConditionValue{UserEnteredValue: v})
			list = append(list, v)
		}
	}
	if len(vals) == 0 {
		return fmt.Errorf("--set needs at least one value, for example --set \"Todo,In Progress,Done\"")
	}
	sheetID, err := tabID(svc, id, g.tab, c)
	if err != nil {
		return err
	}
	req := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{{
			SetDataValidation: &sheets.SetDataValidationRequest{
				// no EndRowIndex: the rule runs to the last row, so a new row
				// appended later gets the dropdown too
				Range: &sheets.GridRange{
					SheetId:          sheetID,
					StartRowIndex:    int64(lookupHeaderRow),
					StartColumnIndex: int64(col),
					EndColumnIndex:   int64(col + 1),
					ForceSendFields:  []string{"StartColumnIndex"},
				},
				Rule: &sheets.DataValidationRule{
					Condition:    &sheets.BooleanCondition{Type: "ONE_OF_LIST", Values: vals},
					Strict:       !dropdownLenient,
					ShowCustomUi: true,
				},
			},
		}},
	}
	if _, err := svc.Spreadsheets.BatchUpdate(id, req).Context(c).Do(); err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(map[string]any{"tab": g.tab, "column": g.headerName(col), "values": list, "strict": !dropdownLenient})
	}
	fmt.Printf("set a dropdown on %s!%s%d:%s (%s): %s\n", quoteTab(g.tab), colLetter(col), lookupHeaderRow+1, colLetter(col),
		g.headerName(col), strings.Join(list, " | "))
	return nil
}

// columnRules returns each distinct dropdown rule found in rng, in sheet order.
func columnRules(svc *sheets.Service, id, tab, rng string, firstRow, col int, c context.Context) ([]dropdown, error) {
	ss, err := svc.Spreadsheets.Get(id).Ranges(rng).
		Fields("sheets.data.rowData.values.dataValidation").Context(c).Do()
	if err != nil {
		return nil, gsvc.Explain(err)
	}
	var out []dropdown
	seen := map[string]bool{}
	for _, s := range ss.Sheets {
		for _, d := range s.Data {
			for i, r := range d.RowData {
				for _, v := range r.Values {
					if v == nil || v.DataValidation == nil {
						continue
					}
					dd, ok, err := toDropdown(svc, id, v.DataValidation, c)
					if err != nil {
						return nil, err
					}
					if !ok {
						continue
					}
					k := dd.Kind + "|" + dd.Source + "|" + strings.Join(dd.Values, "\x00")
					if seen[k] {
						continue
					}
					seen[k] = true
					dd.FirstAt = fmt.Sprintf("%s!%s%d", quoteTab(tab), colLetter(col), firstRow+i)
					out = append(out, dd)
				}
			}
		}
	}
	return out, nil
}

// cellDropdown returns the dropdown rule on one cell, if it has one.
func cellDropdown(svc *sheets.Service, id, addr string, c context.Context) (dropdown, bool, error) {
	ss, err := svc.Spreadsheets.Get(id).Ranges(addr).
		Fields("sheets.data.rowData.values.dataValidation").Context(c).Do()
	if err != nil {
		return dropdown{}, false, gsvc.Explain(err)
	}
	for _, s := range ss.Sheets {
		for _, d := range s.Data {
			for _, r := range d.RowData {
				for _, v := range r.Values {
					if v != nil && v.DataValidation != nil {
						return toDropdown(svc, id, v.DataValidation, c)
					}
				}
			}
		}
	}
	return dropdown{}, false, nil
}

// tableDropdown reads the dropdown of a Sheets Table column. A Table keeps the
// rule on the column, not on each cell, so the cell read finds nothing there.
// row is zero-based; -1 accepts any row of the table.
func tableDropdown(svc *sheets.Service, id, tab string, row, col int, c context.Context) (dropdown, bool, error) {
	ss, err := svc.Spreadsheets.Get(id).
		Fields("sheets(properties(title),tables(name,range,columnProperties))").Context(c).Do()
	if err != nil {
		return dropdown{}, false, gsvc.Explain(err)
	}
	for _, s := range ss.Sheets {
		if s.Properties == nil || s.Properties.Title != tab {
			continue
		}
		for _, t := range s.Tables {
			if t.Range == nil {
				continue
			}
			r := t.Range
			if int64(col) < r.StartColumnIndex || int64(col) >= r.EndColumnIndex {
				continue
			}
			if row >= 0 && (int64(row) <= r.StartRowIndex || int64(row) >= r.EndRowIndex) {
				continue
			}
			for _, p := range t.ColumnProperties {
				if r.StartColumnIndex+p.ColumnIndex != int64(col) || p.DataValidationRule == nil || p.DataValidationRule.Condition == nil {
					continue
				}
				dd := dropdown{Kind: "TABLE_" + p.ColumnType, Source: "table " + t.Name}
				for _, v := range p.DataValidationRule.Condition.Values {
					dd.Values = append(dd.Values, v.UserEnteredValue)
				}
				return dd, true, nil
			}
		}
	}
	return dropdown{}, false, nil
}

// toDropdown keeps only list rules; a number or date rule is not a dropdown.
func toDropdown(svc *sheets.Service, id string, rule *sheets.DataValidationRule, c context.Context) (dropdown, bool, error) {
	if rule.Condition == nil {
		return dropdown{}, false, nil
	}
	dd := dropdown{Kind: rule.Condition.Type, Strict: rule.Strict}
	switch rule.Condition.Type {
	case "ONE_OF_LIST":
		for _, v := range rule.Condition.Values {
			dd.Values = append(dd.Values, v.UserEnteredValue)
		}
	case "ONE_OF_RANGE":
		if len(rule.Condition.Values) == 0 {
			return dropdown{}, false, nil
		}
		dd.Source = strings.TrimPrefix(rule.Condition.Values[0].UserEnteredValue, "=")
		vr, err := svc.Spreadsheets.Values.Get(id, dd.Source).
			ValueRenderOption("FORMATTED_VALUE").Context(c).Do()
		if err != nil {
			return dropdown{}, false, gsvc.Explain(err)
		}
		for _, row := range vr.Values {
			for _, v := range row {
				if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
					dd.Values = append(dd.Values, s)
				}
			}
		}
	default:
		return dropdown{}, false, nil
	}
	return dd, true, nil
}

// disallowed returns the parts of value the dropdown does not list. A
// multi-select chip cell holds its choices comma-separated, so each part is
// checked on its own.
func (dd dropdown) disallowed(value string) []string {
	ok := map[string]bool{}
	for _, v := range dd.Values {
		ok[strings.TrimSpace(v)] = true
	}
	var bad []string
	for _, p := range strings.Split(value, ",") {
		p = strings.TrimSpace(p)
		if p != "" && !ok[p] {
			bad = append(bad, p)
		}
	}
	return bad
}
