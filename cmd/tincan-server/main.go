// Command tincan-server hosts tincan for a group of friends.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jvitoroc/tincan/internal/proto"
	"github.com/jvitoroc/tincan/internal/server/hub"
	"github.com/jvitoroc/tincan/internal/transport"
	"github.com/jvitoroc/tincan/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tincan-server:", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", ":"+proto.DefaultPort, "UDP `address` to listen on")
	data := flag.String("data", "tincan-data", "`directory` for the server's state (its certificate, for now)")
	certPath := flag.String("cert", "", "PEM certificate `file` to use instead of the self-signed one (e.g. from Let's Encrypt)")
	keyPath := flag.String("key", "", "PEM private key `file` for -cert")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "%s\n\nUsage: tincan-server [flags]\n\n", version.String("tincan-server"))
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String("tincan-server"))
		return nil
	}
	if (*certPath == "") != (*keyPath == "") {
		return errors.New("-cert and -key must be used together")
	}

	var cert tls.Certificate
	var err error
	if *certPath != "" {
		cert, err = tls.LoadX509KeyPair(*certPath, *keyPath)
	} else {
		cert, err = transport.LoadOrCreateCert(*data)
	}
	if err != nil {
		return err
	}

	ln, err := transport.Listen(*listen, cert)
	if err != nil {
		return err
	}
	defer ln.Close()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("tincan-server starting", "version", version.Version(), "protocol", version.Protocol)
	// Friends see this fingerprint the first time they connect, so they can
	// compare it with what you tell them.
	log.Info("listening", "addr", ln.Addr().String(), "fingerprint", transport.Fingerprint(cert))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = hub.New(log).Serve(ctx, ln)
	log.Info("stopped")
	return err
}
