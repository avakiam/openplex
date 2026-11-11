package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"openplex.dev/openplex/internal/extensions"
	"openplex.dev/openplex/internal/update"
)

// updateCommand: "openplexd update apply --data-dir DIR" installs a pending
// update. The daemon starts it (as a transient systemd unit) after verifying
// the release; it is not meant to be run by hand.
func updateCommand(args []string) error {
	if len(args) == 0 || args[0] != "apply" {
		return errors.New("usage: openplexd update apply --data-dir DIR")
	}
	fs := flag.NewFlagSet("update apply", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "/var/lib/openplex", "data directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return update.Apply(*dataDir, update.ApplyOptions{Log: func(f string, a ...any) { fmt.Printf(f+"\n", a...) }})
}

// releaseCommand: "openplexd release sign -key FILE SHA256SUMS" writes
// SHA256SUMS.sig next to it (release tooling, see scripts/sign-release.sh).
func releaseCommand(args []string) error {
	if len(args) == 0 || args[0] != "sign" {
		return errors.New("usage: openplexd release sign -key FILE SHA256SUMS")
	}
	fs := flag.NewFlagSet("release sign", flag.ContinueOnError)
	keyFile := fs.String("key", "", "release signing key (JSON from 'openplexd ext keygen')")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 1 || *keyFile == "" {
		return errors.New("usage: openplexd release sign -key FILE SHA256SUMS")
	}
	keyID, key, err := extensions.LoadSigningKey(*keyFile)
	if err != nil {
		return err
	}
	sums, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	sig, err := update.Sign(sums, keyID, key)
	if err != nil {
		return err
	}
	if _, ok := update.TrustedKeys[keyID]; !ok {
		fmt.Fprintf(os.Stderr, "warning: %s is not a key OpenPlex trusts for updates\n", keyID)
	}
	if err := os.WriteFile(fs.Arg(0)+".sig", append(sig, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s.sig: signed with %s\n", fs.Arg(0), keyID)
	return nil
}
