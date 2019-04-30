package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

func (p *Proxy) websocketHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filePath := vars["filePath"]

	if path, ok := p.aliasToPath[filePath]; !ok {
		filePath = fmt.Sprintf("/%s", filePath)
	} else {
		filePath = path
	}

	filePath = strings.TrimSuffix(filePath, "/")

	port, _ := p.portMap.Get(filePath)
	backendWsURL := url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("localhost:%d", port),
	}

	cookies := []string{}
	for _, cookie := range r.Cookies() {
		cookies = append(cookies, cookie.String())
	}

	header := http.Header{
		"Cookie": cookies,
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
