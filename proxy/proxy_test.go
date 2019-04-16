package proxy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestProxy() (*Proxy, error) {
	code, err := LoadConfig("./test.yaml")
	if err != nil {
		return nil, err
	}

	p, err := NewProxy(
		UseCode(code),
	)

	if err != nil {
		return nil, err
	}

	return p, nil
}

func TestCleanRequestPath(t *testing.T) {
	p, err := newTestProxy()
	require.NoError(t, err)

	expectedPath := "/mleu/cool"

	var cleanedPath string

	paths := []string{
		"/a/b/c/mleu/cool",    // Test prefix with project path
		"/a/b/mleu/cool",      // Test prefix with project parent path
		"/project1/mleu/cool", // Test prefix with project alias
	}

	for _, path := range paths {
		cleanedPath = p.cleanRequestPath(path)
		require.Equal(t, expectedPath, cleanedPath)
	}
}

func TestPathToPort(t *testing.T) {
	p, err := newTestProxy()
	require.NoError(t, err)

	data := []struct {
		port int
		path string
	}{
		{
			9000,
			"/a/b/c/mleu/cool",
		},
		{
			9001,
			"/a/b/f/mleu/cool",
		},
		{
			9002,
			"/a/d/e",
		},
	}

	for _, d := range data {
		require.Equal(t, d.port, p.pathToPort(d.path))
	}
}
