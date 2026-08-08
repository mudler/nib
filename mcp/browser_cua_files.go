package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxBrowserUploadFiles = 32

	browserSetInputFilesDescription = "Assign one to 32 existing regular files from WorkingDir to a current file-input ref " +
		"and return a fresh semantic snapshot."
	browserDownloadDescription = "Activate a current downloadable ref and save it in an existing directory under WorkingDir, " +
		"returning only an opaque download id, byte count, and fresh semantic snapshot."
)

type browserPathKind int

const (
	browserUploadFile browserPathKind = iota
	browserDownloadDir
)

type BrowserSetInputFilesInput struct {
	Ref   string   `json:"ref"`
	Files []string `json:"files"`
}

type BrowserSetInputFilesOutput struct {
	BrowserOutcome
	AssignedCount int    `json:"assigned_count,omitempty"`
	Snapshot      string `json:"snapshot,omitempty"`
	ElementCount  int    `json:"element_count,omitempty"`
}

type BrowserDownloadInput struct {
	Ref       string `json:"ref"`
	Directory string `json:"directory,omitempty"`
}

type BrowserDownloadOutput struct {
	BrowserOutcome
	DownloadID   string `json:"download_id,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	Snapshot     string `json:"snapshot,omitempty"`
	ElementCount int    `json:"element_count,omitempty"`
}

type cuaSetInputFilesResult struct {
	Status    string `json:"status"`
	TargetID  string `json:"target_id"`
	TabID     string `json:"tab_id"`
	Ref       string `json:"ref"`
	FileCount int    `json:"file_count"`
}

type cuaDownloadResult struct {
	Status     string `json:"status"`
	DownloadID string `json:"download_id"`
	Bytes      int64  `json:"bytes"`
}

type setInputFilesCall struct {
	args        map[string]any
	privateRoot string
	rawRef      string
	fileCount   int
}

func resolveBrowserPath(root, relative string, kind browserPathKind) (string, error) {
	if kind != browserUploadFile && kind != browserDownloadDir {
		return "", errors.New("browser path has an unsupported required kind")
	}
	if kind == browserUploadFile && relative == "" {
		return "", errors.New("browser upload path must not be empty")
	}
	if filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return "", errors.New("browser file paths must be relative to WorkingDir")
	}
	if hasBrowserParentComponent(relative) {
		return "", errors.New("browser file paths must not contain parent traversal")
	}

	canonicalRoot, err := canonicalBrowserRoot(root)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(relative)
	if relative == "" {
		cleaned = "."
	}
	candidate := filepath.Join(canonicalRoot, cleaned)
	if !browserPathContained(canonicalRoot, candidate) {
		return "", errors.New("browser file path escapes WorkingDir")
	}

	info, err := lstatBrowserPathComponents(canonicalRoot, cleaned)
	if err != nil {
		return "", err
	}
	if err := requireBrowserPathKind(info, kind); err != nil {
		return "", err
	}

	canonical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", errors.New("browser file path could not be canonicalized")
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", errors.New("browser file path could not be made absolute")
	}
	if !browserPathContained(canonicalRoot, canonical) {
		return "", errors.New("browser file path escapes WorkingDir")
	}

	finalInfo, err := os.Lstat(canonical)
	if err != nil {
		return "", errors.New("browser file path changed during validation")
	}
	if finalInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, finalInfo) {
		return "", errors.New("browser file path changed during validation")
	}
	if err := requireBrowserPathKind(finalInfo, kind); err != nil {
		return "", err
	}
	return canonical, nil
}

func canonicalBrowserRoot(root string) (string, error) {
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", errors.New("WorkingDir could not be resolved")
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", errors.New("WorkingDir could not be made absolute")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", errors.New("WorkingDir must be an existing directory")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", errors.New("WorkingDir must be an existing directory")
	}
	return canonical, nil
}

func hasBrowserParentComponent(path string) bool {
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".." {
			return true
		}
	}
	return false
}

func browserPathContained(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func lstatBrowserPathComponents(root, relative string) (os.FileInfo, error) {
	current := root
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, errors.New("WorkingDir must be an existing directory")
	}
	if relative == "." {
		return rootInfo, nil
	}
	var info os.FileInfo
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err = os.Lstat(current)
		if err != nil {
			return nil, errors.New("browser file path must already exist")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("browser file paths must not contain symlinks")
		}
	}
	if info == nil {
		return rootInfo, nil
	}
	return info, nil
}

func requireBrowserPathKind(info os.FileInfo, kind browserPathKind) error {
	switch kind {
	case browserUploadFile:
		if !info.Mode().IsRegular() {
			return errors.New("browser upload path must be an existing regular file")
		}
	case browserDownloadDir:
		if !info.IsDir() {
			return errors.New("browser download destination must be an existing directory")
		}
	default:
		return errors.New("browser path has an unsupported required kind")
	}
	return nil
}

func (b *cuaBrowserServer) browserSetInputFiles(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserSetInputFilesInput,
) (*mcp.CallToolResult, BrowserSetInputFilesOutput, error) {
	if len(in.Files) == 0 || len(in.Files) > maxBrowserUploadFiles {
		return nil, BrowserSetInputFilesOutput{}, fmt.Errorf(
			"browser_set_input_files requires 1 to %d files",
			maxBrowserUploadFiles,
		)
	}
	files := make([]string, len(in.Files))
	privateRoot, err := canonicalBrowserRoot(b.cfg.WorkingDir)
	if err != nil {
		return nil, BrowserSetInputFilesOutput{}, err
	}
	for index, relative := range in.Files {
		resolved, err := resolveBrowserPath(privateRoot, relative, browserUploadFile)
		if err != nil {
			return nil, BrowserSetInputFilesOutput{}, err
		}
		files[index] = resolved
	}

	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserSetInputFilesOutput{}, err
	}
	element, err := b.actionableRef(in.Ref, "upload")
	if err != nil {
		return nil, BrowserSetInputFilesOutput{}, err
	}
	args, err := b.exactArgs(map[string]any{"ref": element.Raw, "files": files})
	if err != nil {
		return nil, BrowserSetInputFilesOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	return b.setInputFilesThenSnapshot(actionCtx, setInputFilesCall{
		args:        args,
		privateRoot: privateRoot,
		rawRef:      element.Raw,
		fileCount:   len(files),
	})
}

func (b *cuaBrowserServer) setInputFilesThenSnapshot(
	ctx context.Context,
	call setInputFilesCall,
) (*mcp.CallToolResult, BrowserSetInputFilesOutput, error) {
	publicArgs := browserFileSanitizerArgs(call.args, call.privateRoot)
	var mutation cuaSetInputFilesResult
	_, refusal, err := b.callResult(ctx, "browser_set_input_files", call.args, &mutation)
	if err != nil {
		b.clearTabScopedCapabilities()
		return nil, BrowserSetInputFilesOutput{}, b.publicCallError(err, publicArgs)
	}
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
		return b.setInputFilesRefusalResult(refusal, publicArgs)
	}

	b.clearTabScopedCapabilities()
	if err := b.validateSetInputFilesResult(mutation, call.rawRef, call.fileCount); err != nil {
		return nil, BrowserSetInputFilesOutput{}, b.publicCallError(err, publicArgs)
	}
	snapshotResult, snapshotOutput, _, err := b.snapshotInternal(ctx, false, false, false)
	if err != nil {
		public := b.publicCallError(err, publicArgs)
		return nil, BrowserSetInputFilesOutput{}, fmt.Errorf(
			"file assignment succeeded but post-action snapshot failed: %w",
			public,
		)
	}
	if snapshotOutput.Refusal != nil {
		b.mu.Lock()
		snapshotOutput.Refusal = b.aliasStateLocked().resanitizePublicRefusal(snapshotOutput.Refusal, publicArgs)
		b.mu.Unlock()
		snapshotResult = textResult(snapshotOutput.Refusal.Message)
	} else if snapshotOutput.Status == "ok" {
		var sanitizeErr error
		snapshotOutput.Snapshot, sanitizeErr = b.sanitizeBrowserFileSnapshot(snapshotOutput.Snapshot, publicArgs)
		if sanitizeErr != nil {
			b.clearTabScopedCapabilities()
			return nil, BrowserSetInputFilesOutput{}, sanitizeErr
		}
	}
	output := BrowserSetInputFilesOutput{
		BrowserOutcome: snapshotOutput.BrowserOutcome,
		AssignedCount:  mutation.FileCount,
		Snapshot:       snapshotOutput.Snapshot,
		ElementCount:   snapshotOutput.ElementCount,
	}
	if output.Status == "ok" {
		snapshotResult = textResult(output.Snapshot)
	}
	return snapshotResult, output, nil
}

func (b *cuaBrowserServer) validateSetInputFilesResult(
	result cuaSetInputFilesResult,
	rawRef string,
	fileCount int,
) error {
	b.mu.Lock()
	targetID, tabID := b.targetID, b.selectedTab
	b.mu.Unlock()
	if result.TargetID == "" || result.TabID == "" || result.Ref == "" {
		return errors.New("Cua browser_set_input_files success omitted exact target, tab, or ref")
	}
	if result.TargetID != targetID {
		b.observeTargetGeneration(result.TargetID)
		return errors.New("Cua browser_set_input_files returned a different browser target")
	}
	if result.TabID != tabID {
		return errors.New("Cua browser_set_input_files returned a different browser tab")
	}
	if result.Ref != rawRef {
		return errors.New("Cua browser_set_input_files returned a different file-input ref")
	}
	if result.FileCount != fileCount {
		return errors.New("Cua browser_set_input_files returned a different assigned file count")
	}
	return nil
}

func (b *cuaBrowserServer) setInputFilesRefusalResult(
	refusal *cuaRefusal,
	args map[string]any,
) (*mcp.CallToolResult, BrowserSetInputFilesOutput, error) {
	b.mu.Lock()
	public := b.aliasStateLocked().publicRefusal(refusal, args)
	b.mu.Unlock()
	output := BrowserSetInputFilesOutput{
		BrowserOutcome: BrowserOutcome{Status: "refused", Refusal: public},
	}
	return textResult(public.Message), output, nil
}

func (b *cuaBrowserServer) browserDownload(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserDownloadInput,
) (*mcp.CallToolResult, BrowserDownloadOutput, error) {
	privateRoot, err := canonicalBrowserRoot(b.cfg.WorkingDir)
	if err != nil {
		return nil, BrowserDownloadOutput{}, err
	}
	destination, err := resolveBrowserPath(privateRoot, in.Directory, browserDownloadDir)
	if err != nil {
		return nil, BrowserDownloadOutput{}, err
	}

	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserDownloadOutput{}, err
	}
	element, err := b.actionableRef(in.Ref, "click")
	if err != nil {
		return nil, BrowserDownloadOutput{}, err
	}
	args, err := b.exactArgs(map[string]any{
		"ref": element.Raw, "destination_root": destination,
	})
	if err != nil {
		return nil, BrowserDownloadOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	return b.downloadThenSnapshot(actionCtx, args, privateRoot)
}

func (b *cuaBrowserServer) downloadThenSnapshot(
	ctx context.Context,
	args map[string]any,
	privateRoot string,
) (*mcp.CallToolResult, BrowserDownloadOutput, error) {
	publicArgs := browserFileSanitizerArgs(args, privateRoot)
	mutation, refusal, err := b.callDownloadResult(ctx, args)
	if err != nil {
		b.clearTabScopedCapabilities()
		return nil, BrowserDownloadOutput{}, b.publicCallError(err, publicArgs)
	}
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
		return b.downloadRefusalResult(refusal, publicArgs)
	}

	b.clearTabScopedCapabilities()
	if mutation.DownloadID == "" {
		return nil, BrowserDownloadOutput{}, errors.New("Cua browser_download completion omitted download id")
	}
	if mutation.Bytes < 0 {
		return nil, BrowserDownloadOutput{}, errors.New("Cua browser_download completion returned a negative byte count")
	}
	snapshotResult, snapshotOutput, _, err := b.snapshotInternal(ctx, false, false, false)
	if err != nil {
		public := b.publicCallError(err, publicArgs)
		return nil, BrowserDownloadOutput{}, fmt.Errorf(
			"download succeeded but post-action snapshot failed: %w",
			public,
		)
	}
	if snapshotOutput.Refusal != nil {
		b.mu.Lock()
		snapshotOutput.Refusal = b.aliasStateLocked().resanitizePublicRefusal(snapshotOutput.Refusal, publicArgs)
		b.mu.Unlock()
		snapshotResult = textResult(snapshotOutput.Refusal.Message)
	} else if snapshotOutput.Status == "ok" {
		var sanitizeErr error
		snapshotOutput.Snapshot, sanitizeErr = b.sanitizeBrowserFileSnapshot(snapshotOutput.Snapshot, publicArgs)
		if sanitizeErr != nil {
			b.clearTabScopedCapabilities()
			return nil, BrowserDownloadOutput{}, sanitizeErr
		}
	}
	output := BrowserDownloadOutput{
		BrowserOutcome: snapshotOutput.BrowserOutcome,
		DownloadID:     mutation.DownloadID,
		Bytes:          mutation.Bytes,
		Snapshot:       snapshotOutput.Snapshot,
		ElementCount:   snapshotOutput.ElementCount,
	}
	if output.Status == "ok" {
		snapshotResult = textResult(output.Snapshot)
	}
	return snapshotResult, output, nil
}

func (b *cuaBrowserServer) callDownloadResult(
	ctx context.Context,
	args map[string]any,
) (cuaDownloadResult, *cuaRefusal, error) {
	if b.runtime == nil {
		return cuaDownloadResult{}, nil, errors.New("Cua browser backend requires a shared runtime")
	}
	result, err := b.runtime.CallTool(ctx, &mcp.CallToolParams{Name: "browser_download", Arguments: args})
	if err != nil {
		return cuaDownloadResult{}, nil, fmt.Errorf("Cua browser_download: %w", err)
	}
	return decodeCUADownloadResult(result)
}

func decodeCUADownloadResult(result *mcp.CallToolResult) (cuaDownloadResult, *cuaRefusal, error) {
	if result == nil {
		return cuaDownloadResult{}, nil, errors.New("Cua browser_download returned no tool result")
	}
	if result.IsError {
		return cuaDownloadResult{}, nil, errors.New("Cua browser_download reported a protocol error")
	}
	if result.StructuredContent == nil {
		return cuaDownloadResult{}, nil, errors.New("Cua browser_download omitted structured content")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return cuaDownloadResult{}, nil, errors.New("encode Cua browser_download structured content")
	}
	var header struct {
		Status  string      `json:"status"`
		Refusal *cuaRefusal `json:"refusal"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return cuaDownloadResult{}, nil, errCUAInvalidStatus
	}
	if header.Status == "refused" {
		if header.Refusal == nil || header.Refusal.Code == "" || header.Refusal.Message == "" {
			return cuaDownloadResult{}, nil, errors.New("Cua browser_download refusal omitted code or message")
		}
		return cuaDownloadResult{}, header.Refusal, nil
	}
	if header.Status != "completed" {
		return cuaDownloadResult{}, nil, errCUAInvalidStatus
	}
	var download cuaDownloadResult
	if err := json.Unmarshal(raw, &download); err != nil {
		return cuaDownloadResult{}, nil, errors.New("decode Cua browser_download structured content")
	}
	return download, nil, nil
}

func (b *cuaBrowserServer) downloadRefusalResult(
	refusal *cuaRefusal,
	args map[string]any,
) (*mcp.CallToolResult, BrowserDownloadOutput, error) {
	b.mu.Lock()
	public := b.aliasStateLocked().publicRefusal(refusal, args)
	b.mu.Unlock()
	output := BrowserDownloadOutput{
		BrowserOutcome: BrowserOutcome{Status: "refused", Refusal: public},
	}
	return textResult(public.Message), output, nil
}

func browserFileSanitizerArgs(args map[string]any, privateRoot string) map[string]any {
	publicArgs := make(map[string]any, len(args)+1)
	for key, value := range args {
		publicArgs[key] = value
	}
	publicArgs["path"] = privateRoot
	return publicArgs
}

func (b *cuaBrowserServer) sanitizeBrowserFileSnapshot(snapshot string, args map[string]any) (string, error) {
	b.mu.Lock()
	replacements, safe := b.aliasStateLocked().refusalReplacements(args)
	b.mu.Unlock()
	if !safe {
		return "", errors.New("Cua browser file action snapshot could not be sanitized")
	}
	return applyReplacements(snapshot, replacements), nil
}

func registerCUABrowserFileTools(server *mcp.Server, browser *cuaBrowserServer) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_set_input_files",
		Description: browserSetInputFilesDescription,
	}, browser.browserSetInputFiles)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_download",
		Description: browserDownloadDescription,
	}, browser.browserDownload)
}
