// SPDX-License-Identifier: GPL-3.0-or-later

// Package updater keeps Aether Time up to date from GitHub Releases.
//
// Every release publishes per-platform archives, a checksums.txt file listing
// their SHA-256 hashes, and checksums.txt.sig, an Ed25519 signature of that
// file. The updater verifies the signature with the public key compiled into
// the binary, then the archive hash, then swaps the executable atomically and
// restarts. Being offline simply postpones the next check.
package updater

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/version"
)

//go:embed signing_key.pub
var embeddedPublicKey string

// Release describes an available version.
type Release struct {
	Version      string    `json:"version"`
	Tag          string    `json:"tag"`
	Name         string    `json:"name"`
	Notes        string    `json:"notes"`
	URL          string    `json:"url"`
	Published    time.Time `json:"published"`
	Prerelease   bool      `json:"prerelease"`
	AssetName    string    `json:"asset_name"`
	assetURL     string
	checksumsURL string
	signatureURL string
}

// Status is reported to the UI.
type Status struct {
	Current     string     `json:"current"`
	Latest      *Release   `json:"latest,omitempty"`
	Available   bool       `json:"available"`
	State       string     `json:"state"` // idle, checking, downloading, installing, installed, error, disabled
	Progress    float64    `json:"progress"`
	LastCheck   *time.Time `json:"last_check,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	Signed      bool       `json:"signature_check"`
	AutoInstall bool       `json:"auto_install"`
	Channel     string     `json:"channel"`
}

// Updater checks for and installs updates.
type Updater struct {
	cfg     config.UpdateConfig
	current string
	client  *http.Client
	pubKey  ed25519.PublicKey
	exe     string
	goos    string
	goarch  string
	log     *slog.Logger

	// OnInstalled is called after an update was installed; it should restart the program.
	OnInstalled func()

	mu     sync.Mutex
	status Status
	busy   bool
}

// New creates an updater for the running executable.
func New(cfg config.UpdateConfig, log *slog.Logger) *Updater {
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	u := &Updater{
		cfg:     cfg,
		current: version.Version,
		client:  &http.Client{Timeout: 5 * time.Minute},
		exe:     exe,
		goos:    runtime.GOOS,
		goarch:  runtime.GOARCH,
		log:     log,
	}
	if k, err := parsePublicKey(embeddedPublicKey); err == nil {
		u.pubKey = k
	}
	u.status = Status{Current: u.current, State: "idle", Signed: u.pubKey != nil, AutoInstall: cfg.AutoInstall, Channel: cfg.Channel}
	if !cfg.Enabled {
		u.status.State = "disabled"
	}
	return u
}

func parsePublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("no public key")
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("invalid public key")
	}
	return ed25519.PublicKey(b), nil
}

// Status returns the current updater status.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.status
	if s.Latest != nil {
		r := *s.Latest
		s.Latest = &r
	}
	return s
}

func (u *Updater) set(f func(*Status)) {
	u.mu.Lock()
	f(&u.status)
	u.mu.Unlock()
}

// Run checks periodically and installs automatically when configured.
func (u *Updater) Run(ctx context.Context) {
	if !u.cfg.Enabled {
		return
	}
	CleanupOld(u.exe)
	wait := 90 * time.Second // let the program settle first
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		rel, err := u.Check(ctx)
		wait = time.Duration(u.cfg.CheckIntervalH * float64(time.Hour))
		if err != nil {
			u.log.Debug("update check failed (offline?)", "err", err)
			if wait > time.Hour {
				wait = time.Hour
			}
			continue
		}
		if rel != nil && u.cfg.AutoInstall {
			if err := u.Install(ctx); err != nil {
				u.log.Warn("automatic update failed", "err", err)
			}
		}
	}
}

// Check asks GitHub for the newest release. It returns nil when up to date.
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	u.set(func(s *Status) { s.State, s.LastError = "checking", "" })
	rel, err := u.latest(ctx)
	now := time.Now().UTC()
	if err != nil {
		u.set(func(s *Status) { s.State, s.LastError, s.LastCheck = "error", err.Error(), &now })
		return nil, err
	}
	avail := rel != nil && compareVersions(rel.Version, u.current) > 0
	u.set(func(s *Status) {
		s.State, s.LastCheck, s.Latest, s.Available = "idle", &now, rel, avail
	})
	if avail {
		u.log.Info("update available", "current", u.current, "latest", rel.Version)
		return rel, nil
	}
	return nil, nil
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (u *Updater) latest(ctx context.Context) (*Release, error) {
	base := strings.TrimRight(u.cfg.APIBase, "/")
	var list []ghRelease
	if u.cfg.Channel == "beta" {
		if err := u.getJSON(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=20", base, u.cfg.Repo), &list); err != nil {
			return nil, err
		}
	} else {
		var one ghRelease
		if err := u.getJSON(ctx, fmt.Sprintf("%s/repos/%s/releases/latest", base, u.cfg.Repo), &one); err != nil {
			return nil, err
		}
		list = []ghRelease{one}
	}
	var best *Release
	for _, r := range list {
		if r.Draft || (r.Prerelease && u.cfg.Channel != "beta") {
			continue
		}
		rel := u.toRelease(r)
		if rel == nil {
			continue
		}
		if best == nil || compareVersions(rel.Version, best.Version) > 0 {
			best = rel
		}
	}
	return best, nil
}

// AssetName returns the archive name for a version on this platform.
func AssetName(ver, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s.%s", version.BinaryName, ver, goos, goarch, ext)
}

func (u *Updater) toRelease(r ghRelease) *Release {
	ver := strings.TrimPrefix(r.TagName, "v")
	if !parseSemver(ver).ok {
		return nil
	}
	rel := &Release{Version: ver, Tag: r.TagName, Name: r.Name, Notes: r.Body, URL: r.HTMLURL,
		Published: r.PublishedAt, Prerelease: r.Prerelease, AssetName: AssetName(ver, u.goos, u.goarch)}
	for _, a := range r.Assets {
		switch a.Name {
		case rel.AssetName:
			rel.assetURL = a.URL
		case "checksums.txt":
			rel.checksumsURL = a.URL
		case "checksums.txt.sig":
			rel.signatureURL = a.URL
		}
	}
	if rel.assetURL == "" || rel.checksumsURL == "" {
		return nil // no build for this platform in that release
	}
	return rel
}

func (u *Updater) getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", version.BinaryName+"/"+u.current)
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("no releases published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

func (u *Updater) download(ctx context.Context, url string, w io.Writer, limit int64, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", version.BinaryName+"/"+u.current)
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", path.Base(url), resp.Status)
	}
	r := io.LimitReader(resp.Body, limit)
	buf := make([]byte, 64*1024)
	var done int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if progress != nil {
				progress(done, resp.ContentLength)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Install downloads, verifies and installs the latest available release.
func (u *Updater) Install(ctx context.Context) error {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return errors.New("an update is already in progress")
	}
	rel := u.status.Latest
	if rel == nil || !u.status.Available {
		u.mu.Unlock()
		return errors.New("no update available; check first")
	}
	u.busy = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.busy = false
		u.mu.Unlock()
	}()
	err := u.install(ctx, rel)
	if err != nil {
		u.set(func(s *Status) { s.State, s.LastError = "error", err.Error() })
		return err
	}
	u.set(func(s *Status) { s.State, s.Progress, s.Available = "installed", 1, false })
	u.log.Info("update installed; restarting", "version", rel.Version)
	if u.OnInstalled != nil {
		go u.OnInstalled()
	}
	return nil
}

func (u *Updater) install(ctx context.Context, rel *Release) error {
	u.set(func(s *Status) { s.State, s.Progress, s.LastError = "downloading", 0, "" })

	// 1. checksums.txt and its signature.
	var sums strings.Builder
	if err := u.download(ctx, rel.checksumsURL, &sums, 1<<20, nil); err != nil {
		return err
	}
	if u.pubKey != nil {
		if rel.signatureURL == "" {
			return errors.New("release is not signed (checksums.txt.sig missing)")
		}
		var sigB64 strings.Builder
		if err := u.download(ctx, rel.signatureURL, &sigB64, 4096, nil); err != nil {
			return err
		}
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64.String()))
		if err != nil || !ed25519.Verify(u.pubKey, []byte(sums.String()), sig) {
			return errors.New("SIGNATURE VERIFICATION FAILED: the release was not signed by the Aether Time key")
		}
	} else {
		u.log.Warn("no update signing key compiled in; verifying the SHA-256 checksum only")
	}
	want, err := findChecksum(sums.String(), rel.AssetName)
	if err != nil {
		return err
	}

	// 2. The archive, hashed while downloading. It goes next to the executable
	// so the final rename stays on one filesystem.
	dir := filepath.Dir(u.exe)
	tmp, err := os.CreateTemp(dir, ".aether-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (run as administrator/root or install the service): %w", dir, err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	err = u.download(ctx, rel.assetURL, io.MultiWriter(tmp, h), 200<<20, func(done, total int64) {
		if total > 0 {
			u.set(func(s *Status) { s.Progress = float64(done) / float64(total) })
		}
	})
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s", rel.AssetName)
	}

	// 3. Extract and swap.
	u.set(func(s *Status) { s.State = "installing" })
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	newExe := filepath.Join(dir, "."+filepath.Base(u.exe)+".new")
	if err := extractBinary(tmp, rel.AssetName, u.goos, newExe); err != nil {
		return err
	}
	defer os.Remove(newExe)
	return replaceExecutable(u.exe, newExe)
}

func findChecksum(sums, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("%s is not listed in checksums.txt", name)
}

func binaryName(goos string) string {
	if goos == "windows" {
		return version.BinaryName + ".exe"
	}
	return version.BinaryName
}

func extractBinary(f *os.File, assetName, goos, dest string) error {
	want := binaryName(goos)
	var src io.Reader
	if strings.HasSuffix(assetName, ".zip") {
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(f, fi.Size())
		if err != nil {
			return err
		}
		for _, zf := range zr.File {
			if path.Base(zf.Name) == want && !zf.FileInfo().IsDir() {
				rc, err := zf.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				src = rc
				break
			}
		}
	} else {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if path.Base(hdr.Name) == want && hdr.Typeflag == tar.TypeReg {
				src = tr
				break
			}
		}
	}
	if src == nil {
		return fmt.Errorf("%s not found in %s", want, assetName)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(src, 200<<20)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// replaceExecutable swaps newExe into place. A running executable can be
// renamed on every supported OS (but not overwritten on Windows), so the old
// one is moved aside first and restored if anything fails.
func replaceExecutable(exe, newExe string) error {
	old := exe + ".old"
	_ = os.Remove(old)
	if _, err := os.Stat(old); err == nil {
		old = fmt.Sprintf("%s.%d.old", exe, time.Now().Unix())
	}
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("cannot move the current executable aside: %w", err)
	}
	if err := os.Rename(newExe, exe); err != nil {
		_ = os.Rename(old, exe)
		return fmt.Errorf("cannot install the new executable: %w", err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Remove(old) // the running process keeps its inode
	}
	return nil
}

// CleanupOld removes executables left behind by previous updates.
func CleanupOld(exe string) {
	if exe == "" {
		return
	}
	matches, _ := filepath.Glob(exe + "*.old")
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
