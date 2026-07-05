package chat

import (
	"path/filepath"

	"github.com/mudler/cogito"
)

// resolveWorkspacePath joins a relative path onto workingDir (absolute paths are
// used as-is, and an empty workingDir leaves the path unchanged), matching how
// host file tools scope reads.
func resolveWorkspacePath(workingDir, p string) string {
	if workingDir == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workingDir, p)
}

// ---- read_image ----
type readImageArgs struct {
	Path     string `json:"path" jsonschema:"path to the image file (relative to the workspace or absolute)"`
	Question string `json:"question,omitempty" jsonschema:"optional specific question about the image; omit for a general description"`
}
type readImageTool struct {
	describe func(path, question string) (string, error)
}

func (t *readImageTool) Run(args map[string]any) (string, any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "read_image error: 'path' is required", nil, nil
	}
	question, _ := args["question"].(string)
	out, err := t.describe(path, question)
	if err != nil {
		return "read_image failed: " + err.Error(), nil, nil
	}
	return out, nil, nil
}

func readImageToolDefinition(describe func(path, question string) (string, error)) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](&readImageTool{describe: describe}, readImageArgs{},
		"read_image",
		"Read an image file from the workspace and return a text description of it. Provide `question` to ask something specific about the image; omit it for a general description. Returns model-generated text, not the raw image.")
}

// ---- transcribe_audio ----
type transcribeAudioArgs struct {
	Path string `json:"path" jsonschema:"path to the audio file to transcribe (relative to the workspace or absolute)"`
}
type transcribeAudioTool struct {
	transcribe func(path string) (string, error)
}

func (t *transcribeAudioTool) Run(args map[string]any) (string, any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "transcribe_audio error: 'path' is required", nil, nil
	}
	out, err := t.transcribe(path)
	if err != nil {
		return "transcribe_audio failed: " + err.Error(), nil, nil
	}
	return out, nil, nil
}

func transcribeAudioToolDefinition(transcribe func(path string) (string, error)) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](&transcribeAudioTool{transcribe: transcribe}, transcribeAudioArgs{},
		"transcribe_audio",
		"Transcribe an audio file from the workspace to text. Returns the transcript.")
}

// ---- read_video ----
type readVideoArgs struct {
	Path     string `json:"path" jsonschema:"path to the video file (relative to the workspace or absolute)"`
	Question string `json:"question,omitempty" jsonschema:"optional specific question about the video; omit for a general description"`
}
type readVideoTool struct {
	describe func(path, question string) (string, error)
}

func (t *readVideoTool) Run(args map[string]any) (string, any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "read_video error: 'path' is required", nil, nil
	}
	question, _ := args["question"].(string)
	out, err := t.describe(path, question)
	if err != nil {
		return "read_video failed: " + err.Error(), nil, nil
	}
	return out, nil, nil
}

func readVideoToolDefinition(describe func(path, question string) (string, error)) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](&readVideoTool{describe: describe}, readVideoArgs{},
		"read_video",
		"Read a video file from the workspace and return a text description of it. Provide `question` to ask something specific; omit for a general description. Returns model-generated text.")
}
