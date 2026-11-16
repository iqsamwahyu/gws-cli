package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/drive/v3"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

var openCmd = &cobra.Command{
	Use:   "open <link>",
	Short: "Show what a Google Docs, Sheets or Drive link points to",
	Args:  cobra.ExactArgs(1),
	RunE:  runOpen,
}

func init() {
	rootCmd.AddCommand(openCmd)
}

func runOpen(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0])
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Drive(c)
	if err != nil {
		return err
	}
	f, err := svc.Files.Get(ref.ID).
		SupportsAllDrives(true).
		Fields("id,name,mimeType,modifiedTime,size,owners(emailAddress),webViewLink,driveId").
		Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}

	if jsonOut {
		return printJSON(f)
	}

	kind := glink.KindFromMime(f.MimeType)
	w := tw()
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintf(w, "id\t%s\n", f.Id)
	fmt.Fprintf(w, "name\t%s\n", f.Name)
	fmt.Fprintf(w, "kind\t%s\n", kind)
	fmt.Fprintf(w, "mimeType\t%s\n", f.MimeType)
	fmt.Fprintf(w, "modifiedTime\t%s\n", dash(f.ModifiedTime))
	fmt.Fprintf(w, "size\t%s\n", size(f))
	fmt.Fprintf(w, "owners\t%s\n", dash(owners(f)))
	fmt.Fprintf(w, "webViewLink\t%s\n", dash(f.WebViewLink))
	fmt.Fprintf(w, "driveId\t%s\n", dash(f.DriveId))
	if ref.GID >= 0 {
		fmt.Fprintf(w, "gid\t%d\n", ref.GID)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if kind == glink.KindFolder {
		fmt.Print("\nThis link is a folder. gws opens one item at a time and does not list a folder.\n")
		return nil
	}
	fmt.Printf("\nHandled by: %s\n", handler(kind))
	return nil
}

func owners(f *drive.File) string {
	s := make([]string, 0, len(f.Owners))
	for _, o := range f.Owners {
		if o.EmailAddress != "" {
			s = append(s, o.EmailAddress)
		}
	}
	return strings.Join(s, ", ")
}

// size is empty on a Doc or a Sheet, because Google stores no byte count for
// its own formats.
func size(f *drive.File) string {
	if f.Size == 0 {
		return "-"
	}
	return strconv.FormatInt(f.Size, 10)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
