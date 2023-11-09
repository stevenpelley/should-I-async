package hello

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHello(t *testing.T) {
	require.Equal(t, Message(), "hello world")

}
