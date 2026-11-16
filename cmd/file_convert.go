package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/drive/v3"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

const (
	mimeDoc    = "application/vnd.google-apps.document"
	mimeSheet  = "application/vnd.google-apps.spreadsheet"
	mimeSlides = "application/vnd.google-apps.presentation"
)

var (
	convertTo   string
	convertName string
)

var fileConvertCmd = &cobra.Command{
	Use:   "convert <link>",
	Short: "Copy an Office file in Drive into a native Google Doc, Sheet or Slides",
	Long: `Copy an Office file in Drive into a native Google Doc, Sheet or Slides.

Drive writes a new native file and leaves the original alone, so the new file
carries a new id. The old link still points at the Office file, and gws prints
the new link for the converted one.

Drive cannot convert a file in place. An update that sends the same bytes with
a Google MIME type is refused with "Invalid MIME type provided for the uploaded
content", so a converted file always has a new link.`,
	Args: cobra.ExactArgs(1),
	RunE: runFileConvert,
}

func init() {
	fileConvertCmd.Flags().StringVar(&convertTo, "to", "", "target type: doc, sheet or slides (default: from the source MIME type)")
	fileConvertCmd.Flags().StringVar(&convertName, "name", "", "name for the new file (default: the original name without its extension)")
	fileCmd.AddCommand(fileConvertCmd)
}

func runFileConvert(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindFile, glink.KindDoc, glink.KindSheet)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Drive(c)
	if err != nil {
		return err
	}
	meta, err := svc.Files.Get(ref.ID).SupportsAllDrives(true).
		Fields("id,name,mimeType").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if strings.HasPrefix(meta.MimeType, "application/vnd.google-apps.") {
		return fmt.Errorf("%s is already a native Google file (%s): there is nothing to convert", meta.Name, meta.MimeType)
	}
	target, err := targetMime(meta.MimeType, convertTo)
	if err != nil {
		return err
	}

	name := convertName
	if name == "" {
		name = strings.TrimSuffix(meta.Name, fileExt(meta.Name))
	}
	copied, err := svc.Files.Copy(ref.ID, &drive.File{Name: name, MimeType: target}).
		SupportsAllDrives(true).Fields("id,name,mimeType,webViewLink").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(copied)
	}
	fmt.Printf("converted %s to a new %s\n  name: %s\n  link: %s\n  the original %s is unchanged\n",
		meta.Name, shortType(copied.MimeType), copied.Name, copied.WebViewLink, ref.ID)
	return nil
}

func targetMime(sourceMime, want string) (string, error) {
	switch strings.ToLower(want) {
	case "doc", "document", "docs":
		return mimeDoc, nil
	case "sheet", "sheets", "spreadsheet":
		return mimeSheet, nil
	case "slides", "presentation":
		return mimeSlides, nil
	case "":
	default:
		return "", fmt.Errorf("--to takes doc, sheet or slides, not %q", want)
	}

	switch {
	case strings.Contains(sourceMime, "wordprocessingml"),
		sourceMime == "application/msword",
		sourceMime == "application/vnd.oasis.opendocument.text",
		sourceMime == "application/rtf",
		sourceMime == "text/plain",
		sourceMime == "text/markdown",
		sourceMime == "text/html":
		return mimeDoc, nil
	case strings.Contains(sourceMime, "spreadsheetml"),
		sourceMime == "application/vnd.ms-excel",
		sourceMime == "application/vnd.oasis.opendocument.spreadsheet",
		sourceMime == "text/csv",
		sourceMime == "text/tab-separated-values":
		return mimeSheet, nil
	case strings.Contains(sourceMime, "presentationml"),
		sourceMime == "application/vnd.ms-powerpoint",
		sourceMime == "application/vnd.oasis.opendocument.presentation":
		return mimeSlides, nil
	}
	return "", fmt.Errorf("gws has no default target for %s: name one with --to doc, --to sheet or --to slides", sourceMime)
}

func shortType(mime string) string {
	switch mime {
	case mimeDoc:
		return "Google Doc"
	case mimeSheet:
		return "Google Sheet"
	case mimeSlides:
		return "Google Slides"
	}
	return mime
}

func fileExt(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[i:]
	}
	return ""
}
