package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/golang/protobuf/proto"

	"github.com/code-server-proxy/healthproto"

	"github.com/pkg/browser"
	cli "gopkg.in/urfave/cli.v1"
)

const (
	extensionsKind = "extensions"
	settingsKind   = "settings"
)

const (
	defaultProxyURL        = ""
	defaultHost            = ""
	vsCodeConfigDirEnv     = "VSCODE_CONFIG_DIR"
	vsCodeExtensionsDirEnv = "VSCODE_EXTENSIONS_DIR"
	remoteSettingsDir      = ".local/share/code-server/User/"
	remoteExtensionsDir    = ".local/share/code-server/extensions/"
)

// Chrome binaries
const (
	googleChrome       = "google-chrome"
	googleChromeStable = "google-chrome-stable"
	chromium           = "chromium"
	chromiumBrowser    = "chromium-browser"
	chromeMacOs        = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
)

// Chrome settings directory
const (
	vscodeLinuxSettings  = "$HOME/.config/Code/User/"
	vscodeDarwinSettings = "$HOME/Library/Application Support/Code/User/"
)

const (
	vscodeExtensions = "$HOME/.vscode/extensions/"
)

var (
	proxyURL   string
	remoteHost string
)

func main() {
	app := cli.NewApp()
	app.Version = "Proxy CLI version 1.0"
	app.Usage = "csp-cli is tool interacting with code-server-proxy"

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "proxy-url",
			Destination: &proxyURL,
			Usage:       "--proxy-url=url of code-server-proxy",
			Value:       defaultProxyURL,
			EnvVar:      "PROXY_URL",
		},
		cli.StringFlag{
			Name:        "remote-host",
			Destination: &remoteHost,
			Usage:       "--remote-host=host of dev environment",
			Value:       defaultHost,
			EnvVar:      "REMOTE_HOST",
		},
	}

	app.Commands = []cli.Command{
		{
			Name:    "list",
			Aliases: []string{"ls"},
			Usage:   "list available code-server projects",
			Action:  listCmdHandler,
		},
		{
			Name:    "sync",
			Aliases: []string{"sc"},
			Usage:   "Sync local vscode settings",
			Action:  syncCmdHandler,
		},
		{
			Name:   "open",
			Usage:  "Open a code-server project via URL",
			Action: openCmdHandler,
		},
	}

	app.Action = func(c *cli.Context) error {
		projectName := c.Args().Get(0)
		if projectName == "" {
			return errors.New("Project name is required")
		}

		projectURL := fmt.Sprintf("%s/%s", proxyURL, projectName)
		return openBrowser(projectURL)
	}

	if rerr := app.Run(os.Args); rerr != nil {
		log.Fatal(rerr)
	}
}

// openBrowser opens broswer in app mode with specified url
func openBrowser(url string) error {
	chromeOptions := []string{
		fmt.Sprintf("--app=%s", url),
		"--disable-extensions",
		"--disable-plugins",
		"--incognito",
	}

	var openCmd *exec.Cmd
	/* #nosec */
	switch {
	case commandExists(chromeMacOs):
		openCmd = exec.Command(chromeMacOs, chromeOptions...)
	case commandExists(googleChromeStable):
		openCmd = exec.Command(googleChromeStable, chromeOptions...)
	case commandExists(googleChrome):
		openCmd = exec.Command(googleChrome, chromeOptions...)
	case commandExists(chromium):
		openCmd = exec.Command(chromium, chromeOptions...)
	case commandExists(chromiumBrowser):
		openCmd = exec.Command(chromiumBrowser, chromeOptions...)
	default:
		if err := browser.OpenURL(url); err != nil {
			return err
		}
	}

	if err := openCmd.Start(); err != nil {
		return err
	}
	return nil
}

// commandExists checks if a command called name exists locally.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func openCmdHandler(c *cli.Context) error {
	codeServerURL := c.Args().Get(0)

	p, err := url.ParseRequestURI(codeServerURL)
	if err != nil {
		return err
	}

	if err = openBrowser(p.String()); err != nil {
		return err
	}

	return nil
}

// listCmdHandler handles "csp-cli ls" command which lists all remote projects and their statuses
func listCmdHandler(c *cli.Context) error {
	statusAPI := fmt.Sprintf("%s/status", proxyURL)
	resp, err := http.Get(statusAPI) // #nosec
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	data, rerr := ioutil.ReadAll(resp.Body)
	if rerr != nil {
		return err
	}

	healthCheck := healthproto.HealthCheck{}
	if uerr := proto.Unmarshal(data, &healthCheck); uerr != nil {
		return uerr
	}

	for _, server := range healthCheck.CodeServers {
		fmt.Printf("%-20s %s\n", server.Alias, server.State)
	}

	return nil
}

// syncCmdHandler syncs local vscode configuration with remote box
func syncCmdHandler(c *cli.Context) error {
	syncChan := make(chan error)

	start := time.Now()
	for _, config := range []string{settingsKind, extensionsKind} {
		go func(config string) {
			fmt.Printf("Start syncing %s\n", config)
			syncChan <- syncUser(remoteHost, config)
			fmt.Printf("Sync user %s in: %s\n", config, time.Since(start))
		}(config)
	}

	for i := 0; i < 2; i++ {
		if err := <-syncChan; err != nil {
			return err
		}
	}

	return nil
}

// syncUser syncs remote host with vscode configuration by kind
func syncUser(host, kind string) error {
	var localConfigDir string
	var remoteConfigDir string
	var err error

	switch kind {
	case settingsKind:
		localConfigDir, err = settingsDir()
		remoteConfigDir = remoteSettingsDir
	case extensionsKind:
		localConfigDir, err = extensionsDir()
		remoteConfigDir = remoteExtensionsDir
	default:
		return fmt.Errorf("Unrecognized config kind: %s", kind)
	}

	if err != nil {
		return err
	}

	src := fmt.Sprintf("%s/", localConfigDir)
	dst := fmt.Sprintf("%s:%s", host, remoteConfigDir)
	return rsync(dst, src)
}

// settingsDir returns vscode settings directory path
func settingsDir() (string, error) {
	if env, ok := os.LookupEnv(vsCodeConfigDirEnv); ok {
		return os.ExpandEnv(env), nil
	}

	var path string
	switch runtime.GOOS {
	case "linux":
		path = os.ExpandEnv(vscodeLinuxSettings)
	case "darwin":
		path = os.ExpandEnv(vscodeDarwinSettings)
	default:
		return "", fmt.Errorf("Unsupported platform: %s", runtime.GOOS)
	}
	return filepath.Clean(path), nil
}

// extensionsDir returns vscode extensions directory path
func extensionsDir() (string, error) {
	if env, ok := os.LookupEnv(vsCodeExtensionsDirEnv); ok {
		return os.ExpandEnv(env), nil
	}

	var path string
	switch runtime.GOOS {
	case "linux", "darwin":
		path = os.ExpandEnv(vscodeExtensions)
	default:
		return "", fmt.Errorf("Unsupported platform: %s", runtime.GOOS)
	}
	return filepath.Clean(path), nil
}

// rsync syncs drc and remote directories with excluding paths
func rsync(dst, src string, excludePaths ...string) error {
	excludeFlags := make([]string, len(excludePaths))
	for i, path := range excludePaths {
		excludeFlags[i] = fmt.Sprintf("--exclude=%s", path)
	}

	/* #nosec */
	cmd := exec.Command("rsync", append(excludeFlags, "-azvr",
		"-e", "ssh",
		"-u", "--times",
		"--copy-unsafe-links",
		src, dst,
	)...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Failed to rsync '%s' to '%s': %v", src, dst, err)
	}

	return nil
}
