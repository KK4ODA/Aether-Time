// SPDX-License-Identifier: GPL-3.0-or-later

package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KK4ODA/Aether-Time/internal/config"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "0.9.9", 1},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3-beta.2", "1.2.3-beta.10", -1},
		{"1.2.3", "1.2.3-rc.1", 1},
		{"0.0.0-dev", "0.1.0", -1},
		{"1.10.0", "1.9.9", 1},
		{"garbage", "0.0.1", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

type fakeRelease struct {
	srv       *httptest.Server
	archive   []byte
	checksums string
	sig       string
}

func makeArchive(t *testing.T, content string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{{"README.md", "hi"}, {"aether-time", content}} {
		tw.WriteHeader(&tar.Header{Name: "aether-time_9.9.9_linux_amd64/" + f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(f.body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func newFakeRelease(t *testing.T, priv ed25519.PrivateKey) *fakeRelease {
	fr := &fakeRelease{archive: makeArchive(t, "NEW BINARY")}
	asset := AssetName("9.9.9", "linux", "amd64")
	sum := sha256.Sum256(fr.archive)
	fr.checksums = fmt.Sprintf("%s  %s\n%s  other.zip\n", hex.EncodeToString(sum[:]), asset, strings.Repeat("0", 64))
	fr.sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(fr.checksums)))
	mux := http.NewServeMux()
	fr.srv = httptest.NewServer(mux)
	mux.HandleFunc("/repos/KK4ODA/Aether-Time/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v9.9.9","name":"Aether Time 9.9.9","html_url":"x","assets":[
			{"name":%q,"browser_download_url":"%s/dl/archive"},
			{"name":"checksums.txt","browser_download_url":"%s/dl/checksums.txt"},
			{"name":"checksums.txt.sig","browser_download_url":"%s/dl/checksums.txt.sig"}]}`,
			asset, fr.srv.URL, fr.srv.URL, fr.srv.URL)
	})
	mux.HandleFunc("/dl/archive", func(w http.ResponseWriter, r *http.Request) { w.Write(fr.archive) })
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fr.checksums) })
	mux.HandleFunc("/dl/checksums.txt.sig", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, fr.sig) })
	return fr
}

func newTestUpdater(t *testing.T, apiBase string, pub ed25519.PublicKey) (*Updater, string) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "aether-time")
	os.WriteFile(exe, []byte("OLD BINARY"), 0o755)
	cfg := config.Default().Update
	cfg.APIBase = apiBase
	u := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	u.exe, u.goos, u.goarch, u.current, u.pubKey = exe, "linux", "amd64", "1.0.0", pub
	return u, exe
}

func TestCheckAndInstall(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := newFakeRelease(t, priv)
	defer fr.srv.Close()
	u, exe := newTestUpdater(t, fr.srv.URL, pub)
	installed := make(chan struct{}, 1)
	u.OnInstalled = func() { installed <- struct{}{} }

	rel, err := u.Check(context.Background())
	if err != nil || rel == nil || rel.Version != "9.9.9" {
		t.Fatalf("check: %+v %v", rel, err)
	}
	if err := u.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-installed
	if b, _ := os.ReadFile(exe); string(b) != "NEW BINARY" {
		t.Fatalf("executable content %q", b)
	}
	if st := u.Status(); st.State != "installed" {
		t.Fatalf("state %s", st.State)
	}
	CleanupOld(exe) // on Windows the old executable is removed at the next start
	if m, _ := filepath.Glob(exe + "*.old"); len(m) != 0 {
		t.Fatalf("leftover %v", m)
	}
}

func TestRejectsBadSignature(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	fr := newFakeRelease(t, priv)
	defer fr.srv.Close()
	u, exe := newTestUpdater(t, fr.srv.URL, otherPub)
	if _, err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := u.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "SIGNATURE") {
		t.Fatalf("expected signature failure, got %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD BINARY" {
		t.Fatal("executable modified despite bad signature")
	}
}

func TestRejectsTamperedArchive(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := newFakeRelease(t, priv)
	defer fr.srv.Close()
	fr.archive = makeArchive(t, "EVIL BINARY")
	u, exe := newTestUpdater(t, fr.srv.URL, pub)
	u.Check(context.Background())
	if err := u.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum failure, got %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD BINARY" {
		t.Fatal("executable modified despite checksum mismatch")
	}
}

func TestUpToDate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := newFakeRelease(t, priv)
	defer fr.srv.Close()
	u, _ := newTestUpdater(t, fr.srv.URL, pub)
	u.current = "9.9.9"
	rel, err := u.Check(context.Background())
	if err != nil || rel != nil {
		t.Fatalf("expected up to date, got %+v %v", rel, err)
	}
	if err := u.Install(context.Background()); err == nil {
		t.Fatal("install without an available update should fail")
	}
}

func TestOfflineCheck(t *testing.T) {
	u, _ := newTestUpdater(t, "http://127.0.0.1:1", nil)
	if _, err := u.Check(context.Background()); err == nil {
		t.Fatal("expected network error")
	}
	if st := u.Status(); st.State != "error" || st.LastError == "" {
		t.Fatalf("status %+v", st)
	}
}
