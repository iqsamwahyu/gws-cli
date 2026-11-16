// Package gauth logs in to Google and keeps the credential in the OS keyring.
package gauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/iqsamwahyu/gws-cli/internal/keyring"
)

const (
	ModeServiceAccount = "service_account"
	ModeOAuth          = "oauth"
)

// LoginHint is the one sentence every "no credential" error ends with.
const LoginHint = "run: gws auth login --service-account <key.json>  (or: gws auth login --oauth)"

// ErrNotFound is returned when the keyring holds no credential for an account.
var ErrNotFound = keyring.ErrNotFound

// Record is the credential as it is stored in the OS keyring, as JSON.
type Record struct {
	Account        string          `json:"account"`
	Mode           string          `json:"mode"`
	ClientID       string          `json:"client_id,omitempty"`
	ClientSecret   string          `json:"client_secret,omitempty"`
	Token          *oauth2.Token   `json:"token,omitempty"`
	ServiceAccount json.RawMessage `json:"service_account,omitempty"`
	Impersonate    string          `json:"impersonate,omitempty"`
}

// Scopes are the only scopes gws asks for.
func Scopes() []string {
	return []string{
		"https://www.googleapis.com/auth/documents",
		"https://www.googleapis.com/auth/spreadsheets",
		"https://www.googleapis.com/auth/drive",
	}
}

// LoginServiceAccount builds a record from a service account key file. The
// account name is the key's client_email. impersonate may be empty; when it is
// set, the JWT carries it as the Subject for domain-wide delegation.
func LoginServiceAccount(ctx context.Context, keyJSON []byte, impersonate string) (*Record, error) {
	var key struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(keyJSON, &key); err != nil {
		return nil, fmt.Errorf("the service account key is not valid JSON: %w", err)
	}
	if key.ClientEmail == "" {
		return nil, errors.New("the key file has no client_email: it is not a service account key")
	}
	if _, err := google.JWTConfigFromJSON(keyJSON, Scopes()...); err != nil {
		return nil, fmt.Errorf("the service account key is not usable: %w", err)
	}
	return &Record{
		Account:        key.ClientEmail,
		Mode:           ModeServiceAccount,
		ServiceAccount: json.RawMessage(keyJSON),
		Impersonate:    impersonate,
	}, nil
}

const consentPage = `<!doctype html><html><head><meta charset="utf-8"><title>gws</title></head>
<body style="font-family:system-ui,sans-serif;padding:3rem">
<h2>gws has the credential.</h2><p>Go back to the terminal. You can close this tab.</p>
</body></html>`

// LoginOAuth runs the desktop loopback flow with PKCE and returns a record
// holding the refresh token. The account name comes from Drive About.
func LoginOAuth(ctx context.Context, clientID, clientSecret string) (*Record, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start the loopback listener: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  fmt.Sprintf("http://127.0.0.1:%d/", port),
		Scopes:       Scopes(),
	}
	verifier := oauth2.GenerateVerifier()
	state, err := randomState()
	if err != nil {
		return nil, err
	}

	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("error") != "":
			res.err = fmt.Errorf("google refused the consent: %s", q.Get("error"))
		case q.Get("state") != state:
			res.err = errors.New("the redirect carries the wrong state value: the flow is rejected")
		case q.Get("code") == "":
			res.err = errors.New("the redirect carries no authorization code")
		default:
			res.code = q.Get("code")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<!doctype html><p>%s</p>", html.EscapeString(res.err.Error()))
		} else {
			fmt.Fprint(w, consentPage)
		}
		select {
		case ch <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	authURL := conf.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.S256ChallengeOption(verifier),
	)
	if err := exec.Command("xdg-open", authURL).Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Open this URL to grant access:\n%s\n", authURL)
	}
	fmt.Fprintf(os.Stderr, "Waiting for the consent redirect on %s ...\n", conf.RedirectURL)

	var res result
	select {
	case res = <-ch:
	case <-ctx.Done():
		return nil, fmt.Errorf("no consent redirect arrived: %w", ctx.Err())
	}
	if res.err != nil {
		return nil, res.err
	}

	tok, err := conf.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("exchange the authorization code: %w", err)
	}
	if tok.RefreshToken == "" {
		return nil, errors.New("google returned no refresh token: remove the app from the account access page and log in again")
	}

	email, err := accountEmail(ctx, conf.TokenSource(ctx, tok))
	if err != nil {
		return nil, err
	}
	return &Record{
		Account:      email,
		Mode:         ModeOAuth,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Token:        tok,
	}, nil
}

// Save writes the record into the OS keyring. It never writes a file.
func Save(r *Record) error {
	if r == nil || r.Account == "" {
		return errors.New("the credential record has no account")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := keyring.Set(r.Account, string(b)); err != nil {
		return fmt.Errorf("OS keyring unavailable, refusing to store the credential in plaintext: %w", err)
	}
	return nil
}

// Load reads the record for one account out of the OS keyring.
func Load(account string) (*Record, error) {
	if account == "" {
		return nil, errors.New("not logged in: " + LoginHint)
	}
	s, err := keyring.Get(account)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, fmt.Errorf("no credential in the OS keyring for %s: %s", account, LoginHint)
		}
		return nil, fmt.Errorf("OS keyring: %w", err)
	}
	var r Record
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, fmt.Errorf("the keyring entry for %s is not a credential record: %w", account, err)
	}
	return &r, nil
}

// Delete removes the keyring entry for one account.
func Delete(account string) error {
	if account == "" {
		return errors.New("no account given")
	}
	return keyring.Delete(account)
}

// TokenSource builds the token source for a record. An OAuth token that
// rotates is written back into the keyring.
func TokenSource(ctx context.Context, r *Record) (oauth2.TokenSource, error) {
	if r == nil {
		return nil, errors.New("no credential record")
	}
	switch r.Mode {
	case ModeServiceAccount:
		cfg, err := google.JWTConfigFromJSON(r.ServiceAccount, Scopes()...)
		if err != nil {
			return nil, fmt.Errorf("the stored service account key is not usable: %w", err)
		}
		cfg.Subject = r.Impersonate
		return cfg.TokenSource(ctx), nil
	case ModeOAuth:
		if r.Token == nil {
			return nil, errors.New("the stored OAuth credential has no token: run `gws auth login --oauth`")
		}
		conf := &oauth2.Config{
			ClientID:     r.ClientID,
			ClientSecret: r.ClientSecret,
			Endpoint:     google.Endpoint,
			Scopes:       Scopes(),
		}
		return &persistSource{rec: r, src: conf.TokenSource(ctx, r.Token), last: r.Token.AccessToken}, nil
	default:
		return nil, fmt.Errorf("unknown auth mode %q: run `gws auth login`", r.Mode)
	}
}

// persistSource writes a rotated OAuth token back into the keyring.
type persistSource struct {
	mu   sync.Mutex
	rec  *Record
	src  oauth2.TokenSource
	last string
}

func (p *persistSource) Token() (*oauth2.Token, error) {
	t, err := p.src.Token()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if t.AccessToken != p.last {
		p.last = t.AccessToken
		p.rec.Token = t
		// the rotation is silent: a keyring write failure must not break the
		// API call that is in flight
		_ = Save(p.rec)
	}
	return t, nil
}

// accountEmail asks Drive who the credential belongs to.
func accountEmail(ctx context.Context, ts oauth2.TokenSource) (string, error) {
	svc, err := drive.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return "", fmt.Errorf("build the Drive client: %w", err)
	}
	about, err := svc.About.Get().Fields("user").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("read the account from Drive: %w", err)
	}
	if about.User == nil || about.User.EmailAddress == "" {
		return "", errors.New("drive did not name the account")
	}
	return about.User.EmailAddress, nil
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("draw a random state value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
