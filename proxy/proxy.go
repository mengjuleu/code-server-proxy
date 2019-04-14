package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/armon/go-radix"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Proxy is a code-server proxy
type Proxy struct {
	*mux.Router
	upgrader websocket.Upgrader
	code     Code
	portMap  *radix.Tree
}

// Code represents the code-server structures
type Code struct {
	Servers []struct {
		Path string
		Port int
	}
}

// UseUpgrader sets the websocket HTTP upgrader
func UseUpgrader(upgrader websocket.Upgrader) func(*Proxy) error {
	return func(p *Proxy) error {
		p.upgrader = upgrader
		return nil
	}
}

// UseCode sets the code-server configs
func UseCode(code Code) func(*Proxy) error {
	return func(p *Proxy) error {
		p.code = code
		return nil
	}
}

// NewProxy creates a code-server proxy
func NewProxy(options ...func(*Proxy) error) (*Proxy, error) {
	p := &Proxy{}
	for _, f := range options {
		if err := f(p); err != nil {
			return nil, err
		}
	}

	p.portMap = radix.New()
	for _, s := range p.code.Servers {
		p.portMap.Insert(s.Path, s.Port)
		p.portMap.Insert(path.Dir(s.Path), s.Port)
	}

	p.Router = mux.NewRouter()
	p.route()

	return p, nil
}

func (p *Proxy) route() {
	p.HandleFunc("/healthcheck", p.healthCheckHandler)

	p.HandleFunc("/code-servers", p.listHandler)

	// The sequence of following two rules can not exchange
	p.HandleFunc("/{filePath:.*}", p.websocketHandler).Headers("Connection", "upgrade")

	// "/path/opt/go" : Open /opt/go folder, redirect to /
	// "/path/opt/nonexist" : 400 Bad Request
	p.HandleFunc("/path/{filePath:.*}", p.codeServerHandler)

	// 1. Specify path, redirect to here
	// 2. Don't specify path, redirect to 9051 (default)
	p.HandleFunc("/{filePath:.*}", p.forwardRequestHandler)
}

func (p *Proxy) codeServerHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filePath := vars["filePath"]
	filePath = fmt.Sprintf("/%s", filePath)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if _, ok := p.portMap.Get(filePath); !ok {
		errMessage := fmt.Sprintf("File %s is not registered", filePath)
		http.Error(w, errMessage, http.StatusBadRequest)
		return
	}

	redirectURL := url.URL{Scheme: "https", Host: r.Host}
	http.Redirect(w, r, redirectURL.String()+filePath, http.StatusTemporaryRedirect)
}

func (p *Proxy) websocketHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filePath := vars["filePath"]
	filePath = fmt.Sprintf("/%s", filePath)
	port, _ := p.portMap.Get(filePath)
	backendHost := fmt.Sprintf("localhost:%d", port)

	// Don't need to handle path matching
	backendWsURL := url.URL{Scheme: "ws", Host: backendHost}
	back, _, err := websocket.DefaultDialer.Dial(backendWsURL.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer back.Close()

	// websocket connection to frontend
	front, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer front.Close()

	f2b := make(chan error)
	b2f := make(chan error)

	// goroutine that transfers messages from backend to frontend
	go transfer(front, back, b2f)

	// goroutine that transfers messages from frontend tp backend
	go transfer(back, front, f2b)

	// If either direction fails, finish current websocket session
	select {
	case <-f2b:
		return
	case <-b2f:
		return
	}
}

// healthCheckHandler handles healthcheck request
func (p *Proxy) healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	if _, err := io.WriteString(w, "OK"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (p *Proxy) forwardRequestHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filePath := vars["filePath"]
	filePath = fmt.Sprintf("/%s", filePath)

	port := p.pathToPort(r.RequestURI)
	backendHost := fmt.Sprintf("localhost:%d", port)

	r.RequestURI = p.cleanRequestPath(r.RequestURI)
	backendHTTPURL := url.URL{Scheme: "http", Host: backendHost, Path: r.RequestURI}

	req, err := http.NewRequest(r.Method, backendHTTPURL.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.Header = r.Header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for h, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(h, v)
		}
	}

	if _, cerr := io.Copy(w, resp.Body); cerr != nil {
		http.Error(w, cerr.Error(), http.StatusInternalServerError)
		return
	}
}

func (p *Proxy) listHandler(w http.ResponseWriter, r *http.Request) {
	b, err := json.Marshal(p.code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func (p *Proxy) cleanRequestPath(requestPath string) string {
	prefix, _, _ := p.portMap.LongestPrefix(requestPath)
	requestPath = strings.TrimPrefix(requestPath, prefix)

	return requestPath
}

// pathToPort returns the port corresponding to the path.
func (p *Proxy) pathToPort(requestPath string) int {
	port := p.code.Servers[0].Port

	if _, val, ok := p.portMap.LongestPrefix(requestPath); ok {
		port = val.(int)
	}
	return port
}

// transfer populates message from src to dst
func transfer(dst, src *websocket.Conn, ch chan error) {
	for {
		if terr := tunnel(dst, src); terr != nil {
			fmt.Println(terr)
			ch <- terr
		}
	}
}

// tunnel reads from src websocket connection and sends to dst websocket connection.
func tunnel(dst, src *websocket.Conn) error {
	mt, r, err := src.NextReader()
	if err != nil {
		return err
	}
	w, err := dst.NextWriter(mt)
	if err != nil {
		return err
	}
	defer w.Close()

	if _, cerr := io.Copy(w, r); cerr != nil {
		return cerr
	}
	return nil
}
