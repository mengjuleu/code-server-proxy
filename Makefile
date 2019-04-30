CMD=github.com/code-server-proxy/cmd/code-server-proxy
CLICMD=github.com/code-server-proxy/cmd/csp-cli

all: test test-slow lint

test:
	go test -race -v ./...

test-slow:
	go test -tags=slow -race -v ./...

lint: .gotlint
	gometalinter --fast \
	--enable gofmt \
	--disable gotype \
	--disable gocyclo \
	--exclude="file permissions" --exclude="Errors unhandled" \
	./...

setup: .gotlint

install:
	go install $(CMD)

install-cli:
	go install $(CLICMD)

.gotlint:
	go get -u github.com/alecthomas/gometalinter
	gometalinter --install
	touch $@

