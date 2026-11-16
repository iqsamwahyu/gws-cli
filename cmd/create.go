package cmd

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

const mimeFolder = "application/vnd.google-apps.folder"

var (
	newParent string
	newFrom   string
	newCSV    string
	moveTo    string
)

var folderCmd = &cobra.Command{Use: "folder", Short: "Make a Drive folder"}

var folderNewCmd = &cobra.Command{
	Use:   "new <name> [--parent <folder link>]",
	Short: "Make a folder and print its link",
	Args:  cobra.ExactArgs(1),
	RunE:  func(cmd *cobra.Command, args []string) error { return createItem(args[0], mimeFolder, nil, "") },
}

var docNewCmd = &cobra.Command{
	Use:   "new <title> [--parent <folder link>] [--from <text|@file|->]",
	Short: "Make a Google Doc and print its link",
	Long: `Make a Google Doc and print its link.

--from fills the new Doc. Drive imports the content: a .md or .html file keeps
its headings and lists, and anything else arrives as plain text.`,
	Args: cobra.ExactArgs(1),
	RunE: runDocNew,
}

var sheetNewCmd = &cobra.Command{
	Use:   "new <title> [--parent <folder link>] [--csv <@file|->]",
	Short: "Make a Google Sheet and print its link",
	Long: `Make a Google Sheet and print its link.

--csv fills the first tab. Drive imports the CSV, so a formula or a date is
typed as if a person entered it.`,
	Args: cobra.ExactArgs(1),
	RunE: runSheetNew,
}

var fileMoveCmd = &cobra.Command{
	Use:   "move <link> --to <folder link>",
	Short: "Move an item into a folder",
	Long: `Move an item into a folder.

The item keeps its id, its link and its sharing. It leaves every folder it sat
in before, so it lives in one place.`,
	Args: cobra.ExactArgs(1),
	RunE: runFileMove,
}

func init() {
	for _, c := range []*cobra.Command{folderNewCmd, docNewCmd, sheetNewCmd} {
		c.Flags().StringVar(&newParent, "parent", "", "folder link to put the new item in (default: My Drive)")
	}
	docNewCmd.Flags().StringVar(&newFrom, "from", "", "starting content: a literal string, @file, or - for stdin")
	sheetNewCmd.Flags().StringVar(&newCSV, "csv", "", "starting rows: @file or - for stdin")
	fileMoveCmd.Flags().StringVar(&moveTo, "to", "", "folder link to move the item into (required)")
	_ = fileMoveCmd.MarkFlagRequired("to")

	folderCmd.AddCommand(folderNewCmd)
	docCmd.AddCommand(docNewCmd)
	sheetCmd.AddCommand(sheetNewCmd)
	fileCmd.AddCommand(fileMoveCmd)
	rootCmd.AddCommand(folderCmd)
}

func runDocNew(cmd *cobra.Command, args []string) error {
	if newFrom == "" {
		return createItem(args[0], mimeDoc, nil, "")
	}
	data, err := readInput(newFrom)
	if err != nil {
		return err
	}
	return createItem(args[0], mimeDoc, data, importType(newFrom))
}

func runSheetNew(cmd *cobra.Command, args []string) error {
	if newCSV == "" {
		return createItem(args[0], mimeSheet, nil, "")
	}
	data, err := readInput(newCSV)
	if err != nil {
		return err
	}
	return createItem(args[0], mimeSheet, data, "text/csv")
}

// createItem makes one Drive item. With content, Drive imports the bytes into
// the Google type, which is the only conversion the API does on its own.
func createItem(name, mime string, content []byte, contentType string) error {
	c := ctx()
	svc, err := gsvc.Drive(c)
	if err != nil {
		return err
	}
	f := &drive.File{Name: name, MimeType: mime}
	parentName := "My Drive"
	if newParent != "" {
		p, err := folderOf(svc, newParent)
		if err != nil {
			return err
		}
		f.Parents = []string{p.Id}
		parentName = p.Name
	}

	call := svc.Files.Create(f).SupportsAllDrives(true).Fields("id,name,mimeType,webViewLink")
	if content != nil {
		call = call.Media(bytes.NewReader(content), googleapi.ContentType(contentType))
	}
	made, err := call.Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(made)
	}
	fmt.Printf("made %s %q in %s\n  link: %s\n", kindWord(made.MimeType), made.Name, parentName, made.WebViewLink)
	return nil
}

func runFileMove(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindFile, glink.KindDoc, glink.KindSheet, glink.KindFolder)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Drive(c)
	if err != nil {
		return err
	}
	to, err := folderOf(svc, moveTo)
	if err != nil {
		return err
	}
	if to.Id == ref.ID {
		return fmt.Errorf("an item cannot move into itself")
	}
	cur, err := svc.Files.Get(ref.ID).SupportsAllDrives(true).Fields("name,parents").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	for _, p := range cur.Parents {
		if p == to.Id {
			fmt.Printf("%q is already in %q\n", cur.Name, to.Name)
			return nil
		}
	}
	moved, err := svc.Files.Update(ref.ID, &drive.File{}).
		AddParents(to.Id).RemoveParents(strings.Join(cur.Parents, ",")).
		SupportsAllDrives(true).Fields("id,name,parents,webViewLink").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(moved)
	}
	fmt.Printf("moved %q into %q\n  link: %s (unchanged)\n", moved.Name, to.Name, moved.WebViewLink)
	return nil
}

// folderOf resolves a --parent or --to link and refuses anything that is not
// a folder, because Drive accepts a Doc id as a parent and then fails later.
func folderOf(svc *drive.Service, link string) (*drive.File, error) {
	ref, err := glink.Parse(link)
	if err != nil {
		return nil, err
	}
	f, err := svc.Files.Get(ref.ID).SupportsAllDrives(true).Fields("id,name,mimeType").Context(ctx()).Do()
	if err != nil {
		return nil, gsvc.Explain(err)
	}
	if f.MimeType != mimeFolder {
		return nil, fmt.Errorf("%q is a %s, not a folder: pass a folder link (gws folder new makes one)", f.Name, kindWord(f.MimeType))
	}
	return f, nil
}

func importType(arg string) string {
	lower := strings.ToLower(arg)
	switch {
	case strings.HasPrefix(lower, "@") && strings.HasSuffix(lower, ".md"):
		return "text/markdown"
	case strings.HasPrefix(lower, "@") && (strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm")):
		return "text/html"
	}
	return "text/plain"
}

func kindWord(mime string) string {
	switch mime {
	case mimeFolder:
		return "folder"
	case mimeDoc:
		return "Doc"
	case mimeSheet:
		return "Sheet"
	case mimeSlides:
		return "Slides"
	}
	return "file"
}
