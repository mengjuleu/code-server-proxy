package main

import (
	"net/http"

	"github.com/code-server-proxy/proxy"
)

func main() {
	p := proxy.NewProxy()
	http.ListenAndServe("127.0.0.1:5555", p)
}
