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
