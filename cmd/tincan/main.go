// Command tincan is the tincan client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/jvitoroc/tincan/internal/client/config"
	"github.com/jvitoroc/tincan/internal/client/conn"
	"github.com/jvitoroc/tincan/internal/proto"
	"github.com/jvitoroc/tincan/internal/transport"
	"github.com/jvitoroc/tincan/internal/version"
	"github.com/quic-go/quic-go"
)

const dialTimeout = 10 * time.Second

func main() {
	os.Exit(run(os.Args[1:]))
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `tincan: a tin-can telephone for your terminal

Usage:
  tincan ping [-c count] <host[:port]>   check a server is reachable and measure latency
  tincan forget <host[:port]>            forget a server's pinned certificate
  tincan version                          print the version

The port defaults to %s. The chat UI arrives in the next release.
`, proto.DefaultPort)
}

func run(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "ping":
		return cmdPing(args[1:])
	case "forget":
		return cmdForget(args[1:])
	case "version", "-version", "--version", "-v":
		fmt.Println(version.String("tincan"))
		return 0
	case "help", "-h", "-help", "--help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "tincan: unknown command %q\n\n", args[0])
		usage(os.Stderr)
		return 2
	}
}

func loadConfig() (*config.Config, error) {
	dir, err := config.DefaultDir()
	if err != nil {
		return nil, err
	}
	return config.Load(dir)
}

func cmdPing(args []string) int {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	count := fs.Int("c", 4, "number of pings")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *count < 1 {
		fmt.Fprintln(os.Stderr, "usage: tincan ping [-c count] <host[:port]>")
		return 2
	}
	addr := proto.NormalizeAddr(fs.Arg(0))

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tincan:", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	c, err := conn.Dial(dialCtx, addr, cfg.Fingerprint(addr))
	cancel()
	if err != nil {
		reportDialError(addr, err)
		return 1
	}
	defer c.Close()

	peer := c.Peer()
	switch peer.Trust {
	case transport.TrustNew:
		cfg.PinFingerprint(addr, peer.Fingerprint)
		if err := cfg.Save(); err != nil {
			fmt.Fprintln(os.Stderr, "tincan: couldn't save the pinned certificate:", err)
		}
		fmt.Printf("New server. Pinned its certificate (trust on first use):\n  %s\n", peer.Fingerprint)
	case transport.TrustPinned:
		fmt.Printf("Certificate matches the pinned one: %s\n", peer.Fingerprint)
	case transport.TrustCA:
		fmt.Printf("Certificate is signed by a trusted authority: %s\n", peer.Fingerprint)
	}
	fmt.Printf("Connected to %s, running tincan-server %s\n", addr, c.ServerVersion())

	var rtts []time.Duration
	for i := range *count {
		if i > 0 {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
			}
		}
		if ctx.Err() != nil {
			break
		}
		rtt, err := c.Ping(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			fmt.Fprintf(os.Stderr, "ping %d failed: %v\n", i+1, err)
			return 1
		}
		rtts = append(rtts, rtt)
		fmt.Printf("ping %d: %s\n", i+1, ms(rtt))
	}
	if len(rtts) > 0 {
		lo, hi, sum := rtts[0], rtts[0], time.Duration(0)
		for _, d := range rtts {
			lo, hi, sum = min(lo, d), max(hi, d), sum+d
		}
		noun := "pings"
		if len(rtts) == 1 {
			noun = "ping"
		}
		fmt.Printf("%d %s: min %s, avg %s, max %s\n", len(rtts), noun, ms(lo), ms(sum/time.Duration(len(rtts))), ms(hi))
	}
	return 0
}

func ms(d time.Duration) string {
	return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000)
}

func reportDialError(addr string, err error) {
	var mismatch *transport.MismatchError
	var perr *proto.Error
	var idle *quic.IdleTimeoutError
	switch {
	case errors.As(err, &mismatch):
		fmt.Fprintf(os.Stderr, `WARNING: the certificate of %s has changed!
  pinned:    %s
  presented: %s
Either the server's data directory was reset, or someone is intercepting your
connection. Check with whoever runs the server. If the change is expected,
forget the old certificate and connect again:
  tincan forget %s
`, addr, mismatch.Pinned, mismatch.Presented, addr)
	case errors.As(err, &perr) && perr.Code == proto.CodeIncompatibleProtocol:
		fmt.Fprintf(os.Stderr, `tincan: %s
If your client is the older one, update it with:
  %s
Otherwise, ask whoever runs the server to update it.
`, perr.Message, version.UpdateHint("tincan"))
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &idle):
		fmt.Fprintf(os.Stderr, "tincan: no response from %s. Is the server running, and is UDP port %s open?\n", addr, portOf(addr))
	default:
		fmt.Fprintf(os.Stderr, "tincan: couldn't connect to %s: %v\n", addr, err)
	}
}

func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return proto.DefaultPort
}

func cmdForget(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tincan forget <host[:port]>")
		return 2
	}
	addr := proto.NormalizeAddr(args[0])
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tincan:", err)
		return 1
	}
	if !cfg.Forget(addr) {
		fmt.Printf("Nothing saved for %s.\n", addr)
		return 0
	}
	if err := cfg.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "tincan:", err)
		return 1
	}
	fmt.Printf("Forgot %s. Its certificate will be pinned again on the next connection.\n", addr)
	return 0
}
