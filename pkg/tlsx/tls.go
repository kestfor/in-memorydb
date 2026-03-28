package tlsx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// verifyCAOnly returns a VerifyPeerCertificate function that checks the peer's
// certificate chain against the provided CA pool but skips hostname validation.
// Used for internal cluster communication where nodes connect by IP.
func verifyCAOnly(caPool *x509.CertPool) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("tlsx: no peer certificate presented")
		}
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("tlsx: parse peer certificate: %w", err)
		}
		intermediates := x509.NewCertPool()
		for _, raw := range rawCerts[1:] {
			c, err := x509.ParseCertificate(raw)
			if err != nil {
				return fmt.Errorf("tlsx: parse intermediate certificate: %w", err)
			}
			intermediates.AddCert(c)
		}
		_, err = cert.Verify(x509.VerifyOptions{
			Roots:         caPool,
			Intermediates: intermediates,
		})
		if err != nil {
			return fmt.Errorf("tlsx: certificate verification failed: %w", err)
		}
		return nil
	}
}

// LoadServerCredentials loads mTLS credentials for a gRPC server.
// The server presents its own cert/key and requires the connecting client
// to present a certificate signed by the trusted CA.
func LoadServerCredentials(caCertFile, certFile, keyFile string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("tlsx: load server key pair: %w", err)
	}

	caPEM, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, fmt.Errorf("tlsx: read CA cert: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("tlsx: failed to parse CA cert from %s", caCertFile)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   tls.VersionTLS13,
	}
	return credentials.NewTLS(tlsCfg), nil
}

// LoadClientCredentials loads mTLS credentials for a gRPC client.
// The client presents its own cert/key and verifies the server certificate
// against the trusted CA.
func LoadClientCredentials(caCertFile, certFile, keyFile string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("tlsx: load client key pair: %w", err)
	}

	caPEM, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, fmt.Errorf("tlsx: read CA cert: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("tlsx: failed to parse CA cert from %s", caCertFile)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		// InsecureSkipVerify disables the built-in hostname check so nodes can
		// connect to peers by IP address. CA-chain verification is still enforced
		// via VerifyPeerCertificate, so the mutual-TLS trust model is preserved.
		InsecureSkipVerify:    true, //nolint:gosec
		VerifyPeerCertificate: verifyCAOnly(caPool),
		MinVersion:            tls.VersionTLS13,
	}
	return credentials.NewTLS(tlsCfg), nil
}
