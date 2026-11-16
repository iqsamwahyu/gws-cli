package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/drive/v3"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

// the comments endpoints reject a call without a field mask
const (
	commentFields = "comments(id,author(displayName,emailAddress),createdTime,modifiedTime,content,resolved,quotedFileContent(value),replies(id,author(displayName),createdTime,content,action)),nextPageToken"
	oneComment    = "id,author(displayName,emailAddress),createdTime,content,resolved"
	oneReply      = "id,author(displayName),createdTime,content,action"
)

var (
	commentAll      bool
	commentMentions []string
)

var commentCmd = &cobra.Command{
	Use:   "comment",
	Short: "Read and write the comment threads on one Drive item",
	Long: `Read and write the comment threads on one Drive item.

These are the Drive comments that appear in the comment pane of a Doc, a Sheet
or a PDF. A comment gws creates sits on the file, not on a paragraph or a cell:
the Docs API has no comment endpoints, and the Drive anchor field is not
documented for authoring. For a note pinned to one cell, use gws sheet note.`,
}

var commentListCmd = &cobra.Command{
	Use:   "list <link>",
	Short: "List the comment threads, open ones by default",
	Args:  cobra.ExactArgs(1),
	RunE:  runCommentList,
}

var commentAddCmd = &cobra.Command{
	Use:   "add <link> <text|-|@file>",
	Short: "Start a comment thread on the file",
	Args:  cobra.ExactArgs(2),
	RunE:  runCommentAdd,
}

var commentReplyCmd = &cobra.Command{
	Use:   "reply <link> <commentID> <text|-|@file>",
	Short: "Reply inside an existing thread",
	Args:  cobra.ExactArgs(3),
	RunE:  runCommentReply,
}

var commentResolveCmd = &cobra.Command{
	Use:   "resolve <link> <commentID>",
	Short: "Resolve a thread by replying with the resolve action",
	Args:  cobra.ExactArgs(2),
	RunE:  runCommentResolve,
}

func init() {
	commentListCmd.Flags().BoolVar(&commentAll, "all", false, "include the resolved threads")
	for _, c := range []*cobra.Command{commentAddCmd, commentReplyCmd} {
		c.Flags().StringArrayVar(&commentMentions, "mention", nil, "email address to mention; repeatable")
	}
	commentCmd.AddCommand(commentListCmd, commentAddCmd, commentReplyCmd, commentResolveCmd)
	rootCmd.AddCommand(commentCmd)
}

func commentTarget(arg string) (glink.Ref, *drive.Service, error) {
	ref, err := mustRef(arg, glink.KindFile, glink.KindDoc, glink.KindSheet)
	if err != nil {
		return ref, nil, err
	}
	svc, err := gsvc.Drive(ctx())
	return ref, svc, err
}

func runCommentList(cmd *cobra.Command, args []string) error {
	ref, svc, err := commentTarget(args[0])
	if err != nil {
		return err
	}
	c := ctx()

	var all []*drive.Comment
	token := ""
	for {
		call := svc.Comments.List(ref.ID).Fields(commentFields).PageSize(100).Context(c)
		if token != "" {
			call = call.PageToken(token)
		}
		page, err := call.Do()
		if err != nil {
			return gsvc.Explain(err)
		}
		all = append(all, page.Comments...)
		if token = page.NextPageToken; token == "" {
			break
		}
	}

	shown := all[:0:0]
	for _, cm := range all {
		if cm.Resolved && !commentAll {
			continue
		}
		shown = append(shown, cm)
	}

	if jsonOut {
		return printJSON(map[string]any{"file": ref.ID, "comments": shown})
	}
	if len(shown) == 0 {
		word := "open comment"
		if commentAll {
			word = "comment"
		}
		fmt.Printf("no %s on %s\n", word, ref.ID)
		return nil
	}
	w := tw()
	fmt.Fprintln(w, "ID\tAUTHOR\tCREATED\tSTATE\tREPLIES\tCOMMENT")
	for _, cm := range shown {
		state := "open"
		if cm.Resolved {
			state = "resolved"
		}
		text := oneLine(cm.Content)
		if cm.QuotedFileContent != nil && cm.QuotedFileContent.Value != "" {
			text = fmt.Sprintf("[on %q] %s", oneLine(cm.QuotedFileContent.Value), text)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			cm.Id, authorOf(cm.Author), shortTime(cm.CreatedTime), state, len(cm.Replies), text)
	}
	return w.Flush()
}

func runCommentAdd(cmd *cobra.Command, args []string) error {
	ref, svc, err := commentTarget(args[0])
	if err != nil {
		return err
	}
	text, err := readInput(args[1])
	if err != nil {
		return err
	}
	content, err := withMentions(string(text))
	if err != nil {
		return err
	}
	created, err := svc.Comments.Create(ref.ID, &drive.Comment{Content: content}).
		Fields(oneComment).Context(ctx()).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(created)
	}
	fmt.Printf("commented on %s\n  id:      %s\n  content: %s\n", ref.ID, created.Id, oneLine(created.Content))
	mentionNote()
	return nil
}

func runCommentReply(cmd *cobra.Command, args []string) error {
	ref, svc, err := commentTarget(args[0])
	if err != nil {
		return err
	}
	text, err := readInput(args[2])
	if err != nil {
		return err
	}
	content, err := withMentions(string(text))
	if err != nil {
		return err
	}
	created, err := svc.Replies.Create(ref.ID, args[1], &drive.Reply{Content: content}).
		Fields(oneReply).Context(ctx()).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(created)
	}
	fmt.Printf("replied in %s\n  id:      %s\n  content: %s\n", args[1], created.Id, oneLine(created.Content))
	mentionNote()
	return nil
}

func runCommentResolve(cmd *cobra.Command, args []string) error {
	ref, svc, err := commentTarget(args[0])
	if err != nil {
		return err
	}
	// Drive marks a thread resolved through a reply that carries the action,
	// because the resolved field of a comment is read only
	created, err := svc.Replies.Create(ref.ID, args[1], &drive.Reply{Action: "resolve"}).
		Fields(oneReply).Context(ctx()).Do()
	if err != nil {
		return gsvc.Explain(err)
	}
	if jsonOut {
		return printJSON(created)
	}
	fmt.Printf("resolved %s\n", args[1])
	return nil
}

// withMentions puts a +address token in front of the text for each --mention.
// That token is the way a mention is written in a Google comment.
func withMentions(text string) (string, error) {
	if len(commentMentions) == 0 {
		return text, nil
	}
	var tokens []string
	for _, m := range commentMentions {
		m = strings.TrimSpace(strings.TrimPrefix(m, "+"))
		if !strings.Contains(m, "@") || strings.HasPrefix(m, "@") {
			return "", fmt.Errorf("--mention takes an email address, not %q", m)
		}
		tokens = append(tokens, "+"+m)
	}
	return strings.Join(tokens, " ") + " " + text, nil
}

func mentionNote() {
	if len(commentMentions) == 0 {
		return
	}
	fmt.Printf("mentioned: %s\n", strings.Join(commentMentions, ", "))
	fmt.Println("Google decides whether a mention made through the API sends mail; check the document to confirm the chip.")
}

func authorOf(u *drive.User) string {
	if u == nil {
		return "unknown"
	}
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.EmailAddress
}

// shortTime keeps the table narrow; the full RFC 3339 value stays in --json.
func shortTime(t string) string {
	if len(t) >= 16 {
		return strings.Replace(t[:16], "T", " ", 1)
	}
	return t
}
