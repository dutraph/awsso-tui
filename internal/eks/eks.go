// Package eks lists EKS clusters via the AWS SDK and delegates the
// kubeconfig merge to the official `aws eks update-kubeconfig` command,
// which knows how to produce the right exec-auth user block.
package eks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"
)

// ListClusters returns the names of all EKS clusters visible to
// the given profile in the given region, sorted alphabetically.
func ListClusters(ctx context.Context, profile, region string) ([]string, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile(profile),
		awsconfig.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("aws config (profile %s): %w", profile, err)
	}
	client := eks.NewFromConfig(cfg)

	var names []string
	var next *string
	for {
		resp, err := client.ListClusters(ctx, &eks.ListClustersInput{
			MaxResults: aws.Int32(100),
			NextToken:  next,
		})
		if err != nil {
			return nil, fmt.Errorf("eks ListClusters: %w", err)
		}
		names = append(names, resp.Clusters...)
		if resp.NextToken == nil {
			break
		}
		next = resp.NextToken
	}
	sort.Strings(names)
	return names, nil
}

// UpdateKubeconfig shells out to `aws eks update-kubeconfig` so the
// resulting kubeconfig entry uses the canonical exec-auth format
// (calls `aws eks get-token` at request time) and is correctly merged
// into the user's existing ~/.kube/config.
func UpdateKubeconfig(ctx context.Context, profile, region, cluster string) error {
	if _, err := exec.LookPath("aws"); err != nil {
		return fmt.Errorf("`aws` CLI not found on PATH (needed for update-kubeconfig and exec auth): %w", err)
	}
	cmd := exec.CommandContext(ctx, "aws",
		"--profile", profile,
		"--region", region,
		"eks", "update-kubeconfig",
		"--name", cluster,
		"--alias", cluster,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
