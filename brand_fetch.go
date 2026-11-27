package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"gorex/internal/homedir"
)

const brandMaxBytes = 256 * 1024

var (
	// brandBaseURL is where non-embedded brand SVGs are fetched on demand.
	// web/build.mjs publishes both assets/brands/ and assets/brands-extra/
	// under /brands/ on https://web.gorex.uk.
	brandBaseURL    = "https://web.gorex.uk/brands"
	brandHTTPClient = &http.Client{Timeout: 10 * time.Second}

	brandMu          sync.Mutex
	brandSVGs        = map[string]*ui.SVG{}
	brandDiskChecked = map[string]bool{}
	brandFetching    = map[string]bool{}
	brandFailed      = map[string]bool{}

	// onBrandLoaded, when set, is called after an on-demand brand SVG finishes
	// downloading and parsing.
	onBrandLoaded func(id string)
)

func brandCacheDir() string {
	dir := homedir.DataDir(devBuild())
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "cache", "brands")
}

func validBrandID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// onDemandBrandIcon returns a cached SVG for a non-embedded brand mark, or
// starts a background fetch from brandBaseURL and returns the terminal glyph
// as an immediate non-blocking fallback.
func onDemandBrandIcon(id string) *ui.SVG {
	if !validBrandID(id) {
		return icon("terminal")
	}

	brandMu.Lock()
	if s, ok := brandSVGs[id]; ok {
		brandMu.Unlock()
		return s
	}
	needDisk := !brandDiskChecked[id]
	if needDisk {
		brandDiskChecked[id] = true
	}
	brandMu.Unlock()

	if needDisk {
		if dir := brandCacheDir(); dir != "" {
			if data, err := os.ReadFile(filepath.Join(dir, id+".svg")); err == nil && len(data) <= brandMaxBytes {
				if s, err := ui.ParseSVG(data); err == nil {
					brandMu.Lock()
					brandSVGs[id] = s
					brandMu.Unlock()
					return s
				}
			}
		}
	}

	brandMu.Lock()
	shouldFetch := !brandFetching[id] && !brandFailed[id] && brandBaseURL != ""
	if shouldFetch {
		brandFetching[id] = true
	}
	url := brandBaseURL
	brandMu.Unlock()

	if shouldFetch {
		go fetchBrandSVG(id, url)
	}
	return icon("terminal")
}

func fetchBrandSVG(id, baseURL string) {
	fail := func() {
		brandMu.Lock()
		delete(brandFetching, id)
		brandFailed[id] = true
		brandMu.Unlock()
	}

	resp, err := brandHTTPClient.Get(baseURL + "/" + id + ".svg")
	if err != nil {
		fail()
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail()
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, brandMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > brandMaxBytes {
		fail()
		return
	}
	s, err := ui.ParseSVG(data)
	if err != nil {
		fail()
		return
	}

	if dir := brandCacheDir(); dir != "" {
		if os.MkdirAll(dir, 0o755) == nil {
			if tmp, err := os.CreateTemp(dir, id+".*.tmp"); err == nil {
				tmpName := tmp.Name()
				_, werr := tmp.Write(data)
				cerr := tmp.Close()
				if werr == nil && cerr == nil {
					_ = os.Rename(tmpName, filepath.Join(dir, id+".svg"))
				} else {
					_ = os.Remove(tmpName)
				}
			}
		}
	}

	brandMu.Lock()
	brandSVGs[id] = s
	delete(brandFetching, id)
	cb := onBrandLoaded
	brandMu.Unlock()

	if cb != nil {
		cb(id)
	}
}
