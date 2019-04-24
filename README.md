# code-server-proxy
[![Build Status](https://travis-ci.org/mengjuleu/code-server-proxy.svg?branch=master)](https://travis-ci.org/mengjuleu/code-server-proxy)

Code-server-proxy is a proxy for code-server, a service that allows us to open project with vscode in remote box.

## Background

Code-server is a fantastic project. To start a project and open in browser in client side. We have to:
  1. Start a code-server process which listens to certain port.
  2. Register the port in the configuration file of proxy (e.g., Nginx, Haproxy).

Obviously, the above pattern has some drawbacks:
  1. We can only open one project at a time. To open a new project, we have to stop the current one.
  2. If we want to open multiple projects simultaneously, we have to change proxy's configurations frequently.
  

To solve these problems, we need a single endpoint which receives traffic and proxies to corresponding code-server process.

## Usage

### Step 1. Edit code.yaml Configure File

`code-server1` and `code-server2` are the running code-server instances. `path` is the specified path of code-server and `port` is the port used by code-server.

```yaml
servers:
  - path: /a/b/c
    port: 8888
  - path: /d/e/f
    port: 9999
```

### Step 2. Configure Nginx to Allow Traffic to Code-server-proxy

TBD

### Step 3. Install Code-server-proxy

Clone code-server-proxy repo and install: 

```bash
git clone https://github.com/mengjuleu/code-server-proxy.git $GOPATH/src/github.com/code-server-proxy
cd $GOPATH/src/github.com/code-server-proxy
make install
```

Make sure you have `$GOPATH/bin` and `code-server-proxy` set up in `.bashrc` or `.zshrc`:

```bash
export PATH="$PATH:$GOPATH/bin"
export REMOTE_HOST="example@example.org"
export PROXY_URL="https://your-code-server-proxy-url"
```

Run help command of code-server-proxy

```bash
$ code-server-proxy -h
NAME:
   code-server-proxy - Proxy proxies code-server traffic

USAGE:
   code-server-proxy [global options] command [command options] [arguments...]

VERSION:
   Proxy version 1.0

COMMANDS:
     help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --lf value, --log-format value  --log-format=json can only use json or text (default: "json") [$LOG_FORMAT]
   -b value, --bind value          (default: ":5555") [$BIND]
   -c value, --config value        (default: "/opt/go/src/github.com/code-server-proxy/code.yaml") [$CONFIG]
   --help, -h                      show help
   --version, -v                   print the version
```

### Step 4. Start Code-server-proxy

```bash
code-server-proxy \
--log-format=json \
--bind=9999 \
--config=/path/to/code.yaml
```

### Step 5. Open Browser

Go to `https://<your host name>/path/{path to your project}`.

For example, `https://mleu.coolman.com/path/opt/go/src/github.com/code-server-proxy`,
where *https://mleuu.coolman.com* is my domain name, *path* is requiired and */opt/go/src/github.com/code-server-proxy* is the project path.

## CSP-CLI

CSP-CLI (Code-Server-Proxy CLI) is a client of code-server-proxy.

```bash
> csp-cli -h
NAME:
   csp-cli - csp-cli is tool interacting with code-server-proxy

USAGE:
   csp-cli [global options] command [command options] [arguments...]

VERSION:
   Proxy CLI version 1.0

COMMANDS:
     list, ls  list available code-server projects
     sync, sc  Sync local vscode settings
     help, h   Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --proxy-url value    --proxy-url=url of code-server-proxy (default: "https://ide.mleumonster.devbucket.org") [$PROXY_URL]
   --remote-host value  --remote-host=host of dev environment (default: "mleumonster@mleumonster.dev.devbucket.org") [$REMOTE_HOST]
   --help, -h           show help
   --version, -v        print the version
```



