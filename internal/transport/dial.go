package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"

	"github.com/jvitoroc/tincan/internal/proto"
	"github.com/quic-go/quic-go"
)

// Trust records why the client accepted a server's certificate.
type Trust int

const (
	// TrustNew: nothing was pinned for this address, so the certificate was
	// accepted on first use. The caller should pin Peer.Fingerprint.
	TrustNew Trust = iota
	// TrustPinned: the certificate matches the pinned fingerprint.
	TrustPinned
	// TrustCA: the certificate is valid for the hostname under the system's
	// certificate authorities (e.g. Let's Encrypt), so no pin is needed.
	TrustCA
)

// Peer describes the server at the other end of a dialed connection.
type Peer struct {
	Fingerprint string
	Trust       Trust
}

// MismatchError means the server presented a certificate other than the pinned
// one: either the server's data directory was reset, or someone is intercepting
// the connection.
type MismatchError struct {
	Addr      string
	Pinned    string
	Presented string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("certificate of %s changed: pinned %s, presented %s", e.Addr, e.Pinned, e.Presented)
}

// Dial opens a QUIC connection to addr. pin is the fingerprint previously pinned
// for addr, or "" if this is the first contact.
func Dial(ctx context.Context, addr, pin string) (*quic.Conn, Peer, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, Peer{}, err
	}

	var peer Peer
	var mismatch *MismatchError
	tlsConf := &tls.Config{
		// Standard verification rejects self-signed certificates, which is what
		// most tincan servers use; VerifyConnection does the checking instead.
		InsecureSkipVerify: true,
		ServerName:         host,
		NextProtos:         []string{proto.ALPN},
		MinVersion:         tls.VersionTLS13,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("server presented no certificate")
			}
			peer.Fingerprint = proto.Fingerprint(cs.PeerCertificates[0].Raw)
			switch {
			case validUnderSystemCAs(cs.PeerCertificates, host):
				peer.Trust = TrustCA
			case pin == "":
				peer.Trust = TrustNew
			case peer.Fingerprint == pin:
				peer.Trust = TrustPinned
			default:
				mismatch = &MismatchError{Addr: addr, Pinned: pin, Presented: peer.Fingerprint}
				return mismatch
			}
			return nil
		},
	}

	conn, err := quic.DialAddr(ctx, addr, tlsConf, QUICConfig())
	if err != nil {
		// quic-go reports a failed verification as a generic TLS alert, so hand
		// back our own error instead.
		if mismatch != nil {
			return nil, Peer{}, mismatch
		}
		return nil, Peer{}, err
	}
	return conn, peer, nil
}

func validUnderSystemCAs(chain []*x509.Certificate, host string) bool {
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{DNSName: host, Intermediates: intermediates})
	return err == nil
}
