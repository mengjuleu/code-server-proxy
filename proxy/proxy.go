package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

type Proxy struct {
	*mux.Router
	upgrader    websocket.Upgrader
	backendHost string
	pathMap     map[string]int
	initPort    int
}

// NewProxy creates a code-server proxy
func NewProxy() *Proxy {
	p := &Proxy{}
	p.Router = mux.NewRouter()
	p.route()
	p.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
	}
	p.initPort = 9051
	p.backendHost = fmt.Sprintf("localhost:%d", p.initPort)
	p.pathMap = make(map[string]int)
	return p
}

func (p *Proxy) route() {
	p.HandleFunc("/healthcheck", p.healthCheckHandler)

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

	if _, ok := p.pathMap[filePath]; !ok {
		p.pathMap[filePath] = p.initPort
		p.initPort++
	}

	redirectURL := url.URL{Scheme: "https", Host: r.Host}
	http.Redirect(w, r, redirectURL.String()+filePath, http.StatusTemporaryRedirect)
}

func (p *Proxy) websocketHandler(w http.ResponseWriter, r *http.Request) {
	// Don't need to handle path matching
	backendWsURL := url.URL{Scheme: "ws", Host: p.backendHost}
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

	r.RequestURI = p.parseRequestPath(r.RequestURI, filePath)

	backendHTTPURL := url.URL{Scheme: "http", Host: p.backendHost, Path: r.RequestURI}
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

func (p *Proxy) parseRequestPath(requestPath, filePath string) string {
	// TODO: Handle /opt/path
	// TODO: Need to come up with better algorithm or data structure for path matching
	keys := make([]string, len(p.pathMap))
	for k := range p.pathMap {
		keys = append(keys, k)
	}

	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	for _, k := range keys {
		if strings.HasPrefix(requestPath, k) {
			requestPath = strings.TrimPrefix(requestPath, filePath)
			break
		}

		if strings.HasPrefix(requestPath, path.Dir(k)) {
			requestPath = strings.TrimPrefix(requestPath, path.Dir(filePath))
			break
		}
	}

	return requestPath
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
