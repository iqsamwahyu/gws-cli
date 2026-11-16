package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

var rootCmd = &cobra.Command{
	Use:   "gws",
	Short: "Open and edit one Google Doc, Sheet or Drive file by link",
	Long: `gws opens and edits the one Google Doc, Sheet or Drive file that you name by
link, and every command takes that link as its argument.

gws cannot browse Drive: it holds no list, no search, no delete and no share,
so it reaches one item at a time and only the items shared with your account.`,
	SilenceUsage: true,
}

var jsonOut bool

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "print the raw fields as JSON")
}

// ctx gives every command one deadline. AfterFunc releases the timer when the
// deadline passes; the process ends before that on a normal run.
func ctx() context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	context.AfterFunc(c, cancel)
	return c
}

// ctxLong gives a media transfer its own deadline. A large download or upload
// cannot finish inside the 60 s that ctx gives one API call.
func ctxLong() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Minute)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func tw() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}

// readInput reads "-" from stdin, "@path" from that file, anything else as the
// literal string.
func readInput(arg string) ([]byte, error) {
	switch {
	case arg == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return b, nil
	case strings.HasPrefix(arg, "@"):
		p := strings.TrimPrefix(arg, "@")
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		return b, nil
	default:
		return []byte(arg), nil
	}
}

// mustRef parses the link. With want set, a link that does not name its kind
// is resolved through Drive, and a kind outside want is an error that names
// the command which does take that kind.
func mustRef(arg string, want ...glink.Kind) (glink.Ref, error) {
	ref, err := glink.Parse(arg)
	if err != nil {
		return glink.Ref{}, err
	}
	if len(want) == 0 {
		return ref, nil
	}
	if ref.Kind == glink.KindUnknown {
		k, err := resolveKind(ref.ID)
		if err != nil {
			return glink.Ref{}, err
		}
		ref.Kind = k
	}
	for _, w := range want {
		if ref.Kind == w {
			return ref, nil
		}
	}
	return glink.Ref{}, fmt.Errorf("this link is a %s, and this command takes a %s: use %s", ref.Kind, kindList(want), handler(ref.Kind))
}

func resolveKind(id string) (glink.Kind, error) {
	c := ctx()
	svc, err := gsvc.Drive(c)
	if err != nil {
		return glink.KindUnknown, err
	}
	f, err := svc.Files.Get(id).SupportsAllDrives(true).Fields("mimeType").Context(c).Do()
	if err != nil {
		return glink.KindUnknown, gsvc.Explain(err)
	}
	return glink.KindFromMime(f.MimeType), nil
}

// handler names the command that works on one kind.
func handler(k glink.Kind) string {
	switch k {
	case glink.KindDoc:
		return "gws doc"
	case glink.KindSheet:
		return "gws sheet"
	case glink.KindFolder:
		return "gws open"
	default:
		return "gws file"
	}
}

func kindList(kinds []glink.Kind) string {
	s := make([]string, 0, len(kinds))
	for _, k := range kinds {
		s = append(s, string(k))
	}
	return strings.Join(s, " or ")
}
