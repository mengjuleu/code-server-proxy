package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/sirupsen/logrus"

	"gopkg.in/urfave/cli.v1"

	"github.com/code-server-proxy/proxy"
	"github.com/gorilla/websocket"
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
			Value:       "text",
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
		code, err := proxy.LoadConfig(configFile)
		if err != nil {
			logrus.Fatalf("Failed to load config: %v", err)
		}

		// Configure logger
		logger, err := configureLogger(logFormat)
		if err != nil {
			logrus.Fatalf("Failed to configure logger: %v", err)
		}

		// Create proxy instance
		p, err := proxy.NewProxy(
			proxy.UseUpgrader(upgrader),
			proxy.UseCode(code),
			proxy.UseLogger(logger),
		)
		if err != nil {
			logrus.Fatalf("Failed to create proxy: %v", err)
		}

		// Serve HTTP request
		logger.Infof("code-server-proxy - running on '%s', pid: %d",
			bind,
			os.Getpid(),
		)

		if err = http.ListenAndServe(bind, p); err != nil {
			logrus.Fatalf("Failed to serve: %v", err)
		}

		return nil
	}

	err := app.Run(os.Args)
	if err != nil {
		os.Exit(1)
	}
}

func configureLogger(format string) (*logrus.Logger, error) {
	logger := logrus.New()
	logger.Level = logrus.InfoLevel

	switch format {
	case "json":
		logger.Formatter = &logrus.JSONFormatter{FieldMap: logrus.FieldMap{logrus.FieldKeyMsg: "message"}}
	case "text":
		logger.Formatter = &logrus.TextFormatter{}
	default:
		return nil, errors.New("Invalid log format value")
	}

	return logger, nil
}
