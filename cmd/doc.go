package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/docs/v1"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

var docCmd = &cobra.Command{Use: "doc", Short: "Read and edit one Google Doc"}

var docGetCmd = &cobra.Command{
	Use:   "get <link>",
	Short: "Print a Google Doc's body text, or its raw JSON with --json",
	Args:  cobra.ExactArgs(1),
	RunE:  runDocGet,
}

var docAppendCmd = &cobra.Command{
	Use:   "append <link> <text|@file|->",
	Short: "Append text to the end of a Google Doc",
	Args:  cobra.ExactArgs(2),
	RunE:  runDocAppend,
}

var (
	docReplaceFind      string
	docReplaceWith      string
	docReplaceMatchCase bool
)

var docReplaceCmd = &cobra.Command{
	Use:   "replace <link> --find <s> --with <s>",
	Short: "Replace every occurrence of a substring in a Google Doc",
	Args:  cobra.ExactArgs(1),
	RunE:  runDocReplace,
}

var docInsertAt int64

var docInsertCmd = &cobra.Command{
	Use:   "insert <link> <text|@file|-> --at <index>",
	Short: "Insert text at a UTF-16 index in a Google Doc",
	Args:  cobra.ExactArgs(2),
	RunE:  runDocInsert,
}

func init() {
	docReplaceCmd.Flags().StringVar(&docReplaceFind, "find", "", "substring to find (required)")
	docReplaceCmd.Flags().StringVar(&docReplaceWith, "with", "", "replacement text (required)")
	docReplaceCmd.Flags().BoolVar(&docReplaceMatchCase, "match-case", false, "match case exactly")
	_ = docReplaceCmd.MarkFlagRequired("find")
	_ = docReplaceCmd.MarkFlagRequired("with")

	docInsertCmd.Flags().Int64Var(&docInsertAt, "at", 1, "UTF-16 index to insert at; the first position in the body is 1 (required)")
	_ = docInsertCmd.MarkFlagRequired("at")

	docCmd.AddCommand(docGetCmd, docAppendCmd, docReplaceCmd, docInsertCmd)
	rootCmd.AddCommand(docCmd)
}

func runDocGet(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindDoc)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Docs(c)
	if err != nil {
		return err
	}
	doc, err := svc.Documents.Get(ref.ID).IncludeTabsContent(true).Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(doc)
	}
	fmt.Print(docBodyText(doc))
	return nil
}

func runDocAppend(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindDoc)
	if err != nil {
		return err
	}
	text, err := readInput(args[1])
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Docs(c)
	if err != nil {
		return err
	}
	// no field mask: Google refuses "body" in the mask once tabs content is
	// requested, and the emptiness check below needs both
	doc, err := svc.Documents.Get(ref.ID).IncludeTabsContent(true).Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	body := string(text)
	if strings.TrimSpace(docBodyText(doc)) != "" {
		body = "\n" + body
	}
	req := &docs.BatchUpdateDocumentRequest{
		Requests: []*docs.Request{
			{
				InsertText: &docs.InsertTextRequest{
					Text:                 body,
					EndOfSegmentLocation: &docs.EndOfSegmentLocation{},
				},
			},
		},
	}
	resp, err := svc.Documents.BatchUpdate(ref.ID, req).Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(resp)
	}
	fmt.Printf("appended to %s\n", ref.ID)
	return nil
}

func runDocReplace(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindDoc)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Docs(c)
	if err != nil {
		return err
	}
	req := &docs.BatchUpdateDocumentRequest{
		Requests: []*docs.Request{
			{
				ReplaceAllText: &docs.ReplaceAllTextRequest{
					ReplaceText: docReplaceWith,
					ContainsText: &docs.SubstringMatchCriteria{
						Text:      docReplaceFind,
						MatchCase: docReplaceMatchCase,
					},
				},
			},
		},
	}
	resp, err := svc.Documents.BatchUpdate(ref.ID, req).Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(resp)
	}
	var changed int64
	if len(resp.Replies) > 0 && resp.Replies[0].ReplaceAllText != nil {
		changed = resp.Replies[0].ReplaceAllText.OccurrencesChanged
	}
	fmt.Printf("replaced %d occurrences in %s\n", changed, ref.ID)
	return nil
}

func runDocInsert(cmd *cobra.Command, args []string) error {
	if docInsertAt < 1 {
		return errors.New("--at must be 1 or more: index 0 is the section break, and the first writable position in the body is 1")
	}
	ref, err := mustRef(args[0], glink.KindDoc)
	if err != nil {
		return err
	}
	text, err := readInput(args[1])
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Docs(c)
	if err != nil {
		return err
	}
	req := &docs.BatchUpdateDocumentRequest{
		Requests: []*docs.Request{
			{
				InsertText: &docs.InsertTextRequest{
					Text:     string(text),
					Location: &docs.Location{Index: docInsertAt},
				},
			},
		},
	}
	resp, err := svc.Documents.BatchUpdate(ref.ID, req).Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(resp)
	}
	fmt.Printf("inserted into %s at index %d\n", ref.ID, docInsertAt)
	return nil
}

// docBodyText walks every tab and pulls out every TextRun, including text
// nested inside tables and a table of contents, so nothing is silently
// dropped from the plain-text view. Document.Body is the legacy single-tab
// view, so it is the fallback when the response carries no tab.
func docBodyText(doc *docs.Document) string {
	var sb strings.Builder
	if len(doc.Tabs) > 0 {
		writeDocTabs(&sb, doc.Tabs)
		return sb.String()
	}
	if doc.Body != nil {
		writeDocElements(&sb, doc.Body.Content)
	}
	return sb.String()
}

func writeDocTabs(sb *strings.Builder, tabs []*docs.Tab) {
	for _, t := range tabs {
		if t == nil {
			continue
		}
		if t.DocumentTab != nil && t.DocumentTab.Body != nil {
			writeDocElements(sb, t.DocumentTab.Body.Content)
		}
		writeDocTabs(sb, t.ChildTabs)
	}
}

func writeDocElements(sb *strings.Builder, elems []*docs.StructuralElement) {
	for _, e := range elems {
		switch {
		case e.Paragraph != nil:
			for _, pe := range e.Paragraph.Elements {
				if pe.TextRun != nil {
					sb.WriteString(pe.TextRun.Content)
				}
			}
		case e.Table != nil:
			for _, row := range e.Table.TableRows {
				for _, cell := range row.TableCells {
					writeDocElements(sb, cell.Content)
				}
			}
		case e.TableOfContents != nil:
			writeDocElements(sb, e.TableOfContents.Content)
		}
	}
}
