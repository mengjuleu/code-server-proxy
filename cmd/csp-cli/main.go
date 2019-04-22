package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"

	cli "gopkg.in/urfave/cli.v1"
)

const (
	defaultProxyURL = "https://ide.mleumonster.devbucket.org"
)

func main() {
	var (
		proxyURL string
	)

	app := cli.NewApp()
	app.Version = "Proxy CLI version 1.0"
	app.Usage = "csp-cli <project_name>"

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "proxy-url",
			Destination: &proxyURL,
			Usage:       "--proxy-url=url of code-server-proxy",
			Value:       defaultProxyURL,
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

func openBrowser(url string) error {
	var openCmd *exec.Cmd
	/* #nosec */
	switch {
	case commandExists("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"):
		openCmd = exec.Command("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			chromeOptions(url)...)
	}

	if err := openCmd.Start(); err != nil {
		return err
	}
	return nil
}

func chromeOptions(url string) []string {
	return []string{
		fmt.Sprintf("--app=%s", url),
		"--disable-extensions",
		"--disable-plugins",
		"--incognito",
	}
}

// commandExists checks if a command exists locally.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
