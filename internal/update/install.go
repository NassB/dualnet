package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Installing an update (only when the user asks for it in the interface).
//
// The release's dualnet.exe and dualnet.exe.sha256 are downloaded from the
// project's own releases on github.com, the file is checked against the
// checksum, then it takes the place of the running program, which is kept as
// dualnet.exe.old until the new one has started.
//
// The checksum comes from the same place as the file: it catches a damaged or
// truncated download, not a compromised release. What vouches for the release
// is the HTTPS connection to github.com (see README, "Verifying a download",
// for the stronger build attestation).

const maxExe = 200 << 20 // far above any real build; stops a runaway download

// asset is the file a release provides for this system.
const asset = "dualnet.exe"

// Fetch downloads the latest release's program next to exe as exe+".new" and
// returns that path once its checksum is verified.
func (c *Checker) Fetch(ctx context.Context, exe string) (string, error) {
	info := c.Info()
	if !info.Available {
		return "", errors.New("no update available")
	}
	if _, _, ok := parse(info.Latest); !ok || !strings.HasPrefix(info.Latest, "v") {
		return "", fmt.Errorf("unexpected version %q", info.Latest)
	}
	base := c.Download
	if base == "" {
		base = "https://github.com"
	}
	url := base + "/" + c.Repo + "/releases/download/" + info.Latest + "/" + asset
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	sumFile, err := c.get(ctx, url+".sha256", 4096)
	if err != nil {
		return "", fmt.Errorf("checksum: %w", err)
	}
	want, err := parseSum(sumFile)
	if err != nil {
		return "", err
	}
	body, err := c.get(ctx, url, maxExe)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	if got := sha256.Sum256(body); !bytes.Equal(got[:], want) {
		return "", fmt.Errorf("the downloaded file does not match its checksum (got %x)", got[:6])
	}
	if len(body) < 1<<20 || body[0] != 'M' || body[1] != 'Z' {
		return "", errors.New("the downloaded file is not a Windows program")
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, body, 0o755); err != nil {
		return "", err
	}
	return tmp, nil
}

func (c *Checker) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dualnet/"+c.Current)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("file too large")
	}
	return b, nil
}

// parseSum reads the first field of a sha256sum line ("<hex>  dualnet.exe").
func parseSum(b []byte) ([]byte, error) {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return nil, errors.New("empty checksum file")
	}
	sum, err := hex.DecodeString(f[0])
	if err != nil || len(sum) != sha256.Size {
		return nil, errors.New("unreadable checksum file")
	}
	return sum, nil
}

// Swap puts the verified file next (from Fetch) in place of exe. The running
// program cannot be overwritten on Windows, but it can be renamed: it stays as
// exe+".old" (see RemoveOld) and is put back if the swap fails.
func Swap(exe, next string) error {
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(next)
		return err
	}
	if err := os.Rename(next, exe); err != nil {
		os.Rename(old, exe)
		os.Remove(next)
		return err
	}
	return nil
}

// RemoveOld deletes what an update left next to exe (the previous program,
// which could not be deleted while it was running).
func RemoveOld(exe string) {
	os.Remove(exe + ".old")
	os.Remove(exe + ".new")
}
