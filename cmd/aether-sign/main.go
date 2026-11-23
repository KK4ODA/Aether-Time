// Command aether-sign manages the Ed25519 key that signs Aether Time releases.
//
//	aether-sign keygen [-write internal/updater/signing_key.pub]
//	    Creates a key pair. Store the private key as the AETHER_SIGNING_KEY
//	    repository secret; commit the public key so it is built into the app.
//	aether-sign sign FILE        (private key in $AETHER_SIGNING_KEY)
//	    Writes FILE.sig.
//	aether-sign verify FILE      (public key from internal/updater/signing_key.pub or -pub)
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
	fmt.Fprintln(os.Stderr, "usage: aether-sign keygen [-write PUBFILE] | sign FILE | verify [-pub KEY] FILE")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	write := fs.String("write", "", "write the public key to this file")
	fs.Parse(args)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	fmt.Println("Private key (store as the AETHER_SIGNING_KEY secret; never commit it):")
	fmt.Println(base64.StdEncoding.EncodeToString(priv))
	fmt.Println()
	fmt.Println("Public key:")
	fmt.Println(pubB64)
	if *write != "" {
		if err := os.WriteFile(*write, []byte(pubB64+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Println("\nPublic key written to", *write)
	}
	return nil
}

func sign(args []string) error {
	if len(args) != 1 {
		usage()
	}
	raw := strings.TrimSpace(os.Getenv("AETHER_SIGNING_KEY"))
	if raw == "" {
		return errors.New("AETHER_SIGNING_KEY is not set")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return errors.New("AETHER_SIGNING_KEY is not a base64 Ed25519 private key")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	sig := ed25519.Sign(ed25519.PrivateKey(key), data)
	return os.WriteFile(args[0]+".sig", []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644)
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubFlag := fs.String("pub", "", "base64 public key (default: internal/updater/signing_key.pub)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	pubB64 := *pubFlag
	if pubB64 == "" {
		b, err := os.ReadFile("internal/updater/signing_key.pub")
		if err != nil {
			return err
		}
		pubB64 = string(b)
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid public key")
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
		return errors.New("signature INVALID")
	}
	fmt.Println("signature OK")
	return nil
}
