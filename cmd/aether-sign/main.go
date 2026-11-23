// SPDX-License-Identifier: GPL-3.0-or-later

// Command aether-sign manages the Ed25519 key that signs Aether Time releases.
//
//	aether-sign keygen
//	    Creates a key pair. The private key goes to aether-signing-key.secret
//	    (never printed, never committed); the public key goes to
//	    internal/updater/signing_key.pub, which is built into the app.
//	aether-sign sign [-key FILE] FILE
//	    Writes FILE.sig. The private key comes from -key or $AETHER_SIGNING_KEY.
//	aether-sign verify [-pub KEY] FILE
//	    Checks FILE.sig against the committed public key.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

const defaultPub = "internal/updater/signing_key.pub"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: aether-sign keygen [-out FILE] [-pub FILE] | sign [-key FILE] FILE | verify [-pub KEY] FILE")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "aether-signing-key.secret", "file that receives the PRIVATE key")
	pubPath := fs.String("pub", defaultPub, "file that receives the public key built into the app")
	force := fs.Bool("force", false, "replace an existing public key (only if the old private key is lost)")
	fs.Parse(args)

	if b, err := os.ReadFile(*pubPath); err == nil && strings.TrimSpace(string(b)) != "" && !*force {
		return fmt.Errorf("%s already holds a public key. Installed copies only accept updates signed with its private key, "+
			"so replacing it means users must update by hand once. Re-run with -force only if the old private key is lost", *pubPath)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	// O_EXCL: never overwrite a private key that might be the only copy.
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", *out, err)
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv) + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	if err := os.WriteFile(*pubPath, []byte(pubB64+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf(`Signing key created.

  Private key: %s   (keep it secret; it is git-ignored)
  Public key:  %s
               %s

Next steps:
  1. Store the private key as the GitHub secret AETHER_SIGNING_KEY:
       gh secret set AETHER_SIGNING_KEY < %s
     or paste the file's contents under Settings → Secrets and variables → Actions.
  2. Save a copy in your password manager, then delete %s.
  3. Commit the public key:
       git add %s && git commit -m "Add release signing key" && git push
`, *out, *pubPath, pubB64, *out, *out, *pubPath)
	return nil
}

func loadPrivate(keyFile string) (ed25519.PrivateKey, error) {
	raw := os.Getenv("AETHER_SIGNING_KEY")
	if keyFile != "" {
		b, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, err
		}
		raw = string(b)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("no private key: pass -key FILE or set AETHER_SIGNING_KEY")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("the private key is not a base64 Ed25519 key made by 'aether-sign keygen'")
	}
	return ed25519.PrivateKey(key), nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyFile := fs.String("key", "", "private key file (default: $AETHER_SIGNING_KEY)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	key, err := loadPrivate(*keyFile)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	sig := ed25519.Sign(key, data)
	return os.WriteFile(fs.Arg(0)+".sig", []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644)
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubFlag := fs.String("pub", "", "base64 public key (default: "+defaultPub+")")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	pubB64 := *pubFlag
	if pubB64 == "" {
		b, err := os.ReadFile(defaultPub)
		if err != nil {
			return err
		}
		pubB64 = string(b)
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid or missing public key")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	sigB64, err := os.ReadFile(fs.Arg(0) + ".sig")
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
		return errors.New("signature INVALID: the secret does not match the committed public key")
	}
	fmt.Println("signature OK")
	return nil
}
