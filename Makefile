CMD=github.com/code-server-proxy/cmd/code-server-proxy

all: test test-slow lint

test: .gotdeps
	go test -race -v ./...

test-slow: .gotdeps
	go test -tags=slow -race -v ./...

lint: .gotlint
	gometalinter --fast --vendor \
	--enable gofmt \
	--disable gotype \
	--disable gocyclo \
	--exclude="file permissions" --exclude="Errors unhandled" \
	./...

setup: .gotglide .gotlint

install: .gotdeps
	go install $(CMD)

.gotlint:
	go get -u github.com/alecthomas/gometalinter
	gometalinter --install
	touch $@

.gotglide:
	go get github.com/Masterminds/glide
	touch $@

.gotdeps: .gotglide glide.lock
	glide install
	touch $@