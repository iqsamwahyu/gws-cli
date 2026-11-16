// Package gsvc builds the Google API clients from the stored credential.
package gsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oauth2"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"

	"github.com/iqsamwahyu/gws-cli/internal/config"
	"github.com/iqsamwahyu/gws-cli/internal/gauth"
)

// Drive builds the Drive client for the configured account.
func Drive(ctx context.Context) (*drive.Service, error) {
	ts, err := tokenSource(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := drive.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("build the Drive client: %w", err)
	}
	return svc, nil
}

// Docs builds the Google Docs client for the configured account.
func Docs(ctx context.Context) (*docs.Service, error) {
	ts, err := tokenSource(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := docs.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("build the Docs client: %w", err)
	}
	return svc, nil
}

// Sheets builds the Google Sheets client for the configured account.
func Sheets(ctx context.Context) (*sheets.Service, error) {
	ts, err := tokenSource(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := sheets.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("build the Sheets client: %w", err)
	}
	return svc, nil
}

// Record returns the credential record of the configured account.
func Record() (*gauth.Record, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg.Account == "" {
		return nil, fmt.Errorf("not logged in: %s", gauth.LoginHint)
	}
	return gauth.Load(cfg.Account)
}

func tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	rec, err := Record()
	if err != nil {
		return nil, err
	}
	return gauth.TokenSource(ctx, rec)
}

// Explain turns a Google API error into the action the user must take.
func Explain(err error) error {
	var gerr *googleapi.Error
	if err == nil || !errors.As(err, &gerr) {
		return err
	}
	account, mode := who()
	reason := reasonOf(gerr)

	switch {
	case gerr.Code == 401, gerr.Code == 403 && reason == "insufficientPermissions":
		return fmt.Errorf("google %d: %s does not carry the scopes gws needs (%s).\nLog in again: gws auth login",
			gerr.Code, account, strings.Join(gauth.Scopes(), " "))
	case gerr.Code == 403 && apiDisabled(reason, gerr.Message):
		return fmt.Errorf("google 403: this command needs a Google API that the Cloud project does not have enabled.\n%s", gerr.Message)
	case gerr.Code == 403 && mode == gauth.ModeServiceAccount:
		return fmt.Errorf("google 403: the service account %s is not allowed to open this item.\nShare the Doc, Sheet or folder with %s as Editor.",
			account, account)
	case gerr.Code == 403:
		return fmt.Errorf("google 403: %s is not allowed to open this item: %s", account, gerr.Message)
	case gerr.Code == 404:
		return fmt.Errorf("google 404: no item answers this id. Two causes: the link is wrong, or the item is not shared with %s.", account)
	case gerr.Code == 400 && officeFile(gerr.Message):
		return fmt.Errorf("google 400: this link points at an Office file in Drive, not a native Google Doc or Sheet.\n" +
			"Two ways on: convert it in Drive with File > Save as Google Docs (or Google Sheets), which leaves the original alone,\n" +
			"or work on the file itself: gws file get <link>")
	}
	return fmt.Errorf("google %d: %s", gerr.Code, gerr.Message)
}

// who names the configured account for an error message. It stays quiet when
// the config cannot be read, because the caller already holds the real error.
func who() (account, mode string) {
	cfg, err := config.Load()
	if err != nil || cfg.Account == "" {
		return "this account", ""
	}
	return cfg.Account, cfg.AuthMode
}

// apiDisabled separates "the project never switched this API on" from a real
// permission refusal. Google answers both with 403, and the two need opposite
// actions.
func apiDisabled(reason, message string) bool {
	return reason == "accessNotConfigured" ||
		strings.Contains(message, "has not been used in project") ||
		strings.Contains(message, "it is disabled")
}

// officeFile reports the Docs and Sheets refusal of a .docx or .xlsx that
// lives in Drive but was never converted.
func officeFile(message string) bool {
	return strings.Contains(message, "must not be an Office file") ||
		strings.Contains(message, "This operation is not supported for this document")
}

func reasonOf(gerr *googleapi.Error) string {
	for _, e := range gerr.Errors {
		if e.Reason != "" {
			return e.Reason
		}
	}
	return ""
}
