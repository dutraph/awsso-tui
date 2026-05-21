// Package ssoauth wraps the AWS SSO OIDC device-authorization flow and
// the SSO portal APIs needed to list accounts and roles. The access token
// is cached under ~/.aws/sso/cache using the same filename hashing scheme
// the official aws CLI uses, so the cache is shared with `aws sso login`.
package ssoauth

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sso"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	ssooidctypes "github.com/aws/aws-sdk-go-v2/service/ssooidc/types"
	"github.com/pkg/browser"
)

// Account is a flattened view of the SSO portal account listing.
type Account struct {
	ID    string
	Name  string
	Email string
}

// Client is bound to a single SSO start URL + region.
type Client struct {
	oidc        *ssooidc.Client
	sso         *sso.Client
	startURL    string
	region      string
	sessionName string
	cachePath   string
}

// New constructs a Client. It loads AWS SDK config with the given region
// so SSO API endpoints resolve correctly even on a host with no profile.
func New(ctx context.Context, startURL, region, sessionName string) (*Client, error) {
	if startURL == "" {
		return nil, errors.New("ssoauth: empty start URL")
	}
	if region == "" {
		return nil, errors.New("ssoauth: empty region")
	}
	if sessionName == "" {
		sessionName = "awsso"
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cacheFile := filepath.Join(home, ".aws", "sso", "cache", cacheFilename(sessionName))

	return &Client{
		oidc:        ssooidc.NewFromConfig(cfg),
		sso:         sso.NewFromConfig(cfg),
		startURL:    startURL,
		region:      region,
		sessionName: sessionName,
		cachePath:   cacheFile,
	}, nil
}

// cacheFilename mirrors the awscli v2 convention: sha1(session_name).json.
func cacheFilename(sessionName string) string {
	h := sha1.Sum([]byte(sessionName))
	return hex.EncodeToString(h[:]) + ".json"
}

// cacheEntry matches the JSON layout written by `aws sso login`.
type cacheEntry struct {
	StartURL              string    `json:"startUrl"`
	Region                string    `json:"region"`
	AccessToken           string    `json:"accessToken"`
	ExpiresAt             time.Time `json:"expiresAt"`
	ClientID              string    `json:"clientId,omitempty"`
	ClientSecret          string    `json:"clientSecret,omitempty"`
	RegistrationExpiresAt time.Time `json:"registrationExpiresAt,omitempty"`
	RefreshToken          string    `json:"refreshToken,omitempty"`
}

// Login returns a valid access token, reusing the cache when possible.
// When the cache is expired or missing it runs the device-authorization
// flow, opens the verification URL in the browser, and polls for the token.
func (c *Client) Login(ctx context.Context) (string, error) {
	// Cache hit?
	if entry, ok := c.readCache(); ok {
		// Give ourselves a 60s safety margin.
		if entry.AccessToken != "" && time.Until(entry.ExpiresAt) > time.Minute {
			return entry.AccessToken, nil
		}
	}

	// Otherwise: register a public client and start the device flow.
	clientID, clientSecret, err := c.ensureRegisteredClient(ctx)
	if err != nil {
		return "", fmt.Errorf("register client: %w", err)
	}

	da, err := c.oidc.StartDeviceAuthorization(ctx, &ssooidc.StartDeviceAuthorizationInput{
		ClientId:     aws.String(clientID),
		ClientSecret: aws.String(clientSecret),
		StartUrl:     aws.String(c.startURL),
	})
	if err != nil {
		return "", fmt.Errorf("start device authorization: %w", err)
	}

	verify := aws.ToString(da.VerificationUriComplete)
	if verify == "" {
		verify = aws.ToString(da.VerificationUri)
	}
	fmt.Println("opening browser for SSO confirmation:")
	fmt.Println("  ", verify)
	fmt.Println("user code:", aws.ToString(da.UserCode))
	_ = browser.OpenURL(verify) // ignore browser-open errors, the user still has the URL

	interval := time.Duration(da.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expiresIn := time.Duration(da.ExpiresIn) * time.Second
	deadline := time.Now().Add(expiresIn)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return "", errors.New("device authorization timed out")
		}
		tok, err := c.oidc.CreateToken(ctx, &ssooidc.CreateTokenInput{
			ClientId:     aws.String(clientID),
			ClientSecret: aws.String(clientSecret),
			DeviceCode:   da.DeviceCode,
			GrantType:    aws.String("urn:ietf:params:oauth:grant-type:device_code"),
		})
		if err != nil {
			var pending *ssooidctypes.AuthorizationPendingException
			if errors.As(err, &pending) {
				continue
			}
			var slow *ssooidctypes.SlowDownException
			if errors.As(err, &slow) {
				interval += 5 * time.Second
				continue
			}
			return "", fmt.Errorf("create token: %w", err)
		}

		accessToken := aws.ToString(tok.AccessToken)
		expiresAt := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)

		c.writeCache(cacheEntry{
			StartURL:              c.startURL,
			Region:                c.region,
			AccessToken:           accessToken,
			ExpiresAt:             expiresAt,
			ClientID:              clientID,
			ClientSecret:          clientSecret,
			RegistrationExpiresAt: time.Now().Add(90 * 24 * time.Hour),
			RefreshToken:          aws.ToString(tok.RefreshToken),
		})
		return accessToken, nil
	}
}

// ensureRegisteredClient returns the OIDC client ID/secret, reusing
// whatever is in the cache if it hasn't expired yet.
func (c *Client) ensureRegisteredClient(ctx context.Context) (string, string, error) {
	if entry, ok := c.readCache(); ok {
		if entry.ClientID != "" && entry.ClientSecret != "" &&
			(entry.RegistrationExpiresAt.IsZero() || time.Until(entry.RegistrationExpiresAt) > time.Hour) {
			return entry.ClientID, entry.ClientSecret, nil
		}
	}
	out, err := c.oidc.RegisterClient(ctx, &ssooidc.RegisterClientInput{
		ClientName: aws.String("awsso-tui"),
		ClientType: aws.String("public"),
		Scopes:     []string{"sso:account:access"},
	})
	if err != nil {
		return "", "", err
	}
	return aws.ToString(out.ClientId), aws.ToString(out.ClientSecret), nil
}

func (c *Client) readCache() (cacheEntry, bool) {
	b, err := os.ReadFile(c.cachePath)
	if err != nil {
		return cacheEntry{}, false
	}
	var e cacheEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return cacheEntry{}, false
	}
	return e, true
}

func (c *Client) writeCache(e cacheEntry) {
	if err := os.MkdirAll(filepath.Dir(c.cachePath), 0o700); err != nil {
		return
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(c.cachePath, b, 0o600)
}

// ListAccounts walks the SSO portal pages and returns all accounts
// the access token is allowed to see, sorted by name.
func (c *Client) ListAccounts(ctx context.Context, token string) ([]Account, error) {
	var out []Account
	var next *string
	for {
		resp, err := c.sso.ListAccounts(ctx, &sso.ListAccountsInput{
			AccessToken: aws.String(token),
			MaxResults:  aws.Int32(100),
			NextToken:   next,
		})
		if err != nil {
			return nil, err
		}
		for _, a := range resp.AccountList {
			out = append(out, Account{
				ID:    aws.ToString(a.AccountId),
				Name:  aws.ToString(a.AccountName),
				Email: aws.ToString(a.EmailAddress),
			})
		}
		if resp.NextToken == nil {
			break
		}
		next = resp.NextToken
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListAccountRoles returns the role names assigned to the SSO user for
// the given account, sorted alphabetically.
func (c *Client) ListAccountRoles(ctx context.Context, token, accountID string) ([]string, error) {
	var out []string
	var next *string
	for {
		resp, err := c.sso.ListAccountRoles(ctx, &sso.ListAccountRolesInput{
			AccessToken: aws.String(token),
			AccountId:   aws.String(accountID),
			MaxResults:  aws.Int32(100),
			NextToken:   next,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range resp.RoleList {
			out = append(out, aws.ToString(r.RoleName))
		}
		if resp.NextToken == nil {
			break
		}
		next = resp.NextToken
	}
	sort.Strings(out)
	return out, nil
}
