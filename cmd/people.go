package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"google.golang.org/api/drive/v3"

	"github.com/iqsamwahyu/gws-cli/internal/glink"
	"github.com/iqsamwahyu/gws-cli/internal/gsvc"
)

var peopleCmd = &cobra.Command{
	Use:   "people <link>",
	Short: "List who can reach this item, with their email addresses",
	Long: `List who can reach this item, with their email addresses.

This reads the permissions of the one item in the link. It does not change
them: gws cannot share, and it cannot remove access.

Use it to find the address to pass to gws comment --mention, because a Drive
comment names its author but does not carry an email address.

Drive withholds an address in some cases, such as a link share or a share to a
whole domain. A row with no address says so instead of showing a blank.`,
	Args: cobra.ExactArgs(1),
	RunE: runPeople,
}

func init() {
	rootCmd.AddCommand(peopleCmd)
}

func runPeople(cmd *cobra.Command, args []string) error {
	ref, err := mustRef(args[0], glink.KindFile, glink.KindDoc, glink.KindSheet, glink.KindFolder)
	if err != nil {
		return err
	}
	c := ctx()
	svc, err := gsvc.Drive(c)
	if err != nil {
		return err
	}

	var all []*drive.Permission
	token := ""
	for {
		call := svc.Permissions.List(ref.ID).SupportsAllDrives(true).PageSize(100).
			Fields("permissions(id,type,role,emailAddress,displayName,domain),nextPageToken").Context(c)
		if token != "" {
			call = call.PageToken(token)
		}
		page, err := call.Do()
		if err != nil {
			return gsvc.Explain(err)
		}
		all = append(all, page.Permissions...)
		if token = page.NextPageToken; token == "" {
			break
		}
	}

	sort.SliceStable(all, func(i, j int) bool { return roleRank(all[i].Role) < roleRank(all[j].Role) })

	if jsonOut {
		return printJSON(map[string]any{"file": ref.ID, "permissions": all})
	}
	if len(all) == 0 {
		fmt.Printf("no permission is readable on %s: the account may not be allowed to see them\n", ref.ID)
		return nil
	}
	w := tw()
	fmt.Fprintln(w, "NAME\tEMAIL\tROLE\tTYPE")
	for _, p := range all {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", orDash(p.DisplayName), addressOf(p), p.Role, p.Type)
	}
	return w.Flush()
}

// addressOf says why an address is absent rather than printing a blank, since
// a blank reads as a bug and the absence is the answer.
func addressOf(p *drive.Permission) string {
	if p.EmailAddress != "" {
		return p.EmailAddress
	}
	switch p.Type {
	case "domain":
		return "(everyone at " + orDash(p.Domain) + ")"
	case "anyone":
		return "(anyone with the link)"
	}
	return "(withheld by Drive)"
}

func roleRank(role string) int {
	switch role {
	case "owner":
		return 0
	case "organizer", "fileOrganizer":
		return 1
	case "writer":
		return 2
	case "commenter":
		return 3
	case "reader":
		return 4
	}
	return 5
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
