package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"gopkg.in/urfave/cli.v1"

	"github.com/code-server-proxy/proxy"
	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v2"
)

const (
	readBufferSize  = 4096
	writeBufferSize = 4096
	defaultPort     = 5555
)

func main() {
	var (
		logFormat  string
		bind       string
		configFile string
	)

	app := cli.NewApp()
	app.Version = "Proxy version 1.0"
	app.Usage = "Proxy proxies code-server traffic"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "lf,log-format",
			Destination: &logFormat,
			Usage:       "--log-format=json can only use json or text",
			EnvVar:      "LOG_FORMAT",
			Value:       "json",
		},
		cli.StringFlag{
			Name:        "b, bind",
			Destination: &bind,
			EnvVar:      "BIND",
			Value:       fmt.Sprintf(":%d", defaultPort),
		},
		cli.StringFlag{
			Name:        "c, config",
			Destination: &configFile,
			EnvVar:      "CONFIG",
			Value:       "/opt/go/src/github.com/code-server-proxy/code.yaml",
		},
	}

	app.Action = func(c *cli.Context) error {
		// Config websocket upgrader
		upgrader := websocket.Upgrader{
			ReadBufferSize:  readBufferSize,
			WriteBufferSize: writeBufferSize,
		}

		// Load code-server configs
		code, err := loadConfig(configFile)
		if err != nil {
			return err
		}

		// Create proxy instance
		p, err := proxy.NewProxy(
			proxy.UseUpgrader(upgrader),
			proxy.UseCode(code),
		)
		if err != nil {
			return err
		}

		// Serve HTTP request
		if err = http.ListenAndServe(bind, p); err != nil {
			return err
		}
		return nil
	}

	err := app.Run(os.Args)
	if err != nil {
		os.Exit(1)
	}
}

// loadConfig loads the config file of code-server-proxy
func loadConfig(config string) (proxy.Code, error) {
	code := proxy.Code{}

	data, err := ioutil.ReadFile(config)
	if err != nil {
		return code, err
	}

	if err := yaml.Unmarshal(data, &code); err != nil {
		return code, err
	}
	return code, nil
}
