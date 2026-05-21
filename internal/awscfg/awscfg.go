// Package awscfg edits the user's ~/.aws/config file to add
// SSO session and profile sections that match what the awscli expects.
package awscfg

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/ini.v1"
)

// Profile is the minimum set of fields we write under [profile <name>].
type Profile struct {
	Name        string // section name (without the "profile " prefix)
	AccountID   string
	RoleName    string
	Region      string // default region for the profile
	SessionName string // must match the sso-session block
}

// SanitizeProfile normalizes account/role strings into a valid INI section
// name: lowercase, alnum/dot/underscore only, no leading or trailing dashes.
func SanitizeProfile(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`[^a-z0-9._-]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "config"), nil
}

// loadOrEmpty returns the parsed ~/.aws/config, or an empty file when
// it doesn't exist yet.
func loadOrEmpty() (string, *ini.File, error) {
	p, err := configPath()
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", nil, err
	}

	opts := ini.LoadOptions{
		Loose:                    true, // don't error if file is missing
		SpaceBeforeInlineComment: true,
	}
	f, err := ini.LoadSources(opts, p)
	if err != nil {
		return "", nil, fmt.Errorf("loading %s: %w", p, err)
	}
	return p, f, nil
}

// WriteSSOSession upserts the [sso-session <name>] block.
func WriteSSOSession(name, startURL, region string) error {
	if name == "" {
		return fmt.Errorf("sso session name is empty")
	}
	p, f, err := loadOrEmpty()
	if err != nil {
		return err
	}

	// Section returns the existing section or creates it — idempotent.
	sec := f.Section("sso-session " + name)
	sec.Key("sso_start_url").SetValue(startURL)
	sec.Key("sso_region").SetValue(region)
	sec.Key("sso_registration_scopes").SetValue("sso:account:access")
	return f.SaveTo(p)
}

// WriteProfile upserts a [profile <Name>] block tied to the sso-session.
func WriteProfile(p Profile) error {
	if p.Name == "" {
		return fmt.Errorf("profile name is empty")
	}
	path, f, err := loadOrEmpty()
	if err != nil {
		return err
	}

	sec := f.Section("profile " + p.Name)
	sec.Key("sso_session").SetValue(p.SessionName)
	sec.Key("sso_account_id").SetValue(p.AccountID)
	sec.Key("sso_role_name").SetValue(p.RoleName)
	sec.Key("region").SetValue(p.Region)
	sec.Key("output").SetValue("json")
	return f.SaveTo(path)
}
