package clierror

import "errors"

var (
	// ErrMissingProjectName is missing project
	ErrMissingProjectName = errors.New("Project name is required")
	// ErrMissingRemoteHost means REMOTE_HOST doesn't exist
	ErrMissingRemoteHost = errors.New("Please set $REMOTE_HOST environment variable")
	// ErrMissingProxyURL means PROXY_URL doesn't exist
	ErrMissingProxyURL = errors.New("Please set $PROXY_URL environment variable")
)
