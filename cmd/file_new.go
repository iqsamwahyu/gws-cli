package cmd

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var newFileName string

var fileNewCmd = &cobra.Command{
	Use:   "new <localpath> [--parent <folder link>] [--name <name>]",
	Short: "Upload a local file as a new Drive file and print its link",
	Long: `Upload a local file as a new Drive file and print its link.

The file keeps its own type: a PDF stays a PDF. Drive does not convert it.
gws file put replaces an existing file; this command makes a new one.`,
	Args: cobra.ExactArgs(1),
	RunE: runFileNew,
}

func init() {
	fileNewCmd.Flags().StringVar(&newParent, "parent", "", "folder link to put the new file in (default: My Drive)")
	fileNewCmd.Flags().StringVar(&newFileName, "name", "", "name in Drive (default: the local file name)")
	fileCmd.AddCommand(fileNewCmd)
}

func runFileNew(cmd *cobra.Command, args []string) error {
	path := args[0]
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory: upload one file at a time", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	name := newFileName
	if name == "" {
		name = filepath.Base(path)
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return createItem(name, contentType, data, contentType)
}
