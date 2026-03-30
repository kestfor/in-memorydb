package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "lume-ca",
		Short: "Certificate authority tool for Lume cluster",
		Long: `lume-ca manages TLS certificates for a Lume cluster.

Run this tool on the trusted host to create a CA and issue
node certificates. Each node in the cluster uses its certificate
to authenticate itself to other nodes (mutual TLS).

Workflow:
  1. lume-ca init-ca --out-dir ./certs
  2. lume-ca issue --name node1 --ca-cert ./certs/ca.crt --ca-key ./certs/ca.key --out-dir ./certs/node1
  3. lume-ca issue --name node2 --ca-cert ./certs/ca.crt --ca-key ./certs/ca.key --out-dir ./certs/node2`,
	}

	rootCmd.AddCommand(initCACmd())
	rootCmd.AddCommand(issueCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func initCACmd() *cobra.Command {
	var outDir string
	var validityYears int

	cmd := &cobra.Command{
		Use:   "init-ca",
		Short: "Generate a new Certificate Authority key and self-signed certificate",
		Long: `Generates a CA private key (ca.key) and a self-signed CA certificate (ca.crt).

The CA certificate is used to sign node certificates. Keep ca.key secret —
it only needs to be present when issuing new node certificates.

Output files:
  <out-dir>/ca.key  — CA private key (ECDSA P-256, keep secret)
  <out-dir>/ca.crt  — CA self-signed certificate (distribute to all nodes)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInitCA(outDir, validityYears)
		},
	}

	cmd.Flags().StringVar(&outDir, "out-dir", "./certs", "Directory to write ca.key and ca.crt")
	cmd.Flags().IntVar(&validityYears, "validity", 10, "CA certificate validity in years")

	return cmd
}

func issueCmd() *cobra.Command {
	var nodeName string
	var caCertFile string
	var caKeyFile string
	var outDir string
	var validityYears int
	var hosts []string

	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Issue a TLS certificate for a node, signed by the CA",
		Long: `Generates a node private key and a certificate signed by the CA.

Each node in the cluster needs its own certificate. The node presents
this certificate when connecting to other nodes (mutual TLS).

Output files:
  <out-dir>/<name>.key  — Node private key (ECDSA P-256)
  <out-dir>/<name>.crt  — Node certificate signed by CA`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIssue(nodeName, caCertFile, caKeyFile, outDir, validityYears, hosts)
		},
	}

	cmd.Flags().StringVar(&nodeName, "name", "", "Node name (used as CommonName and as output file prefix)")
	cmd.Flags().StringVar(&caCertFile, "ca-cert", "./certs/ca.crt", "Path to CA certificate file")
	cmd.Flags().StringVar(&caKeyFile, "ca-key", "./certs/ca.key", "Path to CA private key file")
	cmd.Flags().StringVar(&outDir, "out-dir", "./certs", "Directory to write node key and certificate")
	cmd.Flags().IntVar(&validityYears, "validity", 1, "Node certificate validity in years")
	cmd.Flags().StringSliceVar(&hosts, "hosts", nil, "Additional DNS names or IP addresses for the certificate SAN (optional)")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}

// runInitCA generates a CA key + self-signed certificate and writes them to outDir.
func runInitCA(outDir string, validityYears int) error {
	if err := os.MkdirAll(outDir, 0700); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Lume CA",
			Organization: []string{"Lume"},
		},
		NotBefore:             now,
		NotAfter:              now.AddDate(validityYears, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}

	keyPath := filepath.Join(outDir, "ca.key")
	certPath := filepath.Join(outDir, "ca.crt")

	if err := writeECKey(keyPath, key); err != nil {
		return err
	}
	if err := writeCert(certPath, certDER); err != nil {
		return err
	}

	fmt.Printf("CA initialised successfully\n")
	fmt.Printf("  Certificate : %s\n", certPath)
	fmt.Printf("  Private key : %s  (keep secret)\n", keyPath)
	fmt.Printf("  Valid until : %s\n", tmpl.NotAfter.Format("2006-01-02"))
	return nil
}

// runIssue generates a node key + certificate signed by the CA and writes them to outDir.
func runIssue(nodeName, caCertFile, caKeyFile, outDir string, validityYears int, hosts []string) error {
	caCertPEM, err := os.ReadFile(caCertFile)
	if err != nil {
		return fmt.Errorf("read CA cert %s: %w", caCertFile, err)
	}
	block, _ := pem.Decode(caCertPEM)
	if block == nil {
		return fmt.Errorf("no PEM block found in %s", caCertFile)
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse CA cert: %w", err)
	}

	caKeyPEM, err := os.ReadFile(caKeyFile)
	if err != nil {
		return fmt.Errorf("read CA key %s: %w", caKeyFile, err)
	}
	caKey, err := parseECKey(caKeyPEM)
	if err != nil {
		return fmt.Errorf("parse CA key: %w", err)
	}

	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate node key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   nodeName,
			Organization: []string{"Lume"},
		},
		NotBefore: now,
		NotAfter:  now.AddDate(validityYears, 0, 0),
		// Node acts as both server and client in mutual TLS.
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &nodeKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create node certificate: %w", err)
	}

	if err := os.MkdirAll(outDir, 0700); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	keyPath := filepath.Join(outDir, nodeName+".key")
	certPath := filepath.Join(outDir, nodeName+".crt")

	if err := writeECKey(keyPath, nodeKey); err != nil {
		return err
	}
	if err := writeCert(certPath, certDER); err != nil {
		return err
	}

	fmt.Printf("Certificate issued for node %q\n", nodeName)
	fmt.Printf("  Certificate : %s\n", certPath)
	fmt.Printf("  Private key : %s\n", keyPath)
	fmt.Printf("  Signed by   : %s\n", caCert.Subject.CommonName)
	fmt.Printf("  Valid until : %s\n", tmpl.NotAfter.Format("2006-01-02"))
	return nil
}

// writeECKey writes an ECDSA private key to path in PEM format with mode 0600.
func writeECKey(path string, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal EC key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create key file %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// writeCert writes a DER-encoded certificate as PEM with mode 0644.
func writeCert(path string, certDER []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create cert file %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
}

// parseECKey decodes a PEM-encoded ECDSA private key.
func parseECKey(pemData []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse EC private key: %w", err)
	}
	return key, nil
}

// randomSerial generates a random 128-bit certificate serial number.
func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}
