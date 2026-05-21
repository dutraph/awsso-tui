// awsso is a small TUI to pick an AWS SSO account/role, persist a profile
// in ~/.aws/config, and then pick an EKS cluster and update ~/.kube/config.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dutraph/awsso-tui/internal/awscfg"
	"github.com/dutraph/awsso-tui/internal/config"
	"github.com/dutraph/awsso-tui/internal/eks"
	"github.com/dutraph/awsso-tui/internal/ssoauth"
	"github.com/dutraph/awsso-tui/internal/tui"
)

// Set at build time with -ldflags "-X main.version=..."
var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}

	var err error
	switch cmd {
	case "configure":
		err = runConfigure(args[1:])
	case "login", "":
		err = runLogin(ctx)
	case "version", "--version", "-v":
		fmt.Println("awsso", version)
	case "help", "--help", "-h":
		printHelp()
	default:
		printHelp()
		os.Exit(2)
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`awsso - AWS SSO + EKS context picker (TUI)

usage:
  awsso configure [--start-url URL] [--sso-region R] [--eks-region R] [--session NAME]
  awsso              run the interactive picker (alias: awsso login)
  awsso version
  awsso help

flow:
  1. awsso configure          (one time)
  2. awsso                    pick account -> role -> EKS cluster

first time setup:
  awsso configure \
    --start-url https://<d-xxxxxx>.awsapps.com/start \
    --sso-region eu-central-1 \
    --eks-region eu-south-2
`)
}

func runConfigure(args []string) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	startURL := fs.String("start-url", "", "SSO start URL (https://<id>.awsapps.com/start)")
	ssoRegion := fs.String("sso-region", "", "SSO region (e.g. eu-central-1)")
	eksRegion := fs.String("eks-region", "", "Default EKS region (e.g. eu-south-2)")
	session := fs.String("session", "", "SSO session name in ~/.aws/config (default: awsso)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	existing, _ := config.Load()
	if existing == nil {
		existing = &config.Config{}
	}

	if *startURL != "" {
		existing.StartURL = *startURL
	}
	if *ssoRegion != "" {
		existing.SSORegion = *ssoRegion
	}
	if *eksRegion != "" {
		existing.EKSRegion = *eksRegion
	}
	if *session != "" {
		existing.SessionName = *session
	}

	// Prompt interactively for anything still missing.
	existing.StartURL = promptIfEmpty("SSO start URL", existing.StartURL, "")
	existing.SSORegion = promptIfEmpty("SSO region", existing.SSORegion, "eu-central-1")
	existing.EKSRegion = promptIfEmpty("EKS region", existing.EKSRegion, "eu-south-2")
	existing.SessionName = promptIfEmpty("SSO session name", existing.SessionName, "awsso")

	if existing.StartURL == "" {
		return errors.New("SSO start URL is required (e.g. https://<id>.awsapps.com/start)")
	}

	// Trim trailing slash and the "#/" suffix from the start URL.
	existing.StartURL = strings.TrimSuffix(existing.StartURL, "#/")
	existing.StartURL = strings.TrimSuffix(existing.StartURL, "/")

	if err := config.Save(existing); err != nil {
		return err
	}

	// Mirror the session config into ~/.aws/config so the aws CLI works too.
	if err := awscfg.WriteSSOSession(existing.SessionName, existing.StartURL, existing.SSORegion); err != nil {
		return fmt.Errorf("writing sso-session to ~/.aws/config: %w", err)
	}

	path, _ := config.Path()
	fmt.Println("saved configuration to", path)
	return nil
}

func promptIfEmpty(label, current, suggestion string) string {
	if current != "" {
		return current
	}
	if suggestion != "" {
		fmt.Printf("%s [%s]: ", label, suggestion)
	} else {
		fmt.Printf("%s: ", label)
	}
	var v string
	_, _ = fmt.Scanln(&v)
	v = strings.TrimSpace(v)
	if v == "" {
		return suggestion
	}
	return v
}

func runLogin(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "not configured. Run:\n")
		fmt.Fprintln(os.Stderr, "  awsso configure \\")
		fmt.Fprintln(os.Stderr, "    --start-url https://<d-xxxxxx>.awsapps.com/start \\")
		fmt.Fprintln(os.Stderr, "    --sso-region eu-central-1 \\")
		fmt.Fprintln(os.Stderr, "    --eks-region eu-south-2")
		os.Exit(1)
	}

	// 1. SSO device flow (with token cache reuse).
	auth, err := ssoauth.New(ctx, cfg.StartURL, cfg.SSORegion, cfg.SessionName)
	if err != nil {
		return err
	}
	token, err := auth.Login(ctx)
	if err != nil {
		return err
	}

	// 2. List accounts and let the user pick.
	accounts, err := auth.ListAccounts(ctx, token)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return errors.New("no accounts available for this SSO user")
	}
	account, ok, err := tui.PickAccount(accounts)
	if err != nil {
		return err
	}
	if !ok {
		return nil // user quit
	}

	// 3. List roles for the chosen account.
	roles, err := auth.ListAccountRoles(ctx, token, account.ID)
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		return fmt.Errorf("no roles available in account %s", account.Name)
	}
	role, ok, err := tui.PickRole(account, roles)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	// 4. Persist profile in ~/.aws/config.
	profileName := awscfg.SanitizeProfile(account.Name + "-" + role)
	if err := awscfg.WriteProfile(awscfg.Profile{
		Name:        profileName,
		AccountID:   account.ID,
		RoleName:    role,
		Region:      cfg.EKSRegion,
		SessionName: cfg.SessionName,
	}); err != nil {
		return fmt.Errorf("writing profile to ~/.aws/config: %w", err)
	}
	fmt.Printf("saved profile %q in ~/.aws/config\n", profileName)

	// 5. List EKS clusters in eks region using the new profile.
	clusters, err := eks.ListClusters(ctx, profileName, cfg.EKSRegion)
	if err != nil {
		return fmt.Errorf("listing eks clusters: %w", err)
	}
	if len(clusters) == 0 {
		fmt.Printf("no EKS clusters found in %s for profile %s\n", cfg.EKSRegion, profileName)
		fmt.Printf("you can still use this profile: export AWS_PROFILE=%s\n", profileName)
		return nil
	}
	cluster, ok, err := tui.PickCluster(profileName, cfg.EKSRegion, clusters)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	// 6. Update kubeconfig for the chosen cluster.
	if err := eks.UpdateKubeconfig(ctx, profileName, cfg.EKSRegion, cluster); err != nil {
		return fmt.Errorf("update-kubeconfig: %w", err)
	}
	fmt.Printf("kubeconfig updated for cluster %s (profile %s, region %s)\n",
		cluster, profileName, cfg.EKSRegion)
	return nil
}
