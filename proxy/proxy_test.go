package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type RegisterRequest struct {
	Folder string `json:"folder"`
	Name   string `json:"name"`
	Port   string `json:"port"`
}

func newTestProxy() (*Proxy, error) {
	code, err := LoadConfig("./test.yaml")
	logger := logrus.New()
	if err != nil {
		return nil, err
	}

	p, err := NewProxy(
		UseCode(code),
		UseLogger(logger),
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
		r    *http.Request
	}{
		{
			9000,
			&http.Request{
				RequestURI: "/a/b/c/mleu/cool",
			},
		},
		{
			9001,
			&http.Request{
				RequestURI: "/a/b/f/mleu/cool",
			},
		},
		{
			9002,
			&http.Request{
				RequestURI: "/a/d/e",
			},
		},
		{
			9000,
			&http.Request{
				RequestURI: "/main.css",
				Header: http.Header{
					"Referer": []string{"http://localhost/project1"},
				},
			},
		},
	}

	for _, d := range data {
		require.Equal(t, d.port, p.getPort(d.r))
	}
}

func TestHealthCheckHandler(t *testing.T) {
	p, err := newTestProxy()
	require.NoError(t, err)

	req, err := http.NewRequest("GET", "/healthcheck", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(p.HealthCheckHandler)

	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "incorrect response code")

	resp := HealthcheckResponse{}
	uerr := json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, uerr)

	require.Equal(t, "OK", resp.CodeServerProxy, "incorrect code-server-proxy status")

	for _, status := range resp.CodeServers {
		require.Equal(t, "NOT OK", status.State, "incorrect code-server status")
	}
}

func TestRegisterHandler(t *testing.T) {
	p, err := newTestProxy()
	require.NoError(t, err)

	reqBody := RegisterRequest{
		Folder: "/k/m/n",
		Name:   "coolproj",
		Port:   "1999",
	}

	b, err := json.Marshal(reqBody)
	require.NoError(t, err)

	reader := bytes.NewReader(b)
	req, nerr := http.NewRequest("POST", "/register", reader)
	require.NoError(t, nerr)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(p.registerHandler)

	handler.ServeHTTP(rr, req)
	require.Equal(t, rr.Code, http.StatusOK, "incorrect response code")

	var ok bool
	_, ok = p.portMap.Get(fmt.Sprintf("/%s", reqBody.Name))
	require.True(t, ok, "alias not found in radix tree")

	_, ok = p.portMap.Get(reqBody.Folder)
	require.True(t, ok, "path not found in radix tree")

	_, ok = p.portMap.Get(path.Dir(reqBody.Folder))
	require.True(t, ok, "parent path not found in radix tree")
}
