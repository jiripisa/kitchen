// Package updater implements `kitchen upgrade`: query GitHub releases, fetch
// the matching archive, verify checksum, and atomically replace the running
// binary.
package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Options configures Run.
type Options struct {
	Owner, Repo    string
	CurrentVersion string
	// HTTPClient lets tests inject a mock. nil ⇒ http.DefaultClient with a
	// sane timeout.
	HTTPClient *http.Client
}

// Run executes the upgrade flow.
func Run(ctx context.Context, out io.Writer, opts Options) error {
	if isDev(opts.CurrentVersion) {
		fmt.Fprintln(out, "skipping upgrade: this is a development build (version=dev).")
		return nil
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	rel, err := fetchLatestRelease(ctx, client, opts.Owner, opts.Repo)
	if err != nil {
		return err
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	current := strings.TrimPrefix(opts.CurrentVersion, "v")

	if !isNewer(latest, current) {
		fmt.Fprintf(out, "already up to date (kitchen %s)\n", current)
		return nil
	}

	fmt.Fprintf(out, "found a newer release: %s → %s\n", current, latest)

	archiveAsset, err := pickArchiveAsset(rel.Assets)
	if err != nil {
		return err
	}
	checksumAsset := pickChecksumAsset(rel.Assets)

	fmt.Fprintf(out, "downloading %s…\n", archiveAsset.Name)
	archivePath, err := downloadToTemp(ctx, client, archiveAsset.BrowserDownloadURL, archiveAsset.Name)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	if checksumAsset != nil {
		fmt.Fprintln(out, "verifying checksum…")
		if err := verifyChecksum(ctx, client, archivePath, archiveAsset.Name, checksumAsset.BrowserDownloadURL); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, "installing…")
	binPath, err := extractBinary(archivePath)
	if err != nil {
		return err
	}
	defer os.Remove(binPath)

	if err := replaceSelf(binPath); err != nil {
		return err
	}

	fmt.Fprintf(out, "✓ kitchen upgraded to %s\n", latest)
	return nil
}

func isDev(v string) bool {
	return v == "" || v == "dev"
}

// --- GitHub Releases API ---

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatestRelease(ctx context.Context, c *http.Client, owner, repo string) (*release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %s", resp.Status)
	}

	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &rel, nil
}

// --- Asset picking ---

func pickArchiveAsset(assets []asset) (*asset, error) {
	wantOS := runtime.GOOS
	wantArch := runtime.GOARCH
	for i := range assets {
		name := strings.ToLower(assets[i].Name)
		if !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		if strings.Contains(name, wantOS) && strings.Contains(name, wantArch) {
			return &assets[i], nil
		}
	}
	return nil, fmt.Errorf("no release asset found for %s/%s", wantOS, wantArch)
}

func pickChecksumAsset(assets []asset) *asset {
	for i := range assets {
		if strings.EqualFold(assets[i].Name, "checksums.txt") {
			return &assets[i]
		}
	}
	return nil
}

// --- Download / verify / extract ---

func downloadToTemp(ctx context.Context, c *http.Client, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: status %s", name, resp.Status)
	}

	f, err := os.CreateTemp("", "kitchen-upgrade-*-"+name)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("write archive: %w", err)
	}
	return f.Name(), nil
}

func verifyChecksum(ctx context.Context, c *http.Client, archivePath, archiveName, checksumURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download checksums: status %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}

	want := ""
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[1] == archiveName {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum entry for %s", archiveName)
	}

	got, err := sha256File(archivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extractBinary(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tar: %w", err)
		}
		if filepath.Base(hdr.Name) != "kitchen" {
			continue
		}
		out, err := os.CreateTemp("", "kitchen-bin-*")
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.Remove(out.Name())
			return "", err
		}
		out.Close()
		if err := os.Chmod(out.Name(), 0o755); err != nil {
			os.Remove(out.Name())
			return "", err
		}
		return out.Name(), nil
	}
	return "", fmt.Errorf("kitchen binary not found in archive")
}

// replaceSelf atomically replaces the running binary with the file at newPath.
//
// On Unix systems, renaming over an executable that is currently running is
// safe — the old process continues to use the unlinked inode. We use a
// temp file in the same directory as the target so rename stays atomic
// across the rename hop.
func replaceSelf(newPath string) error {
	target, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("resolve self: %w", err)
	}

	dir := filepath.Dir(target)
	staged, err := os.CreateTemp(dir, ".kitchen-update-*")
	if err != nil {
		return fmt.Errorf("stage update (need write access to %s): %w", dir, err)
	}
	stagedPath := staged.Name()
	staged.Close()

	if err := copyFile(newPath, stagedPath); err != nil {
		os.Remove(stagedPath)
		return err
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		os.Remove(stagedPath)
		return err
	}
	if err := os.Rename(stagedPath, target); err != nil {
		os.Remove(stagedPath)
		return fmt.Errorf("install update: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
