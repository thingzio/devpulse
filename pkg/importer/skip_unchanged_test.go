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

package importer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestShouldSkipUnchangedRepo(t *testing.T) {
	tests := []struct {
		name         string
		pushedAt     time.Time
		maxEventTime time.Time
		hasForkData  bool
		wantSkip     bool
	}{
		{
			name:         "no events yet, never skip",
			pushedAt:     time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			maxEventTime: time.Time{},
			hasForkData:  true,
			wantSkip:     false,
		},
		{
			name:         "no pushed_at, never skip",
			pushedAt:     time.Time{},
			maxEventTime: time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			hasForkData:  true,
			wantSkip:     false,
		},
		{
			name:         "pushed_at before max event, skip",
			pushedAt:     time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC),
			maxEventTime: time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			hasForkData:  true,
			wantSkip:     true,
		},
		{
			name:         "pushed_at equal to max event, skip",
			pushedAt:     time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			maxEventTime: time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			hasForkData:  true,
			wantSkip:     true,
		},
		{
			name:         "pushed_at after max event, do not skip",
			pushedAt:     time.Date(2025, 3, 25, 0, 0, 0, 0, time.UTC),
			maxEventTime: time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			hasForkData:  true,
			wantSkip:     false,
		},
		{
			name:         "fork data missing, never skip even if pushed_at would otherwise trigger",
			pushedAt:     time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC),
			maxEventTime: time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC),
			hasForkData:  false,
			wantSkip:     false,
		},
		{
			name:         "fork data missing, ai-dynamo/dynamo-style trap is unblocked",
			pushedAt:     time.Date(2026, 5, 3, 23, 53, 1, 0, time.UTC),
			maxEventTime: time.Date(2026, 5, 4, 1, 41, 35, 0, time.UTC),
			hasForkData:  false,
			wantSkip:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipUnchangedRepo(tt.pushedAt, tt.maxEventTime, tt.hasForkData)
			assert.Equal(t, tt.wantSkip, got)
		})
	}
}
