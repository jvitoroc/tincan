# tincan

A tin-can telephone for your terminal: a small Discord for you and your friends, with servers, text channels, voice, and webcams drawn in ASCII.

> **Status: early.** This release only has the foundation: a server and a `tincan ping` command that checks your connection to it. Chat, voice and video come next (see [plan.md](plan.md)).

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/jvitoroc/tincan/main/install.sh | sh
```

This installs `tincan` to `~/.local/bin`. Run the same command again to update. If you have Go, `go install github.com/jvitoroc/tincan/cmd/tincan@latest` works too.

## Host a server

```sh
curl -fsSL https://raw.githubusercontent.com/jvitoroc/tincan/main/install.sh | sh -s -- --server
tincan-server -listen :7777 -data ~/.tincan-server
```

Open **UDP** port 7777 in your firewall or router. On first run the server creates a certificate in its data directory and logs its fingerprint. Keep that directory: if it's lost, every friend gets a "certificate changed" warning.

## Check a connection

```sh
tincan ping your.server.com
```

The first connection pins the server's certificate fingerprint, the way SSH remembers hosts. Compare it with the one the server logged. If the certificate later changes, tincan refuses to connect and explains what to do (`tincan forget <server>` once you've confirmed the change is expected).

## Development

```sh
make test         # go test -race ./...
make run-server   # server on :7777 with data in ./tincan-data
make ping         # ping it
```

Set `TINCAN_CONFIG_DIR` to give each client its own config directory when running several on one machine.
