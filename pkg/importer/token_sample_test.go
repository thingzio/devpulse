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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPurgeThreshold(t *testing.T) {
	threshold := purgeThreshold()
	expected := time.Now().UTC().AddDate(0, 0, -quotaSampleRetentionDays)
	assert.WithinDuration(t, expected, threshold, 2*time.Second)
}

func TestSampleTokenQuotas_NilConfig(t *testing.T) {
	// Should be a no-op when ghAppConfig is nil — no panic, no error.
	sampleTokenQuotas(context.Background(), nil, nil)
}
