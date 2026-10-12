package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Issuance runs with the service stopped, as its service account, so the panel,
// CLI and renewal scheduler cannot concurrently replace settings or key files.
func runTLSCommand(args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 || args[0] != "issue" {
		return errors.New("usage: meridian tls issue --domain panel.example.com --wildcard '*.example.com' --email admin@example.com --port 9090 --token-stdin")
	}
	flags := flag.NewFlagSet("tls issue", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	domain := flags.String("domain", "", "panel hostname")
	wildcard := flags.String("wildcard", "", "wildcard domain")
	email := flags.String("email", "", "ACME email")
	dbPath := flags.String("db", os.Getenv("DB_PATH"), "database path")
	port := flags.Int("port", 9090, "panel port")
	stdin := flags.Bool("token-stdin", false, "read DNS token from stdin")
	staging := flags.Bool("staging", false, "ACME staging")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !*stdin || *dbPath == "" {
		return errors.New("invalid tls issue arguments")
	}
	route, err := normalizeWildcardDomain(*wildcard)
	if err != nil {
		return err
	}
	settings, err := normalizeManagedPanelSettings(*domain, route)
	if err != nil {
		return err
	}
	if err = validatePanelListenPort(*port); err != nil {
		return err
	}
	tokenBytes, err := io.ReadAll(io.LimitReader(input, 514))
	if err != nil || len(tokenBytes) > 513 {
		return errors.New("DNS token input is too large")
	}
	token := strings.TrimSpace(string(tokenBytes))
	if err = validatePanelCertificateRequest(strings.TrimSpace(*email), token); err != nil {
		return err
	}
	if jwtSecretEphemeral {
		return errors.New("a persistent JWT_SECRET is required for certificate renewal")
	}
	db, err := openDB(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	manager := newPanelCertificateManager(*dbPath, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if *staging {
		if err := manager.testCertificate(ctx, strings.TrimSpace(*email), token, settings); err != nil {
			return err
		}
		_, err := fmt.Fprintln(output, "ACME staging test succeeded; service certificate and renewal settings were not changed.")
		return err
	}
	issued, err := manager.requestCertificate(ctx, strings.TrimSpace(*email), token, settings.PanelDomain, settings.RouteDomain, false)
	if err != nil {
		return err
	}
	issued, err = manager.validateProductionCertificate(issued, settings)
	if err != nil {
		return err
	}
	ciphertext, err := encryptPanelACMEToken(token)
	if err != nil {
		return err
	}
	backup, err := manager.backupInstalledFiles()
	if err != nil {
		return err
	}
	previous, err := db.PanelSettings()
	if err != nil {
		return err
	}
	restore := func() { _ = manager.restoreInstalledFiles(backup); _ = db.restorePanelSettings(previous) }
	if err = manager.install(issued, false); err != nil {
		restore()
		return err
	}
	if _, _, err = db.SaveManagedPanelSettings(settings.PanelDomain, settings.RouteDomain, *port, true); err != nil {
		restore()
		return err
	}
	if err = db.savePanelACMECertificateSettings(strings.TrimSpace(*email), "cloudflare", ciphertext); err != nil {
		restore()
		return err
	}
	_, err = fmt.Fprintln(output, "Certificate installed; automatic renewal configured.")
	return err
}
