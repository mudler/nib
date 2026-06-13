package voice

import "github.com/mudler/nib/types"

const voiceProfile = `
You are operating in VOICE mode: your replies are spoken aloud, not read.
- Keep replies short and conversational; avoid lists, code blocks, and markdown.
- Favor one or two sentences — you are heard, not read.
- For anything that takes more than a moment (builds, searches, multi-step work),
  spawn a background sub-agent or run the shell job in the background and give a
  brief spoken acknowledgement now; report the result when it completes.`

// applyProfile appends the voice-mode instructions to the session prompt. The
// session templates cfg.Prompt; appended text contains no template actions, so
// it is safe to concatenate.
func applyProfile(cfg types.Config) types.Config {
	if cfg.Prompt == "" {
		cfg.Prompt = voiceProfile
	} else {
		cfg.Prompt = cfg.Prompt + "\n" + voiceProfile
	}
	return cfg
}
