// Package update asks GitHub whether a newer dualnet release exists. It only
// reads the public "latest release" of the project; nothing is downloaded or
// installed automatically. Installing is a separate step the user asks for
// (see install.go).
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Info is the result of the last check.
type Info struct {
	Available bool      `json:"available"`
	Latest    string    `json:"latest"`
	URL       string    `json:"url"`
	CheckedAt time.Time `json:"checkedAt"`
}

// Checker polls the releases API.
type Checker struct {
	Current string // this build's version, e.g. "v0.2.2" ("dev…" never reports updates)
	Repo    string // "owner/name"
	API     string // base URL, default https://api.github.com
	// Download is the base URL release files are fetched from when the user
	// installs an update (default https://github.com; tests point it elsewhere).
	Download string
	Enabled  func() bool

	mu   sync.Mutex
	info Info
}

// Info returns the last known result.
func (c *Checker) Info() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Check performs one check (a no-op when disabled).
func (c *Checker) Check(ctx context.Context) error {
	if c.Enabled != nil && !c.Enabled() {
		return nil
	}
	api := c.API
	if api == "" {
		api = "https://api.github.com"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+c.Repo+"/releases/latest", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "dualnet/"+c.Current)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("releases API: HTTP %d", resp.StatusCode)
	}
	var rel struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&rel); err != nil {
		return err
	}
	info := Info{Latest: rel.Tag, CheckedAt: time.Now()}
	// Only ever offer a page of our own repository, whatever the API answers.
	if !rel.Draft && !rel.Prerelease && strings.HasPrefix(rel.URL, "https://github.com/"+c.Repo+"/") && Newer(rel.Tag, c.Current) {
		info.Available, info.URL = true, rel.URL
	}
	c.mu.Lock()
	c.info = info
	c.mu.Unlock()
	return nil
}

// Run checks at startup (after a short delay) and then every interval.
func (c *Checker) Run(ctx context.Context, every time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(20 * time.Second):
	}
	for {
		c.Check(ctx) // errors (offline…) are not worth reporting
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Newer reports whether version latest is newer than current. Versions look
// like "v1.2.3"; a suffix ("-dev", "-local") makes a build older than the same
// release. Anything unparsable (including "dev") is never considered outdated.
func Newer(latest, current string) bool {
	l, _, ok1 := parse(latest)
	c, pre, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return pre // same numbers: a pre-release build is older than the release
}

func parse(v string) (n [3]int, pre bool, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v, pre = v[:i], true
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return n, false, false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return n, false, false
		}
		n[i] = x
	}
	return n, pre, true
}
