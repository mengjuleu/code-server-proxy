package main

import (
	"io/ioutil"
	"log"
	"net/http"

	"github.com/code-server-proxy/proxy"
	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v2"
)

func main() {
	// Config websocket upgrader
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
	}

	code, err := loadConfig("/opt/go/src/github.com/code-server-proxy/code.yaml")
	if err != nil {
		log.Fatalln(err)
	}

	p, err := proxy.NewProxy(
		proxy.UseUpgrader(upgrader),
		proxy.UseCode(code),
	)
	if err != nil {
		log.Fatalln(err)
	}

	http.ListenAndServe("127.0.0.1:5555", p)
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
