package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/armon/go-radix"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Proxy is a code-server proxy
type Proxy struct {
	*mux.Router
	upgrader    websocket.Upgrader
	code        Code
	portMap     *radix.Tree
	logger      *logrus.Logger
	aliasToPath map[string]string
}

// Code represents the code-server structures
type Code struct {
	Servers []struct {
		Path  string
		Alias string
		Port  int
	}
}

// CodeServerStatus represents the health status of a code-server
type CodeServerStatus struct {
	Port     int
	State    string
	URL      string
	Alias    string
	AliasURL string
}

// HealthcheckResponse is the response structure of code-server-proxy
type HealthcheckResponse struct {
	CodeServerProxy string
	CodeServers     []CodeServerStatus
}

// CodeServerPingResponse represents the response of ping request
type CodeServerPingResponse struct {
	Hostname string `json:"hostname"`
}

// UseLogger sets proxy's logger
func UseLogger(logger *logrus.Logger) func(*Proxy) error {
	return func(p *Proxy) error {
		p.logger = logger
		return nil
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

	// Construct radix tree
	p.portMap = radix.New()
	for _, s := range p.code.Servers {
		p.portMap.Insert(s.Path, s.Port)
		p.portMap.Insert(path.Dir(s.Path), s.Port)
		p.portMap.Insert(fmt.Sprintf("/%s", s.Alias), s.Port)
	}

	// Create path to its alias mapping
	p.aliasToPath = make(map[string]string)
	for _, s := range p.code.Servers {
		p.aliasToPath[s.Alias] = s.Path
	}

	p.Router = mux.NewRouter()
	p.route()

	return p, nil
}

func (p *Proxy) route() {
	p.HandleFunc("/healthcheck", p.healthCheckHandler)

	// The sequence of following two rules can not exchange
	p.HandleFunc("/{filePath:.*}", p.websocketHandler).Headers("Connection", "upgrade")

	p.HandleFunc("/{filePath:.*}", p.forwardRequestHandler)
}

func (p *Proxy) websocketHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filePath := vars["filePath"]

	if path, ok := p.aliasToPath[filePath]; !ok {
		filePath = fmt.Sprintf("/%s", filePath)
	} else {
		filePath = path
	}

	port, _ := p.portMap.Get(filePath)
	backendWsURL := url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("localhost:%d", port),
	}

	header := http.Header{
		"Cookie": []string{r.Header.Get("Cookie")},
	}

	p.logger.WithFields(logrus.Fields{
		"filePath": filePath,
		"backend":  backendWsURL.String(),
	}).Info("Receive websocket connection request")

	// websocket connection to backend
	back, _, err := websocket.DefaultDialer.Dial(backendWsURL.String(), header)
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
	go p.transfer(front, back, b2f)

	// goroutine that transfers messages from frontend tp backend
	go p.transfer(back, front, f2b)

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
	healthcheckResponse := HealthcheckResponse{}

	for _, s := range p.code.Servers {
		state := "NOT OK"
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/ping", s.Port))
		if err == nil {
			defer resp.Body.Close()

			codeServerPingResponse := CodeServerPingResponse{}
			b, err := ioutil.ReadAll(resp.Body)

			if err != nil {
				p.logger.Fatalf("Failed to read ping request: %v", err)
			}
			if uerr := json.Unmarshal(b, &codeServerPingResponse); uerr != nil {
				p.logger.Fatalf("Failed to unmarshal response body: %v", uerr)
			}

			if codeServerPingResponse.Hostname != "" {
				state = "OK"
			}
		}

		backendURL := url.URL{Scheme: "https", Host: r.Host, Path: s.Path}
		aliasURL := url.URL{Scheme: "https", Host: r.Host, Path: s.Alias}
		healthcheckResponse.CodeServers = append(
			healthcheckResponse.CodeServers,
			CodeServerStatus{
				Port:     s.Port,
				State:    state,
				URL:      backendURL.String(),
				Alias:    s.Alias,
				AliasURL: aliasURL.String(),
			},
		)
	}

	healthcheckResponse.CodeServerProxy = "OK"

	w.Header().Set("Content-Type", "application/json")

	b, err := json.Marshal(healthcheckResponse)
	if err != nil {
		p.logger.Fatalf("Failed to marshal healthCheckResponse: %v", err)
	}

	if _, werr := w.Write(b); werr != nil {
		http.Error(w, werr.Error(), http.StatusInternalServerError)
	}
}

func (p *Proxy) forwardRequestHandler(w http.ResponseWriter, r *http.Request) {
	host := fmt.Sprintf("localhost:%d", p.pathToPort(r.RequestURI))
	cleanedPath := p.cleanRequestPath(r.RequestURI)
	backendHTTPURL := url.URL{
		Scheme: "http",
		Host:   host,
		Path:   cleanedPath,
	}

	p.logger.WithFields(logrus.Fields{
		"host":    host,
		"path":    cleanedPath,
		"backend": backendHTTPURL.String(),
	}).Info("Receive forward request")

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
func (p *Proxy) transfer(dst, src *websocket.Conn, ch chan error) {
	for {
		if terr := tunnel(dst, src); terr != nil {
			p.logger.Info(terr.Error())
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
