package mcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/chromedp/chromedp"
	"github.com/mudler/nib/types"
)

type browserServer struct {
	cfg         types.BrowserConfig
	mu          sync.Mutex
	allocCancel context.CancelFunc
	ctxCancel   context.CancelFunc
	bctx        context.Context // the live browser context (nil until ensureBrowser)
	refs        map[string]int64
}

func newBrowserServer(cfg types.BrowserConfig) *browserServer {
	return &browserServer{cfg: cfg, refs: map[string]int64{}}
}

func (b *browserServer) profileDir() string {
	if b.cfg.ProfileDir != "" {
		return b.cfg.ProfileDir
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "dante-browser-profile")
}

// discoverChrome returns the Chrome/Chromium binary to drive: explicit path,
// else the first installed candidate for the platform, else "" (chromedp will
// then try its own default / a downloaded copy).
func discoverChrome(explicit string) string {
	if explicit != "" {
		return explicit
	}
	var cands []string
	switch runtime.GOOS {
	case "darwin":
		cands = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	case "linux":
		cands = []string{"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/brave-browser"}
	case "windows":
		cands = []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`, `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`}
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// ensureBrowser lazily launches the headed Chrome on the dedicated persistent
// profile and returns the browser context. Reused across calls.
func (b *browserServer) ensureBrowser(ctx context.Context) (context.Context, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bctx != nil {
		return b.bctx, nil
	}
	opts := append([]chromedp.ExecAllocatorOption{},
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.UserDataDir(b.profileDir()),
		chromedp.Flag("headless", false), // HEADED — visible + take-over-able
	)
	if p := discoverChrome(b.cfg.ChromePath); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	bctx, ctxCancel := chromedp.NewContext(allocCtx)
	// Kick the browser to actually start now (surfaces launch errors here).
	if err := chromedp.Run(bctx); err != nil {
		ctxCancel()
		allocCancel()
		return nil, err
	}
	b.allocCancel, b.ctxCancel, b.bctx = allocCancel, ctxCancel, bctx
	return b.bctx, nil
}

func (b *browserServer) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctxCancel != nil {
		b.ctxCancel()
	}
	if b.allocCancel != nil {
		b.allocCancel()
	}
	b.bctx = nil
}
