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

package data

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContains(t *testing.T) {
	assert.True(t, Contains([]string{"a", "b", "c"}, "b"))
	assert.False(t, Contains([]string{"a", "b"}, "d"))
	assert.False(t, Contains[string](nil, "a"))
}

func TestContains_Integers(t *testing.T) {
	assert.True(t, Contains([]int{1, 2, 3}, 2))
	assert.False(t, Contains([]int{1, 2, 3}, 4))
}

func TestContains_EmptySlice(t *testing.T) {
	assert.False(t, Contains([]string{}, "a"))
	assert.False(t, Contains([]int{}, 0))
}

func TestContains_SingleElement(t *testing.T) {
	assert.True(t, Contains([]string{"only"}, "only"))
	assert.False(t, Contains([]string{"only"}, "other"))
}

func TestErrDBNotInitialized(t *testing.T) {
	assert.EqualError(t, ErrDBNotInitialized, "database not initialized")
}
