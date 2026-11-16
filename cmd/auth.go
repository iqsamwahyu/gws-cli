package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/iqsamwahyu/gws-cli/internal/config"
	"github.com/iqsamwahyu/gws-cli/internal/gauth"
)

var authCmd = &cobra.Command{Use: "auth", Short: "Manage the Google credential"}

var (
	authKeyFile     string
	authImpersonate string
	authOAuth       bool
)

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in with a service account key, or with an OAuth desktop client",
	Long: `Log in with a service account key, or with an OAuth desktop client.

  gws auth login --service-account /path/key.json [--impersonate user@example.com]
  gws auth login --oauth

A service account reaches only the items that somebody shares with its email
address. That per item sharing is the security model: share each Doc, Sheet or
folder with the service account as Editor before gws can open it.

The credential goes into the OS keyring only. It is never written to a file.`,
	RunE: runAuthLogin,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored credential from the OS keyring",
	RunE:  runAuthLogout,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current auth status (never prints a secret)",
	RunE:  runAuthStatus,
}

func init() {
	authLoginCmd.Flags().StringVar(&authKeyFile, "service-account", "", "path to a service account key JSON; gws reaches only what is shared with its client_email")
	authLoginCmd.Flags().StringVar(&authImpersonate, "impersonate", "", "user to impersonate through domain-wide delegation")
	authLoginCmd.Flags().BoolVar(&authOAuth, "oauth", false, "log in as yourself with an OAuth desktop client (loopback + PKCE)")
	authCmd.AddCommand(authLoginCmd, authLogoutCmd, authStatusCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	switch {
	case authKeyFile != "" && authOAuth:
		return errors.New("choose one form: --service-account <key.json> or --oauth")
	case authOAuth && authImpersonate != "":
		return errors.New("--impersonate works only with --service-account: domain-wide delegation needs a service account key")
	case authKeyFile != "":
		return loginServiceAccount()
	case authOAuth:
		return loginOAuth()
	default:
		return errors.New("name the credential. Use one of:\n  gws auth login --service-account <key.json> [--impersonate user@example.com]\n  gws auth login --oauth")
	}
}

func loginServiceAccount() error {
	keyJSON, err := os.ReadFile(authKeyFile)
	if err != nil {
		return fmt.Errorf("read the service account key: %w", err)
	}
	c := ctx()
	rec, err := gauth.LoginServiceAccount(c, keyJSON, authImpersonate)
	if err != nil {
		return err
	}
	if err := probe(c, rec); err != nil {
		return err
	}
	if err := persist(rec); err != nil {
		return err
	}

	if jsonOut {
		return printJSON(recordFields(rec))
	}
	fmt.Printf("Account: %s\nMode:    %s\nStore:   credential in OS keyring\n", rec.Account, rec.Mode)
	if rec.Impersonate != "" {
		fmt.Printf("Impersonate: %s\n", rec.Impersonate)
	}
	fmt.Printf("Share each Doc, Sheet or folder with %s as Editor: gws opens only what is shared with it.\n", rec.Account)
	return nil
}

func loginOAuth() error {
	r := bufio.NewReader(os.Stdin)
	clientID := prompt(r, "OAuth client id: ")
	if clientID == "" {
		return errors.New("the client id is required")
	}
	fmt.Fprint(os.Stderr, "OAuth client secret: ")
	secretBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("read the client secret: %w", err)
	}
	clientSecret := strings.TrimSpace(string(secretBytes))
	if clientSecret == "" {
		return errors.New("the client secret is required")
	}

	// the user consents in a browser, so this flow gets more time than ctx()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rec, err := gauth.LoginOAuth(c, clientID, clientSecret)
	if err != nil {
		return err
	}
	if err := probe(c, rec); err != nil {
		return err
	}
	if err := persist(rec); err != nil {
		return err
	}
	if jsonOut {
		return printJSON(recordFields(rec))
	}
	fmt.Printf("Account: %s\nMode:    %s\nStore:   credential in OS keyring\n", rec.Account, rec.Mode)
	return nil
}

// probe validates the credential against Drive before anything is persisted.
func probe(c context.Context, rec *gauth.Record) error {
	ts, err := gauth.TokenSource(c, rec)
	if err != nil {
		return err
	}
	svc, err := drive.NewService(c, option.WithTokenSource(ts))
	if err != nil {
		return fmt.Errorf("build the Drive client: %w", err)
	}
	if _, err := svc.About.Get().Fields("user", "storageQuota").Context(c).Do(); err != nil {
		return fmt.Errorf("the credential does not work: %w", err)
	}
	return nil
}

func persist(rec *gauth.Record) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	old := cfg.Account
	if err := gauth.Save(rec); err != nil {
		return err
	}
	// a login under a new account must not leave the previous credential in
	// the keyring, because no gws command can reach it any more
	if old != "" && old != rec.Account {
		if err := gauth.Delete(old); err != nil && !errors.Is(err, gauth.ErrNotFound) {
			return fmt.Errorf("OS keyring: remove the credential of %s: %w", old, err)
		}
	}
	cfg.Account = rec.Account
	cfg.AuthMode = rec.Mode
	return config.Save(cfg)
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Account == "" {
		return errors.New("not logged in")
	}
	if err := gauth.Delete(cfg.Account); err != nil && !errors.Is(err, gauth.ErrNotFound) {
		return fmt.Errorf("OS keyring: %w", err)
	}
	if jsonOut {
		return printJSON(map[string]string{"account": cfg.Account, "status": "logged out"})
	}
	fmt.Printf("Logged out %s (keyring entry removed, config kept)\n", cfg.Account)
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Account == "" {
		return errors.New("not logged in: " + gauth.LoginHint)
	}
	rec, err := gauth.Load(cfg.Account)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(recordFields(rec))
	}
	fmt.Printf("Account: %s\nMode:    %s\nStore:   OS keyring\n", rec.Account, rec.Mode)
	if rec.Impersonate != "" {
		fmt.Printf("Impersonate: %s\n", rec.Impersonate)
	}
	return nil
}

// recordFields is the JSON shape of a credential. It never carries a token, a
// secret or a private key.
func recordFields(rec *gauth.Record) map[string]string {
	return map[string]string{
		"account":     rec.Account,
		"mode":        rec.Mode,
		"impersonate": rec.Impersonate,
		"store":       "OS keyring",
	}
}

func prompt(r *bufio.Reader, label string) string {
	fmt.Fprint(os.Stderr, label)
	s, _ := r.ReadString('\n')
	return strings.TrimSpace(s)
}
