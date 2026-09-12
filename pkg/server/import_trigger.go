// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
)

type importTrigger struct {
	client  *run.JobsClient
	jobName string
	wg      sync.WaitGroup
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

// TriggerRepoImportAsync schedules a trigger in the background and tracks it
// via the trigger's WaitGroup so callers can drain in-flight work at shutdown.
// The provided ctx is detached so server cancellation does not abort an
// already-scheduled import; a separate timeout bounds the call.
func (t *importTrigger) TriggerRepoImportAsync(org, repo string, timeout time.Duration) {
	if t == nil {
		return
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := t.TriggerRepoImport(ctx, org, repo); err != nil {
			slog.Error("triggering on-demand import", "org", org, "repo", repo, "error", err)
		}
	}()
}

// Wait blocks until all async triggers spawned via TriggerRepoImportAsync have
// completed. Safe to call on a nil receiver.
func (t *importTrigger) Wait() {
	if t == nil {
		return
	}
	t.wg.Wait()
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
