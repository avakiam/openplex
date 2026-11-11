package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"openplex.dev/openplex/internal/extensions"
)

// extCommand: openplexd ext pack|keygen …  (tools for extension authors)
func extCommand(args []string) error {
	usage := errors.New("usage: openplexd ext pack [-key FILE] [-o FILE] DIR | openplexd ext keygen -id KEY_ID -o FILE")
	if len(args) == 0 {
		return usage
	}
	switch args[0] {
	case "pack":
		return extPack(args[1:])
	case "keygen":
		return extKeygen(args[1:])
	}
	return usage
}

func extPack(args []string) error {
	fs := flag.NewFlagSet("ext pack", flag.ContinueOnError)
	keyFile := fs.String("key", "", "sign with this key file (from `openplexd ext keygen`)")
	out := fs.String("o", "", "output file (default: <id>-<version>.opx)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: openplexd ext pack [-key FILE] [-o FILE] DIR")
	}
	dir := fs.Arg(0)
	var keyID string
	var key ed25519.PrivateKey
	if *keyFile != "" {
		var err error
		if keyID, key, err = extensions.LoadSigningKey(*keyFile); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(".", ".opx-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	m, err := extensions.Pack(dir, tmp, keyID, key)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	name := *out
	if name == "" {
		name = fmt.Sprintf("%s-%s.opx", m.ID, m.Version)
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		return err
	}
	signed := "unsigned"
	if key != nil {
		signed = "signed with " + keyID
	}
	fmt.Printf("%s: %s %s (%s)\n", name, m.ID, m.Version, signed)
	return nil
}

var keyIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,40}$`)

func extKeygen(args []string) error {
	fs := flag.NewFlagSet("ext keygen", flag.ContinueOnError)
	id := fs.String("id", "", "key id, e.g. yourcompany-2026")
	out := fs.String("o", "", "private key file to create")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !keyIDRe.MatchString(*id) || *out == "" {
		return errors.New("usage: openplexd ext keygen -id KEY_ID -o FILE")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(extensions.SigningKey{KeyID: *id, PrivateKey: base64.StdEncoding.EncodeToString(priv)}, "", "  ")
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("private key: %s (keep it secret and backed up)\npublic key %s: %s\n", *out, *id, base64.StdEncoding.EncodeToString(pub))
	return nil
}
