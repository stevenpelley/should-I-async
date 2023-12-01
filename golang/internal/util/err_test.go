package util

import (
	"errors"
	"testing"

	goerrors "github.com/go-errors/errors"
	"github.com/stretchr/testify/require"
)

// Test wrapping behavior of go-errors/errors

// wrapping *goerrors.Error returns the input instead of wrapping
func TestWrap(t *testing.T) {
	require := require.New(t)
	e1 := errors.New("without ST")
	e2 := goerrors.New("with ST")

	e1w := goerrors.Wrap(e1, 0)
	require.NotEqual(e1, e1w)
	require.True(errors.Is(e1w, e1))

	e2w := goerrors.Wrap(e2, 0)
	require.Equal(e2, e2w)

	e2f := goerrors.Errorf("force wrap: %w", e2)
	require.NotEqual(e2, e2f)
	require.True(errors.Is(e2f, e2))
}

// wrapping with a prefix always works
func TestWrapPrefix(t *testing.T) {
	require := require.New(t)
	e1 := errors.New("without ST")
	e2 := goerrors.WrapPrefix(e1, "prefix1", 0)
	e3 := goerrors.WrapPrefix(e2, "prefix2", 0)
	// works as expected!
	require.Equal("prefix2: prefix1: without ST", e3.Error())
}
