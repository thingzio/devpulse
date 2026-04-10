package server

import (
	"context"
	"fmt"
	"log/slog"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
)

type importTrigger struct {
	client  *run.JobsClient
	jobName string
}

func newImportTrigger(ctx context.Context, jobName string) (*importTrigger, error) {
	if jobName == "" {
		return nil, nil
	}
	client, err := run.NewJobsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating Cloud Run jobs client: %w", err)
	}
	return &importTrigger{client: client, jobName: jobName}, nil
}

// TriggerRepoImport triggers an on-demand import job for a single repo.
// Safe to call on a nil receiver (returns nil when trigger is disabled).
func (t *importTrigger) TriggerRepoImport(ctx context.Context, org, repo string) error {
	if t == nil || t.client == nil {
		return nil
	}

	slog.Info("triggering on-demand import", "org", org, "repo", repo)

	op, err := t.client.RunJob(ctx, &runpb.RunJobRequest{
		Name: t.jobName,
		Overrides: &runpb.RunJobRequest_Overrides{
			TaskCount: 1,
			ContainerOverrides: []*runpb.RunJobRequest_Overrides_ContainerOverride{
				{
					Env: []*runpb.EnvVar{
						{Name: "IMPORT_ORG", Values: &runpb.EnvVar_Value{Value: org}},
						{Name: "IMPORT_REPO", Values: &runpb.EnvVar_Value{Value: repo}},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("triggering import for %s/%s: %w", org, repo, err)
	}

	slog.Info("on-demand import triggered", "org", org, "repo", repo, "operation", op.Name())
	return nil
}

// Close releases the underlying gRPC client.
func (t *importTrigger) Close() error {
	if t == nil || t.client == nil {
		return nil
	}
	return t.client.Close()
}
