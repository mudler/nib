package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

type browserPathFixture struct {
	root          string
	canonicalRoot string
	outside       string
	upload        string
	nestedUpload  string
	downloads     string
}

func newBrowserPathFixture(t *testing.T) browserPathFixture {
	t.Helper()

	root := t.TempDir()
	outside := t.TempDir()
	mustMkdirBrowserPath(t, filepath.Join(root, "nested"))
	mustMkdirBrowserPath(t, filepath.Join(root, "downloads"))
	mustWriteBrowserPath(t, filepath.Join(root, "upload.txt"), "upload-one")
	mustWriteBrowserPath(t, filepath.Join(root, "nested", "second.bin"), "upload-two")
	mustWriteBrowserPath(t, filepath.Join(outside, "outside.txt"), "outside")
	mustMkdirBrowserPath(t, filepath.Join(outside, "outside-dir"))

	mustSymlinkBrowserPath(t, "upload.txt", filepath.Join(root, "upload-link"))
	mustSymlinkBrowserPath(t, "nested", filepath.Join(root, "nested-link"))
	mustSymlinkBrowserPath(t, filepath.Join(outside, "outside.txt"), filepath.Join(root, "outside-file-link"))
	mustSymlinkBrowserPath(t, filepath.Join(outside, "outside-dir"), filepath.Join(root, "outside-dir-link"))
	mustSymlinkBrowserPath(t, "missing-target", filepath.Join(root, "broken-link"))

	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize fixture root: %v", err)
	}
	return browserPathFixture{
		root:          root,
		canonicalRoot: canonicalRoot,
		outside:       outside,
		upload:        filepath.Join(canonicalRoot, "upload.txt"),
		nestedUpload:  filepath.Join(canonicalRoot, "nested", "second.bin"),
		downloads:     filepath.Join(canonicalRoot, "downloads"),
	}
}

func mustMkdirBrowserPath(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("mkdir browser fixture: %v", err)
	}
}

func mustWriteBrowserPath(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write browser fixture: %v", err)
	}
}

func mustSymlinkBrowserPath(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Fatalf("symlink browser fixture: %v", err)
	}
}

func assertBrowserErrorHasNoPaths(t *testing.T, err error, paths ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a path validation error")
	}
	for _, path := range paths {
		if path != "" && strings.Contains(err.Error(), path) {
			t.Fatalf("error leaked private path %q: %v", path, err)
		}
	}
}

func TestBrowserPathResolvesOnlyCanonicalWorkingDirEntries(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	rootLink := filepath.Join(t.TempDir(), "working-link")
	mustSymlinkBrowserPath(t, fixture.root, rootLink)

	tests := []struct {
		name     string
		root     string
		relative string
		kind     browserPathKind
		want     string
	}{
		{name: "root file", root: fixture.root, relative: "upload.txt", kind: browserUploadFile, want: fixture.upload},
		{name: "nested file", root: fixture.root, relative: filepath.Join("nested", "second.bin"), kind: browserUploadFile, want: fixture.nestedUpload},
		{name: "download directory", root: fixture.root, relative: "downloads", kind: browserDownloadDir, want: fixture.downloads},
		{name: "download defaults to root", root: fixture.root, relative: "", kind: browserDownloadDir, want: fixture.canonicalRoot},
		{name: "working dir itself may be a symlink", root: rootLink, relative: "upload.txt", kind: browserUploadFile, want: fixture.upload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveBrowserPath(test.root, test.relative, test.kind)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("resolved path = %q, want %q", got, test.want)
			}
			if !filepath.IsAbs(got) {
				t.Fatalf("resolved path is not absolute: %q", got)
			}
			rel, err := filepath.Rel(fixture.canonicalRoot, got)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("resolved path escaped canonical WorkingDir: path=%q rel=%q err=%v", got, rel, err)
			}
		})
	}
}

func TestBrowserPathUsesCurrentDirectoryForEmptyWorkingDir(t *testing.T) {
	want, err := filepath.EvalSymlinks(".")
	if err != nil {
		t.Fatal(err)
	}
	want, err = filepath.Abs(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveBrowserPath("", "", browserDownloadDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("empty WorkingDir resolved to %q, want current directory %q", got, want)
	}
}

func TestBrowserPathRejectsTraversalSymlinksAndWrongKinds(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	missingRoot := filepath.Join(t.TempDir(), "missing-root")

	tests := []struct {
		name     string
		root     string
		relative string
		kind     browserPathKind
	}{
		{name: "empty upload", root: fixture.root, relative: "", kind: browserUploadFile},
		{name: "absolute upload", root: fixture.root, relative: fixture.upload, kind: browserUploadFile},
		{name: "absolute download", root: fixture.root, relative: fixture.downloads, kind: browserDownloadDir},
		{name: "literal traversal that cleans inside", root: fixture.root, relative: "nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "upload.txt", kind: browserUploadFile},
		{name: "literal traversal escapes", root: fixture.root, relative: ".." + string(filepath.Separator) + filepath.Base(fixture.outside) + string(filepath.Separator) + "outside.txt", kind: browserUploadFile},
		{name: "directory upload", root: fixture.root, relative: "nested", kind: browserUploadFile},
		{name: "missing upload", root: fixture.root, relative: "missing.txt", kind: browserUploadFile},
		{name: "internal file symlink", root: fixture.root, relative: "upload-link", kind: browserUploadFile},
		{name: "external file symlink", root: fixture.root, relative: "outside-file-link", kind: browserUploadFile},
		{name: "internal directory symlink component", root: fixture.root, relative: filepath.Join("nested-link", "second.bin"), kind: browserUploadFile},
		{name: "broken symlink", root: fixture.root, relative: "broken-link", kind: browserUploadFile},
		{name: "file download destination", root: fixture.root, relative: "upload.txt", kind: browserDownloadDir},
		{name: "missing download destination", root: fixture.root, relative: "missing-dir", kind: browserDownloadDir},
		{name: "internal directory symlink destination", root: fixture.root, relative: "nested-link", kind: browserDownloadDir},
		{name: "external directory symlink destination", root: fixture.root, relative: "outside-dir-link", kind: browserDownloadDir},
		{name: "missing WorkingDir", root: missingRoot, relative: "upload.txt", kind: browserUploadFile},
		{name: "file WorkingDir", root: fixture.upload, relative: "upload.txt", kind: browserUploadFile},
		{name: "unknown required kind", root: fixture.root, relative: "upload.txt", kind: browserPathKind(99)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveBrowserPath(test.root, test.relative, test.kind)
			assertBrowserErrorHasNoPaths(t, err, fixture.root, fixture.canonicalRoot, fixture.outside)
		})
	}
}

func TestBrowserPathRejectsWindowsTraversalAndVolumes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path grammar is host-native")
	}
	root := t.TempDir()
	tests := []struct {
		name       string
		relative   string
		wantReason string
	}{
		{name: "backslash traversal", relative: `nested\..\upload.txt`, wantReason: "parent traversal"},
		{name: "mixed slash traversal", relative: `nested/..\upload.txt`, wantReason: "parent traversal"},
		{name: "mixed backslash traversal", relative: `nested\../upload.txt`, wantReason: "parent traversal"},
		{name: "drive relative", relative: `C:upload.txt`, wantReason: "relative to WorkingDir"},
		{name: "drive absolute", relative: `C:\upload.txt`, wantReason: "relative to WorkingDir"},
		{name: "UNC path", relative: `\\server\share\upload.txt`, wantReason: "relative to WorkingDir"},
		{name: "device path", relative: `\\?\C:\upload.txt`, wantReason: "relative to WorkingDir"},
		{name: "device namespace", relative: `\\.\PhysicalDrive0`, wantReason: "relative to WorkingDir"},
		{name: "root relative Windows path", relative: `\Windows\upload.txt`, wantReason: "relative to WorkingDir"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveBrowserPath(root, test.relative, browserUploadFile)
			assertBrowserErrorHasNoPaths(t, err, root)
			if !strings.Contains(err.Error(), test.wantReason) {
				t.Fatalf("error = %v, want %q", err, test.wantReason)
			}
		})
	}
}

func TestBrowserPathAcceptsPOSIXColonAndBackslashComponents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("colon and backslash have path semantics on Windows")
	}
	root := t.TempDir()
	tests := []string{
		`C:upload.txt`,
		`\name`,
		`nested\..\upload.txt`,
	}
	for _, relative := range tests {
		t.Run(relative, func(t *testing.T) {
			want := filepath.Join(root, relative)
			mustWriteBrowserPath(t, want, "upload")
			got, err := resolveBrowserPath(root, relative, browserUploadFile)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("resolved path = %q, want %q", got, want)
			}
		})
	}
}

func TestBrowserPathLexicalPolicyUsesTargetOS(t *testing.T) {
	windowsTraversal := []string{
		`nested\..\upload.txt`,
		`nested/..\upload.txt`,
		`nested\../upload.txt`,
	}
	for _, path := range windowsTraversal {
		if !hasBrowserParentComponentForOS(path, "windows") {
			t.Errorf("Windows path %q did not detect parent traversal", path)
		}
	}
	windowsRooted := []string{
		`C:upload.txt`,
		`C:\upload.txt`,
		`\\server\share\upload.txt`,
		`\\?\C:\upload.txt`,
		`\\.\PhysicalDrive0`,
		`\Windows\upload.txt`,
	}
	for _, path := range windowsRooted {
		if !isBrowserRootedForOS(path, "windows") {
			t.Errorf("Windows path %q was not classified as rooted or volume-qualified", path)
		}
	}

	for _, path := range []string{`C:upload.txt`, `\name`} {
		if isBrowserRootedForOS(path, "linux") {
			t.Errorf("POSIX path %q was classified using Windows volume rules", path)
		}
	}
	if hasBrowserParentComponentForOS(`nested\..\upload.txt`, "linux") {
		t.Error("POSIX backslash filename was classified using Windows separator rules")
	}
	for _, value := range []string{`C:download`, `\name`, `nested\download`, `name/`, `name/.`} {
		if !isNormalBrowserPathComponentForOS(value, "linux") {
			t.Errorf("POSIX value %q was not classified as one Normal component", value)
		}
	}
	for _, value := range []string{`nested\download`, `C:download`, `.\download`, `download\child`} {
		if isNormalBrowserPathComponentForOS(value, "windows") {
			t.Errorf("Windows value %q was classified as one Normal component", value)
		}
	}
	if !isNormalBrowserPathComponentForOS("opaque-download", "windows") {
		t.Error("Windows normal filename was rejected")
	}
}

func configurePreparedFileServer(server *cuaBrowserServer, root string, action string) {
	server.cfg.WorkingDir = root
	server.refs["@e1"] = cuaElement{Raw: "raw-file-capability", Actions: map[string]bool{action: true}}
}

func TestCUABrowserFilesAndDownloadToolContracts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- startCUABrowserMCPServer(
			ctx,
			serverTransport,
			types.Config{WorkingDir: t.TempDir()},
			&cuaRuntime{sessionID: testCUASessionID},
		)
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "file-browser-contract-test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	wantProperties := map[string][]string{
		"browser_set_input_files": {"files", "ref"},
		"browser_download":        {"directory", "ref"},
	}
	found := map[string]bool{}
	for _, tool := range listed.Tools {
		want, ok := wantProperties[tool.Name]
		if !ok {
			continue
		}
		found[tool.Name] = true
		properties := browserToolSchemaProperties(t, tool)
		got := make([]string, 0, len(properties))
		for name := range properties {
			got = append(got, name)
		}
		slices.Sort(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s input properties = %v, want %v", tool.Name, got, want)
		}
	}
	if !found["browser_set_input_files"] || !found["browser_download"] {
		t.Fatalf("Cua file tools found = %#v, want both upload and download", found)
	}

	cancel()
	select {
	case err := <-serverErr:
		if err != nil && !errorsIsContextCancellation(err) {
			t.Fatalf("Cua browser server shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Cua browser server did not stop")
	}
}

func TestCUABrowserFilesRejectInvalidInputBeforeRefTranslationOrCUA(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	tooMany := make([]string, 33)
	for index := range tooMany {
		tooMany[index] = "upload.txt"
	}
	tests := []struct {
		name string
		in   BrowserSetInputFilesInput
	}{
		{name: "no files", in: BrowserSetInputFilesInput{Ref: "@e1"}},
		{name: "too many files", in: BrowserSetInputFilesInput{Ref: "@e1", Files: tooMany}},
		{name: "empty file path", in: BrowserSetInputFilesInput{Ref: "@e404", Files: []string{""}}},
		{name: "absolute file", in: BrowserSetInputFilesInput{Ref: "@e404", Files: []string{fixture.upload}}},
		{name: "traversing file", in: BrowserSetInputFilesInput{Ref: "@e404", Files: []string{"nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "upload.txt"}}},
		{name: "missing file", in: BrowserSetInputFilesInput{Ref: "@e404", Files: []string{"missing.txt"}}},
		{name: "directory", in: BrowserSetInputFilesInput{Ref: "@e404", Files: []string{"nested"}}},
		{name: "symlink", in: BrowserSetInputFilesInput{Ref: "@e404", Files: []string{"upload-link"}}},
		{name: "one invalid among valid", in: BrowserSetInputFilesInput{Ref: "@e404", Files: []string{"upload.txt", "outside-file-link"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, nil)
			configurePreparedFileServer(server, fixture.root, "upload")
			_, _, err := server.browserSetInputFiles(context.Background(), nil, test.in)
			assertBrowserErrorHasNoPaths(t, err, fixture.root, fixture.canonicalRoot, fixture.outside)
			if strings.Contains(err.Error(), "@e404") {
				t.Fatalf("path validation translated the ref first: %v", err)
			}
			if calls := fake.Calls(); len(calls) != 0 {
				t.Fatalf("invalid upload called Cua: %#v", calls)
			}
		})
	}
}

func TestCUABrowserFilesRequireCurrentUploadCapability(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	tests := []struct {
		name string
		ref  string
	}{
		{name: "unknown ref", ref: "@e404"},
		{name: "wrong action", ref: "@e1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, nil)
			server.cfg.WorkingDir = fixture.root
			server.refs["@e1"] = cuaElement{Raw: "raw-file-capability", Actions: map[string]bool{"click": true}}
			_, _, err := server.browserSetInputFiles(context.Background(), nil, BrowserSetInputFilesInput{
				Ref: test.ref, Files: []string{"upload.txt"},
			})
			if err == nil {
				t.Fatal("upload without a current upload capability succeeded")
			}
			if calls := fake.Calls(); len(calls) != 0 {
				t.Fatalf("incapable upload ref called Cua: %#v", calls)
			}
		})
	}
}

func TestCUABrowserFilesPassCanonicalPathsAndMapFileCount(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_set_input_files": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "ref": "raw-file-capability", "file_count": 2,
			})
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserSnapshot(
				"target-1", "tab-a", "files assigned "+fixture.root+" raw-file-capability upload.txt nested/second.bin second.bin",
				cuaBrowserRef("raw-fresh", "Fresh", "click"),
			))
		},
	})
	configurePreparedFileServer(server, fixture.root, "upload")

	result, output, err := server.browserSetInputFiles(context.Background(), nil, BrowserSetInputFilesInput{
		Ref: "@e1", Files: []string{"upload.txt", filepath.Join("nested", "second.bin")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "ok" || output.AssignedCount != 2 || output.ElementCount != 1 ||
		!strings.Contains(output.Snapshot, "files assigned") {
		t.Fatalf("upload output = %#v", output)
	}
	if resultText(result) != output.Snapshot {
		t.Fatalf("upload text result = %q, want fresh snapshot %q", resultText(result), output.Snapshot)
	}
	for _, secret := range []string{
		fixture.root, fixture.canonicalRoot, "raw-file-capability", "upload.txt", "nested/second.bin", "second.bin",
	} {
		if strings.Contains(output.Snapshot, secret) {
			t.Fatalf("upload snapshot leaked %q: %s", secret, output.Snapshot)
		}
	}
	wantArgs := exactTestArgs(map[string]any{
		"ref": "raw-file-capability", "files": []any{fixture.upload, fixture.nestedUpload},
	})
	calls := callsNamed(fake.Calls(), "browser_set_input_files")
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, wantArgs) {
		t.Fatalf("browser_set_input_files calls = %#v, want exactly %#v", calls, wantArgs)
	}
	if _, privateApproval := calls[0].Args["_cua_browser_download_mcp_host_approved"]; privateApproval {
		t.Fatalf("upload args injected Cua private approval marker: %#v", calls[0].Args)
	}
}

func TestCUABrowserFilesRejectMismatchedSuccessAndInvalidateCapabilities(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	tests := []struct {
		name     string
		response map[string]any
	}{
		{name: "wrong count", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "ref": "raw-file-capability", "file_count": 2}},
		{name: "wrong target", response: map[string]any{"target_id": "other-target", "tab_id": "tab-a", "ref": "raw-file-capability", "file_count": 1}},
		{name: "wrong tab", response: map[string]any{"target_id": "target-1", "tab_id": "other-tab", "ref": "raw-file-capability", "file_count": 1}},
		{name: "wrong ref", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "ref": "other-ref", "file_count": 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_set_input_files": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaOK(test.response)
				},
			})
			configurePreparedFileServer(server, fixture.root, "upload")
			_, _, err := server.browserSetInputFiles(context.Background(), nil, BrowserSetInputFilesInput{
				Ref: "@e1", Files: []string{"upload.txt"},
			})
			if err == nil {
				t.Fatal("mismatched upload success was accepted")
			}
			assertBrowserErrorHasNoPaths(t, err, fixture.root, fixture.canonicalRoot)
			if got := countFakeCUACalls(fake.Calls(), "browser_set_input_files"); got != 1 {
				t.Fatalf("browser_set_input_files calls = %d, want 1", got)
			}
			if len(server.refs) != 0 {
				t.Fatalf("uncertain upload retained element capabilities: %#v", server.refs)
			}
		})
	}
}

func TestCUABrowserDownloadRejectsInvalidDestinationBeforeRefTranslationOrCUA(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	tests := []struct {
		name      string
		directory string
	}{
		{name: "absolute", directory: fixture.downloads},
		{name: "traversal", directory: "nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "downloads"},
		{name: "missing", directory: "missing-dir"},
		{name: "file", directory: "upload.txt"},
		{name: "internal symlink", directory: "nested-link"},
		{name: "external symlink", directory: "outside-dir-link"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, nil)
			configurePreparedFileServer(server, fixture.root, "click")
			_, _, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{
				Ref: "@e404", Directory: test.directory,
			})
			assertBrowserErrorHasNoPaths(t, err, fixture.root, fixture.canonicalRoot, fixture.outside)
			if strings.Contains(err.Error(), "@e404") {
				t.Fatalf("destination validation translated the ref first: %v", err)
			}
			if calls := fake.Calls(); len(calls) != 0 {
				t.Fatalf("invalid download destination called Cua: %#v", calls)
			}
		})
	}
}

func TestCUABrowserDownloadRequiresCurrentClickCapability(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	server, fake := preparedCUABrowserTestServer(t, nil)
	server.cfg.WorkingDir = fixture.root
	server.refs["@e1"] = cuaElement{Raw: "raw-file-capability", Actions: map[string]bool{"upload": true}}
	_, _, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{Ref: "@e1"})
	if err == nil {
		t.Fatal("download without a current click capability succeeded")
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("incapable download ref called Cua: %#v", calls)
	}
}

func TestCUABrowserDownloadDefaultsCanonicalDestinationAndReturnsPathFreeOutput(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	const downloadID = "opaque-download-7"
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_download": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return &mcp.CallToolResult{}, map[string]any{
				"status": "completed", "download_id": downloadID, "bytes": int64(4096),
				"path":     filepath.Join(fixture.root, "private-name.pdf"),
				"filename": "private-name.pdf", "url": "https://secret.example/private.pdf",
			}, nil
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserSnapshot(
				"target-1", "tab-a", "download complete "+fixture.root+" raw-file-capability",
			))
		},
	})
	configurePreparedFileServer(server, fixture.root, "click")

	result, output, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{Ref: "@e1"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "ok" || output.DownloadID != downloadID || output.Bytes != 4096 ||
		!strings.Contains(output.Snapshot, "download complete") {
		t.Fatalf("download output = %#v", output)
	}
	wantArgs := exactTestArgs(map[string]any{
		"ref": "raw-file-capability", "destination_root": fixture.canonicalRoot,
	})
	calls := callsNamed(fake.Calls(), "browser_download")
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, wantArgs) {
		t.Fatalf("browser_download calls = %#v, want exactly %#v", calls, wantArgs)
	}
	if _, privateApproval := calls[0].Args["_cua_browser_download_mcp_host_approved"]; privateApproval {
		t.Fatalf("download args injected Cua private approval marker: %#v", calls[0].Args)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	public := string(encoded) + resultText(result)
	for _, secret := range []string{
		fixture.root, fixture.canonicalRoot, "private-name.pdf", "secret.example", "raw-file-capability",
	} {
		if strings.Contains(public, secret) {
			t.Fatalf("download result leaked %q: %s", secret, public)
		}
	}
}

func TestCUABrowserDownloadUsesExistingCanonicalSubdirectory(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_download": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return &mcp.CallToolResult{}, map[string]any{
				"status": "completed", "download_id": "opaque-download", "bytes": int64(0),
			}, nil
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "empty downloads complete"))
		},
	})
	configurePreparedFileServer(server, fixture.root, "click")
	_, output, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{
		Ref: "@e1", Directory: "downloads",
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.Bytes != 0 || output.DownloadID == "" {
		t.Fatalf("zero-byte download output = %#v", output)
	}
	if strings.Contains(output.Snapshot, "downloads") {
		t.Fatalf("download snapshot leaked destination basename: %s", output.Snapshot)
	}
	calls := callsNamed(fake.Calls(), "browser_download")
	if len(calls) != 1 || calls[0].Args["destination_root"] != fixture.downloads {
		t.Fatalf("browser_download destination = %#v, want %q", calls, fixture.downloads)
	}
}

func TestCUABrowserDownloadRejectsMalformedCompletedResultAndInvalidatesCapabilities(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	tests := []struct {
		name     string
		response map[string]any
	}{
		{name: "missing id", response: map[string]any{"status": "completed", "bytes": int64(4)}},
		{name: "missing bytes", response: map[string]any{"status": "completed", "download_id": "opaque"}},
		{name: "negative bytes", response: map[string]any{"status": "completed", "download_id": "opaque", "bytes": int64(-1)}},
		{name: "wrong status", response: map[string]any{"status": "ok", "download_id": "opaque", "bytes": int64(4)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_download": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return &mcp.CallToolResult{}, test.response, nil
				},
			})
			configurePreparedFileServer(server, fixture.root, "click")
			_, _, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{Ref: "@e1"})
			if err == nil {
				t.Fatal("malformed completed download was accepted")
			}
			assertBrowserErrorHasNoPaths(t, err, fixture.root, fixture.canonicalRoot)
			if got := countFakeCUACalls(fake.Calls(), "browser_download"); got != 1 {
				t.Fatalf("browser_download calls = %d, want 1", got)
			}
			if got := countFakeCUACalls(fake.Calls(), "get_browser_state"); got != 0 {
				t.Fatalf("malformed download triggered verification snapshot: %d", got)
			}
			if len(server.refs) != 0 {
				t.Fatalf("uncertain download retained element capabilities: %#v", server.refs)
			}
		})
	}
}

func TestCUABrowserDownloadRejectsUnsafeOpaqueIDsBeforeSnapshot(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	type unsafeIDTest struct {
		name string
		id   string
	}
	tests := []unsafeIDTest{
		{name: "dot", id: "."},
		{name: "parent", id: ".."},
		{name: "slash component", id: "nested/download"},
		{name: "target capability", id: "opaque-target-1-value"},
		{name: "tab capability", id: "opaque-tab-a-value"},
		{name: "element capability", id: "opaque-raw-file-capability-value"},
		{name: "session capability", id: "opaque-" + testCUASessionID + "-value"},
		{name: "private root", id: fixture.canonicalRoot},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests,
			unsafeIDTest{name: "backslash component", id: `nested\download`},
			unsafeIDTest{name: "drive relative component", id: `C:download`},
		)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_download": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return &mcp.CallToolResult{}, map[string]any{
						"status": "completed", "download_id": test.id, "bytes": int64(4),
					}, nil
				},
			})
			configurePreparedFileServer(server, fixture.root, "click")
			_, _, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{Ref: "@e1"})
			if err == nil {
				t.Fatalf("unsafe download id %q was accepted", test.id)
			}
			assertBrowserErrorHasNoPaths(t, err, fixture.root, fixture.canonicalRoot, test.id)
			if got := countFakeCUACalls(fake.Calls(), "browser_download"); got != 1 {
				t.Fatalf("browser_download calls = %d, want 1", got)
			}
			if got := countFakeCUACalls(fake.Calls(), "get_browser_state"); got != 0 {
				t.Fatalf("unsafe download id triggered verification snapshot: %d", got)
			}
			if len(server.refs) != 0 {
				t.Fatalf("unsafe download id retained element capabilities: %#v", server.refs)
			}
		})
	}
}

func TestCUABrowserDownloadAcceptsPOSIXNormalOpaqueIDs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("colon and backslash have path semantics on Windows")
	}
	fixture := newBrowserPathFixture(t)
	for _, downloadID := range []string{`C:download`, `\name`, `nested\download`} {
		t.Run(downloadID, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_download": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return &mcp.CallToolResult{}, map[string]any{
						"status": "completed", "download_id": downloadID, "bytes": int64(4),
					}, nil
				},
				"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "download complete"))
				},
			})
			configurePreparedFileServer(server, fixture.root, "click")
			_, output, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{Ref: "@e1"})
			if err != nil {
				t.Fatal(err)
			}
			if output.DownloadID != downloadID || output.Bytes != 4 || output.Status != "ok" {
				t.Fatalf("download output = %#v", output)
			}
			if got := countFakeCUACalls(fake.Calls(), "get_browser_state"); got != 1 {
				t.Fatalf("verification snapshot calls = %d, want 1", got)
			}
		})
	}
}

func TestCUABrowserDownloadRejectsKnownContinuationInOpaqueID(t *testing.T) {
	server, _ := preparedCUABrowserTestServer(t, nil)
	const continuation = "bc-private-continuation"
	downloadID := "opaque-" + continuation + "-value"
	byteCount := int64(4)
	publicArgs := map[string]any{
		"continuation": continuation,
	}
	_, _, err := server.validateBrowserDownloadResult(cuaDownloadResult{
		DownloadID: &downloadID,
		Bytes:      &byteCount,
	}, publicArgs)
	if err == nil {
		t.Fatal("download id containing a known continuation capability was accepted")
	}
	if strings.Contains(err.Error(), continuation) {
		t.Fatalf("download id validation leaked continuation capability: %v", err)
	}
}

func TestCUABrowserFilesValidatePathsUnderActionSerialization(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	tests := []struct {
		name string
		call func(*cuaBrowserServer) error
	}{
		{
			name: "upload",
			call: func(server *cuaBrowserServer) error {
				_, _, err := server.browserSetInputFiles(context.Background(), nil, BrowserSetInputFilesInput{
					Ref: "@e1", Files: []string{fixture.upload},
				})
				return err
			},
		},
		{
			name: "download",
			call: func(server *cuaBrowserServer) error {
				_, _, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{
					Ref: "@e1", Directory: fixture.downloads,
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, nil)
			configurePreparedFileServer(server, fixture.root, "click")
			if test.name == "upload" {
				configurePreparedFileServer(server, fixture.root, "upload")
			}

			server.actionMu.Lock()
			started := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				close(started)
				done <- test.call(server)
			}()
			<-started

			select {
			case err := <-done:
				server.actionMu.Unlock()
				t.Fatalf("path validation completed outside action serialization: %v", err)
			case <-time.After(75 * time.Millisecond):
				server.actionMu.Unlock()
			}
			err := <-done
			assertBrowserErrorHasNoPaths(t, err, fixture.root, fixture.canonicalRoot)
			if calls := fake.Calls(); len(calls) != 0 {
				t.Fatalf("invalid path called Cua: %#v", calls)
			}
		})
	}
}

func TestCUABrowserFilesAndDownloadDoNotRetryOrLeakAfterSnapshotFailure(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	tests := []struct {
		name         string
		action       string
		mutationTool string
		call         func(*cuaBrowserServer) (string, error)
		response     map[string]any
	}{
		{
			name: "upload", action: "upload", mutationTool: "browser_set_input_files",
			response: map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "ref": "raw-file-capability", "file_count": 1,
			},
			call: func(server *cuaBrowserServer) (string, error) {
				result, output, err := server.browserSetInputFiles(context.Background(), nil, BrowserSetInputFilesInput{
					Ref: "@e1", Files: []string{"upload.txt"},
				})
				return fmt.Sprintf("%s %#v", resultText(result), output), err
			},
		},
		{
			name: "download", action: "click", mutationTool: "browser_download",
			response: map[string]any{"status": "completed", "download_id": "opaque-download", "bytes": int64(7)},
			call: func(server *cuaBrowserServer) (string, error) {
				result, output, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{Ref: "@e1"})
				return fmt.Sprintf("%s %#v", resultText(result), output), err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handlers := map[string]fakeCUAHandler{
				test.mutationTool: func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					if test.mutationTool == "browser_download" {
						return &mcp.CallToolResult{}, test.response, nil
					}
					return cuaOK(test.response)
				},
				"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return nil, nil, errors.New("snapshot failed at " + fixture.upload + " for raw-file-capability")
				},
			}
			server, fake := preparedCUABrowserTestServer(t, handlers)
			configurePreparedFileServer(server, fixture.root, test.action)
			public, err := test.call(server)
			if err == nil || !strings.Contains(err.Error(), "post-action snapshot failed") {
				t.Fatalf("post-action error = %v", err)
			}
			public += " " + err.Error()
			for _, secret := range []string{fixture.root, fixture.canonicalRoot, fixture.upload, "raw-file-capability"} {
				if strings.Contains(public, secret) {
					t.Fatalf("partial failure leaked %q: %s", secret, public)
				}
			}
			if got := countFakeCUACalls(fake.Calls(), test.mutationTool); got != 1 {
				t.Fatalf("%s calls = %d, want exactly 1", test.mutationTool, got)
			}
			if got := countFakeCUACalls(fake.Calls(), "get_browser_state"); got != 1 {
				t.Fatalf("verification snapshot calls = %d, want exactly 1", got)
			}
			if len(server.refs) != 0 {
				t.Fatalf("partial failure retained element capabilities: %#v", server.refs)
			}
		})
	}
}

func TestCUABrowserFilesAndDownloadRefusalsAreSingleCallAndPathFree(t *testing.T) {
	fixture := newBrowserPathFixture(t)
	tests := []struct {
		name         string
		action       string
		mutationTool string
		privateName  string
		call         func(*cuaBrowserServer) (string, BrowserOutcome, error)
	}{
		{
			name: "upload", action: "upload", mutationTool: "browser_set_input_files", privateName: "upload.txt",
			call: func(server *cuaBrowserServer) (string, BrowserOutcome, error) {
				result, output, err := server.browserSetInputFiles(context.Background(), nil, BrowserSetInputFilesInput{
					Ref: "@e1", Files: []string{"upload.txt"},
				})
				return resultText(result), output.BrowserOutcome, err
			},
		},
		{
			name: "download", action: "click", mutationTool: "browser_download", privateName: "downloads",
			call: func(server *cuaBrowserServer) (string, BrowserOutcome, error) {
				result, output, err := server.browserDownload(context.Background(), nil, BrowserDownloadInput{Ref: "@e1", Directory: "downloads"})
				return resultText(result), output.BrowserOutcome, err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				test.mutationTool: func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaRefused("browser_ref_stale", "ref raw-file-capability denied "+fixture.upload+" "+test.privateName, map[string]any{
						"path": fixture.upload, "safe_reason": "raw-file-capability under " + fixture.root + " " + test.privateName,
					})
				},
			})
			configurePreparedFileServer(server, fixture.root, test.action)
			text, outcome, err := test.call(server)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Status != "refused" || outcome.Refusal == nil || outcome.Refusal.Code != "browser_ref_stale" {
				t.Fatalf("refusal outcome = %#v", outcome)
			}
			encoded, err := json.Marshal(outcome)
			if err != nil {
				t.Fatal(err)
			}
			public := text + string(encoded)
			for _, secret := range []string{fixture.root, fixture.canonicalRoot, fixture.upload, "raw-file-capability", test.privateName} {
				if strings.Contains(public, secret) {
					t.Fatalf("refusal leaked %q: %s", secret, public)
				}
			}
			if got := countFakeCUACalls(fake.Calls(), test.mutationTool); got != 1 {
				t.Fatalf("%s calls = %d, want exactly 1", test.mutationTool, got)
			}
			if got := countFakeCUACalls(fake.Calls(), "get_browser_state"); got != 0 {
				t.Fatalf("refusal triggered verification calls: %d", got)
			}
			if len(server.refs) != 0 {
				t.Fatalf("stale-ref refusal retained element capabilities: %#v", server.refs)
			}
		})
	}
}

func resultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var text strings.Builder
	for _, content := range result.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text.WriteString(item.Text)
		}
	}
	return text.String()
}
