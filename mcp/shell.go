package mcp

import (
	"context"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Input type for executing shell scripts
type executeCommandInput struct {
	Script  string `json:"script" jsonschema:"the shell script to execute"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"optional timeout in seconds (default: 30)"`
}

// Output type for script execution results
type executeCommandOutput struct {
	Script       string `json:"script" jsonschema:"the script that was executed"`
	Stdout       string `json:"stdout" jsonschema:"standard output from the script"`
	Stderr       string `json:"stderr" jsonschema:"standard error from the script"`
	ExitCode     int    `json:"exit_code" jsonschema:"exit code of the script (0 means success)"`
	Success      bool   `json:"success" jsonschema:"whether the script executed successfully"`
	Error        string `json:"error,omitempty" jsonschema:"error message if execution failed"`
	Backgrounded bool   `json:"backgrounded,omitempty" jsonschema:"true if the user backgrounded this command mid-run; it keeps running — read further output via bash_job_output"`
	JobID        string `json:"job_id,omitempty" jsonschema:"id of the background job when backgrounded"`
}

// getShellCommand returns the shell command to use, defaulting to "sh" if not set
func getShellCommand() string {
	shellCmd := os.Getenv("SHELL_CMD")
	if shellCmd == "" {
		shellCmd = "sh -c"
	}
	return shellCmd
}

// makeBashTool builds the `bash` tool handler. The command runs as a
// detachable foreground job under srvCtx (so it can outlive the turn if the
// user backgrounds it). The handler blocks until one of:
//   - the command finishes (return full output),
//   - the user backgrounds it via Ctrl+B (return immediately with a job_id; the
//     command keeps running and is readable via bash_job_output),
//   - the per-call/turn context is cancelled, e.g. Ctrl+C interrupt (kill it),
//   - the timeout elapses (kill it).
func makeBashTool(srvCtx context.Context, mgr *bgJobManager) func(context.Context, *mcp.CallToolRequest, executeCommandInput) (*mcp.CallToolResult, executeCommandOutput, error) {
	return func(callCtx context.Context, _ *mcp.CallToolRequest, input executeCommandInput) (
		*mcp.CallToolResult,
		executeCommandOutput,
		error,
	) {
		timeout := input.Timeout
		if timeout <= 0 {
			timeout = 30
		}

		// Parent the job on srvCtx, not callCtx: a backgrounded command must
		// survive this handler returning. Foreground cancellation (Ctrl+C) is
		// still honored via the callCtx.Done() case below.
		j := mgr.launch(srvCtx, input.Script, true)

		timer := time.NewTimer(time.Duration(timeout) * time.Second)
		defer timer.Stop()

		select {
		case <-j.doneCh:
			return nil, j.toOutput(input.Script), nil

		case <-j.detach:
			// User backgrounded it: return what we have so far plus the job id.
			out := j.toOutput(input.Script)
			out.Success = true
			out.Error = ""
			out.Backgrounded = true
			out.JobID = j.id
			return nil, out, nil

		case <-timer.C:
			j.cancel()
			<-j.doneCh
			out := j.toOutput(input.Script)
			out.Success = false
			if out.ExitCode == 0 {
				out.ExitCode = -1
			}
			if out.Error == "" {
				out.Error = "Command timed out"
			}
			return nil, out, nil

		case <-callCtx.Done():
			j.cancel()
			<-j.doneCh
			out := j.toOutput(input.Script)
			out.Success = false
			if out.ExitCode == 0 {
				out.ExitCode = -1
			}
			if out.Error == "" {
				out.Error = "Command cancelled"
			}
			return nil, out, nil
		}
	}
}

func startBashMCPServer(ctx context.Context, transport mcp.Transport, mgr *bgJobManager) error {
	// Create MCP server for shell command execution
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "shell",
		Version: "v1.0.0",
	}, nil)

	// The bash tool runs as a detachable foreground job: it blocks like a normal
	// command, but the user can background it mid-run with Ctrl+B.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash",
		Description: "Execute a shell script and return the output, exit code, and any errors. The shell command can be configured via SHELL_CMD environment variable (default: 'sh'). Long commands can be backgrounded by the user (Ctrl+B); for commands you know are long-running, prefer bash_background.",
	}, makeBashTool(ctx, mgr))

	// Background-shell tools: bash_background / bash_jobs / bash_job_output /
	// bash_job_kill. Jobs run under ctx (session lifetime), surviving turns.
	registerBackgroundShellTools(ctx, server, mgr)

	// Run the server
	if err := server.Run(ctx, transport); err != nil {
		return err
	}

	return nil
}
