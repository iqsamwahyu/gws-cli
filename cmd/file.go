package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

var fileCmd = &cobra.Command{
	Use:   "file",
	Short: "Get, replace or inspect a plain Drive file by link",
}

var (
	fileGetOutput string
	fileGetExport string
	fileGetForce  bool
	filePutMime   string
)

var fileGetCmd = &cobra.Command{
	Use:   "get <link>",
	Short: "Download a file, or export a Google-native one, to a local path",
	Args:  cobra.ExactArgs(1),
	RunE:  runFileGet,
}

var filePutCmd = &cobra.Command{
	Use:   "put <link> <localpath>",
	Short: "Replace a Drive file's content in place from a local file",
	Args:  cobra.ExactArgs(2),
	RunE:  runFilePut,
}

var fileMetaCmd = &cobra.Command{
	Use:   "meta <link>",
	Short: "Show a Drive file's metadata",
	Args:  cobra.ExactArgs(1),
	RunE:  runFileMeta,
}

func init() {
	fileGetCmd.Flags().StringVarP(&fileGetOutput, "output", "o", "", "local path to write to (default: the file's Drive name); - writes to stdout")
	fileGetCmd.Flags().StringVar(&fileGetExport, "export", "", "export MIME type for a Google-native file (document, spreadsheet, presentation, drawing get a default)")
	fileGetCmd.Flags().BoolVar(&fileGetForce, "force", false, "overwrite the local path if it already exists")

	filePutCmd.Flags().StringVar(&filePutMime, "mime", "", "MIME type to store (default: the type the file already has)")

	fileCmd.AddCommand(fileGetCmd, filePutCmd, fileMetaCmd)
	rootCmd.AddCommand(fileCmd)
}

func runFileGet(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindFile, glink.KindDoc, glink.KindSheet)
	if err != nil {
		return err
	}

	c := ctx()
	svc, err := gsvc.Drive(c)
	if err != nil {
		return err
	}
	meta, err := svc.Files.Get(ref.ID).SupportsAllDrives(true).Fields("id,name,mimeType").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}

	native := strings.HasPrefix(meta.MimeType, "application/vnd.google-apps.")
	if fileGetExport != "" && !native {
		return fmt.Errorf("%s is not a Google-native file (%s): --export converts only a Doc, Sheet, Slides or Drawing. Drop --export to download the file as it is.", ref.ID, meta.MimeType)
	}

	// "-" hands the raw bytes to stdout, so a JSON summary would corrupt them.
	// A Drive item named "-" must not trip the sentinel, so only the flag does.
	toStdout := fileGetOutput == "-" && cmd.Flags().Changed("output")
	out := fileGetOutput
	if out == "" {
		out = filepath.Base(meta.Name)
		if out == "" || out == "." || out == ".." || out == "-" || strings.ContainsRune(out, filepath.Separator) {
			return fmt.Errorf("the Drive name %q is not a usable local file name: pass -o <path>", meta.Name)
		}
	}
	if !toStdout && !fileGetForce {
		if _, err := os.Stat(out); err == nil {
			return fmt.Errorf("%s already exists: pass --force to overwrite", out)
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	// the media transfer gets its own deadline, because a large file does not
	// move inside the 60 s that ctx gives one API call
	lc, cancel := ctxLong()
	defer cancel()

	var resp *http.Response
	if native {
		exportMime := fileGetExport
		if exportMime == "" {
			exportMime, err = defaultExportMime(meta.MimeType)
			if err != nil {
				return err
			}
		}
		resp, err = svc.Files.Export(ref.ID, exportMime).Context(lc).Download()
	} else {
		resp, err = svc.Files.Get(ref.ID).SupportsAllDrives(true).Context(lc).Download()
	}
	if err != nil {
		return gsvc.Explain(err)
	}
	defer resp.Body.Close()

	if toStdout {
		_, err := io.Copy(os.Stdout, resp.Body)
		return err
	}

	n, err := downloadToFile(resp.Body, out)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(map[string]any{
			"id":       meta.Id,
			"name":     meta.Name,
			"mimeType": meta.MimeType,
			"path":     out,
			"bytes":    n,
		})
	}
	fmt.Printf("wrote %s (%d bytes)\n", out, n)
	return nil
}

// downloadToFile streams into a sibling temp file and renames it into place,
// so a transfer that fails part way cannot truncate the previous copy.
func downloadToFile(body io.Reader, out string) (int64, error) {
	w, err := os.CreateTemp(filepath.Dir(out), ".gws-*")
	if err != nil {
		return 0, err
	}
	tmp := w.Name()
	n, err := io.Copy(w, body)
	if err != nil {
		_ = w.Close()
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := w.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	// CreateTemp gives 0600; os.Create gave 0644 before the rename was added
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, nil
}

// defaultExportMime picks an export format for the Google-native mime types
// that have one obvious choice; anything else needs an explicit --export.
func defaultExportMime(mime string) (string, error) {
	switch mime {
	case "application/vnd.google-apps.document":
		return "text/plain", nil
	case "application/vnd.google-apps.spreadsheet":
		return "text/csv", nil
	case "application/vnd.google-apps.presentation":
		return "application/pdf", nil
	case "application/vnd.google-apps.drawing":
		return "image/png", nil
	default:
		return "", fmt.Errorf("%s has no default export format: pass --export", mime)
	}
}

func runFilePut(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindFile, glink.KindDoc, glink.KindSheet)
	if err != nil {
		return err
	}
	localPath := args[1]

	c := ctx()
	svc, err := gsvc.Drive(c)
	if err != nil {
		return err
	}

	meta, err := svc.Files.Get(ref.ID).SupportsAllDrives(true).Fields("mimeType").Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if strings.HasPrefix(meta.MimeType, "application/vnd.google-apps.") {
		next := ""
		switch k := glink.KindFromMime(meta.MimeType); k {
		case glink.KindDoc, glink.KindSheet:
			next = fmt.Sprintf(" Edit it with %s.", handler(k))
		}
		return fmt.Errorf("%s is a Google-native file (%s): a raw upload would need a format conversion this command does not do.%s", ref.ID, meta.MimeType, next)
	}

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// the upload gets its own deadline; ctx() is sized for a metadata call
	lc, cancel := ctxLong()
	defer cancel()

	// the upload must declare the type, because the client otherwise sniffs the
	// bytes: a .docx is a zip container, and Drive would relabel the file
	// application/zip and stop opening it as a document
	upMime := meta.MimeType
	if filePutMime != "" {
		upMime = filePutMime
	}
	updated, err := svc.Files.Update(ref.ID, &drive.File{MimeType: upMime}).
		Media(f, googleapi.ContentType(upMime)).SupportsAllDrives(true).
		Fields("size,modifiedTime,mimeType").Context(lc).Do()
	if err != nil {
		return gsvc.Explain(err)
	}

	if jsonOut {
		return printJSON(updated)
	}
	fmt.Printf("replaced %s (%d bytes, %s, modified %s)\n", ref.ID, updated.Size, updated.MimeType, updated.ModifiedTime)
	if updated.MimeType != upMime {
		fmt.Printf("warning: Drive stored the type %s, not %s\n", updated.MimeType, upMime)
	}
	return nil
}

func runFileMeta(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindFile, glink.KindDoc, glink.KindSheet, glink.KindFolder)
	if err != nil {
		return err
	}

	c := ctx()
	svc, err := gsvc.Drive(c)
	if err != nil {
		return err
	}
	f, err := svc.Files.Get(ref.ID).SupportsAllDrives(true).
		Fields("id,name,mimeType,size,modifiedTime,owners(emailAddress),webViewLink,md5Checksum,driveId").
		Context(c).Do()
	if err != nil {
		return gsvc.Explain(err)
	}

	if jsonOut {
		return printJSON(f)
	}

	w := tw()
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintf(w, "id\t%s\n", f.Id)
	fmt.Fprintf(w, "name\t%s\n", f.Name)
	fmt.Fprintf(w, "mimeType\t%s\n", f.MimeType)
	fmt.Fprintf(w, "size\t%s\n", size(f))
	fmt.Fprintf(w, "modifiedTime\t%s\n", dash(f.ModifiedTime))
	fmt.Fprintf(w, "owners\t%s\n", dash(owners(f)))
	fmt.Fprintf(w, "webViewLink\t%s\n", dash(f.WebViewLink))
	fmt.Fprintf(w, "md5Checksum\t%s\n", dash(f.Md5Checksum))
	fmt.Fprintf(w, "driveId\t%s\n", dash(f.DriveId))
	return w.Flush()
}
