package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

type Proxy struct {
	*mux.Router
	upgrader    websocket.Upgrader
	backendHost string
	pathMap     map[string]bool
}

func NewProxy() *Proxy {
	p := &Proxy{}
	p.Router = mux.NewRouter()
	p.Route()
	p.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
	}
	p.backendHost = "localhost:9051"
	p.pathMap = make(map[string]bool)
	return p
}

func (p *Proxy) Route() {
	p.HandleFunc("/healthcheck", p.healthCheckHandler)

	// The sequence of following two rules can not exchange
	p.HandleFunc("/", p.websocketHandler).Headers("Connection", "upgrade")
	p.HandleFunc("/{filePath:.*}", p.codeServerHandler)
}

func (p *Proxy) codeServerHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filePath := vars["filePath"]
	filePath = fmt.Sprintf("/%s", filePath)

	if strings.HasPrefix(filePath, "/path") {
		rURL := url.URL{Scheme: "https", Host: r.Host}
		realPath := strings.TrimPrefix(filePath, "/path")
		if _, err := os.Stat(realPath); os.IsNotExist(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
		} else {
			http.Redirect(w, r, rURL.String(), http.StatusTemporaryRedirect)
		}
		return
	}

	if ferr := p.forwardRequest(w, r); ferr != nil {
		http.Error(w, ferr.Error(), http.StatusInternalServerError)
	}
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

func (p *Proxy) forwardRequest(w http.ResponseWriter, r *http.Request) error {
	backendHttpURL := url.URL{Scheme: "http", Host: p.backendHost, Path: r.RequestURI}
	req, err := http.NewRequest(r.Method, backendHttpURL.String(), nil)
	if err != nil {
		return err
	}

	req.Header = r.Header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	for h, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(h, v)
		}
	}

	if _, cerr := io.Copy(w, resp.Body); cerr != nil {
		return cerr
	}
	return nil
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
