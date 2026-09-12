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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewImportTrigger_EmptyJobName(t *testing.T) {
	trigger, err := newImportTrigger(context.Background(), "")
	assert.NoError(t, err)
	assert.Nil(t, trigger)
}

func TestImportTrigger_NilSafe(t *testing.T) {
	var trigger *importTrigger
	err := trigger.TriggerRepoImport(context.Background(), "org", "repo")
	assert.NoError(t, err)
}

func TestImportTrigger_Close_NilSafe(t *testing.T) {
	var trigger *importTrigger
	err := trigger.Close()
	assert.NoError(t, err)
}

func TestImportTrigger_AsyncAndWait_NilSafe(t *testing.T) {
	var trigger *importTrigger
	// Should be a no-op and not panic.
	trigger.TriggerRepoImportAsync("org", "repo", time.Second)
	trigger.Wait()
}
