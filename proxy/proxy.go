package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/golang/protobuf/proto"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"

	"github.com/armon/go-radix"
	"github.com/code-server-proxy/healthproto"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Proxy is a code-server proxy
type Proxy struct {
	*mux.Router
	client      *http.Client
	upgrader    websocket.Upgrader
	code        Code
	portMap     *radix.Tree
	logger      *logrus.Logger
	config      string
	aliasToPath map[string]string
}

// Server represents a code-server
type Server struct {
	Path  string
	Alias string
	Port  int
}

// Code represents the code-server structures
type Code struct {
	Servers []Server
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

// RegisterRequest represents the struct of reload request
type RegisterRequest struct {
	Folder string `json:"folder"`
	Name   string `json:"name"`
	Port   string `json:"port"`
}

// UseConfig sets config path
func UseConfig(config string) func(*Proxy) error {
	return func(p *Proxy) error {
		p.config = config
		return nil
	}
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

	// Setup client
	p.client = &http.Client{}

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
	p.HandleFunc("/", p.healthCheckHandler)
	p.HandleFunc("/status/{name}", p.codeServerStatusHandler).Methods("GET")
	p.HandleFunc("/status", p.statusHandler).Methods("GET")

	p.HandleFunc("/register", p.registerHandler).Methods("POST")
	p.HandleFunc("/remove/{name}", p.removeHandler).Methods("DELETE")

	// The sequence of following two rules can not exchange
	p.HandleFunc("/{filePath:.*}", p.websocketHandler).Headers("Connection", "upgrade")

	p.HandleFunc("/{filePath:.*}", p.forwardRequestHandler)
}

// healthCheckHandler handles healthcheck request
func (p *Proxy) healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	healthcheckResponse := HealthcheckResponse{}

	for _, s := range p.code.Servers {
		state, err := p.checkCodeServerStatus(s.Port)
		if err != nil {
			p.logger.Errorf("Failed to check code-server status: %v", err)
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

func (p *Proxy) statusHandler(w http.ResponseWriter, r *http.Request) {
	healthCheck := healthproto.HealthCheck{}
	healthCheck.CodeServerProxy = "OK"

	for _, s := range p.code.Servers {
		state, err := p.checkCodeServerStatus(s.Port)
		if err != nil {
			p.logger.Errorf("Failed to check code-server status: %v", err)
		}

		backendURL := url.URL{Scheme: "https", Host: r.Host, Path: s.Path}
		aliasURL := url.URL{Scheme: "https", Host: r.Host, Path: s.Alias}
		healthCheck.CodeServers = append(
			healthCheck.CodeServers,
			&healthproto.CodeServerStatus{
				Port:     int64(s.Port),
				State:    state,
				Url:      backendURL.String(),
				Alias:    s.Alias,
				AliasURL: aliasURL.String(),
			},
		)
	}

	b, merr := proto.Marshal(&healthCheck)
	if merr != nil {
		p.logger.Errorf("Failed to marshal healthcheck object: %v", merr)
	}

	if _, werr := w.Write(b); werr != nil {
		http.Error(w, werr.Error(), http.StatusInternalServerError)
	}
}

func (p *Proxy) codeServerStatusHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := fmt.Sprintf("/%s", vars["name"])
	port, ok := p.portMap.Get(name)
	if !ok {
		http.Error(w, fmt.Sprintf("Project %s does not exist", vars["name"]), http.StatusBadRequest)
		return
	}

	state, err := p.checkCodeServerStatus(port.(int))
	if err != nil {
		p.logger.Errorf("Failed to check code-server status: %v", err)
	}

	codeServerStatus := healthproto.CodeServerStatus{
		Port:  int64(port.(int)),
		State: state,
	}

	b, merr := proto.Marshal(&codeServerStatus)
	if merr != nil {
		p.logger.Errorf("Failed to marshal codeServerStatus object: %v", merr)
	}

	if _, werr := w.Write(b); werr != nil {
		http.Error(w, werr.Error(), http.StatusInternalServerError)
	}
}

func (p *Proxy) forwardRequestHandler(w http.ResponseWriter, r *http.Request) {
	host := fmt.Sprintf("localhost:%d", p.getPort(r))
	cleanedPath := p.cleanRequestPath(r.RequestURI)
	backendHTTPURL := url.URL{
		Scheme: "http",
		Host:   host,
		Path:   cleanedPath,
	}

	req, err := http.NewRequest(r.Method, backendHTTPURL.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.Header = r.Header

	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	p.logger.WithFields(logrus.Fields{
		"host":          host,
		"path":          cleanedPath,
		"backend":       backendHTTPURL.String(),
		"response-code": resp.StatusCode,
		"request-url":   resp.Request.URL,
		"referer":       r.Referer(),
	}).Info("Receive forward request")

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

func (p *Proxy) registerHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)

	var data RegisterRequest

	if err := decoder.Decode(&data); err != nil {
		p.logger.Errorf("Failed to decode: %v", err)
		return
	}

	port, err := strconv.Atoi(data.Port)
	if err != nil {
		p.logger.Errorf("Failed to convert port to integer: %v", err)
		return
	}

	// Prevent duplicated alias or port
	for _, server := range p.code.Servers {
		if server.Alias == data.Name {
			http.Error(w, fmt.Sprintf("Name %s is in use", data.Name), http.StatusBadRequest)
			return
		}

		if server.Port == port {
			http.Error(w, fmt.Sprintf("Name %s is in use", data.Name), http.StatusBadRequest)
			return
		}
	}

	// Append the new code-server to existing slice
	p.code.Servers = append(p.code.Servers, Server{
		Path:  data.Folder,
		Alias: data.Name,
		Port:  port,
	})

	// Consolidate the new code object async
	go WriteConfig(p.code, p.config)

	// Store new path and port to radix tree
	p.portMap.Insert(data.Folder, port)
	p.portMap.Insert(path.Dir(data.Folder), port)
	p.portMap.Insert(fmt.Sprintf("/%s", data.Name), port)

	// Store new alias and name to map
	p.aliasToPath[data.Name] = data.Folder

	w.WriteHeader(http.StatusOK)
}

func (p *Proxy) removeHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	if _, ok := p.aliasToPath[name]; !ok {
		http.Error(w, fmt.Sprintf("Code-server %s doesn't exist", name), http.StatusBadRequest)
		return
	}

	for i, server := range p.code.Servers {
		if server.Alias == name {
			p.code.Servers = append(p.code.Servers[:i], p.code.Servers[i+1:]...)
			break
		}
	}

	p.portMap.Delete(fmt.Sprintf("/%s", name))
	delete(p.aliasToPath, name)

	w.WriteHeader(http.StatusNoContent)
}

// cleanRequestPath removes unrelated prefix from request path
func (p *Proxy) cleanRequestPath(requestPath string) string {
	prefix, _, _ := p.portMap.LongestPrefix(requestPath)
	requestPath = strings.TrimPrefix(requestPath, prefix)

	if strings.HasPrefix(requestPath, "/login") {
		requestPath = "/login" + requestPath
	}

	return requestPath
}

// getPort returns the port corresponding to the path.
func (p *Proxy) getPort(r *http.Request) int {
	requestPath := r.RequestURI
	if r.Referer() != "" {
		u, err := url.Parse(r.Referer())
		if err != nil {
			p.logger.Fatalf("Failed to parse referer: %v", err)
		}
		requestPath = u.Path
	}

	port := p.code.Servers[0].Port
	if _, val, ok := p.portMap.LongestPrefix(requestPath); ok {
		port = val.(int)
	}
	return port
}

// checkCodeServerStatus checks status of code-server by port
func (p *Proxy) checkCodeServerStatus(port int) (string, error) {
	state := "NOT OK"
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/ping", port))

	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()

		codeServerPingResponse := CodeServerPingResponse{}
		b, err := ioutil.ReadAll(resp.Body)

		if err != nil {
			return "", err
		}
		if uerr := json.Unmarshal(b, &codeServerPingResponse); uerr != nil {
			return "", uerr
		}

		if codeServerPingResponse.Hostname != "" {
			state = "OK"
		}
	}

	return state, nil
}

// LoadConfig loads the config file of code-server-proxy
func LoadConfig(config string) (Code, error) {
	code := Code{}

	data, err := ioutil.ReadFile(filepath.Clean(config))
	if err != nil {
		return code, err
	}

	if err := yaml.Unmarshal(data, &code); err != nil {
		return code, err
	}
	return code, nil
}

// WriteConfig writes the config to config.yaml
func WriteConfig(c Code, config string) error {
	y, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	if werr := ioutil.WriteFile(filepath.Clean(config), y, 0644); err != nil {
		return werr
	}
	return nil
}
