package theme

// /settings copy (tui/settings.go). Kept apart from copy.go only so the
// command's strings sit together; the voice is the same: calm, lowercase, no
// emoji.
const (
	// Built-in `/` completion entry, as in copy.go's Comp* block.
	CompSettingsName = "settings"
	CompSettingsDesc = "view or change a config setting"

	// SettingsListHeader heads the /settings listing; %s is the config file
	// /settings writes to.
	SettingsListHeader = "settings · %s"
	// SettingsListFooter closes the listing with how to change a value.
	SettingsListFooter = "/settings <key> <value> to change · /settings <key> default to reset"
	// SettingsPendingNote explains the listing's marker on a saved value the
	// running session has not picked up.
	SettingsPendingNote = "* saved, applies on next start"
	SettingsPendingMark = " *"

	SettingsSourceFile    = "file"
	SettingsSourceDefault = "default"

	// SettingsSaved confirms a write: key, value, file.
	SettingsSaved = "%s = %s · saved to %s"
	// SettingsNextStart is appended to SettingsSaved for a key the running
	// session cannot change under itself.
	SettingsNextStart = " · applies on next start"
	// SettingsReset confirms an unset: key, the default now in effect, file.
	SettingsReset = "%s reset to default %s · removed from %s"
	// SettingsNotSet answers an unset of a key the file never had.
	SettingsNotSet = "%s is not set in %s · default %s already applies"

	// SettingsEndpointOverride is appended instead of SettingsNextStart when
	// a provider-owned key (model/provider/base_url) is written or reset
	// while a DIFFERENT endpoint than config.yaml's is active: the write
	// lands in the file, but %s (ActiveProviderName) is what the session
	// actually runs. /endpoint config switches back to it.
	SettingsEndpointOverride = " · not in use: this session is on %s · /endpoint config to use config.yaml"
	// SettingsModelOverride is the same notice for the narrower case where
	// the session is already on config.yaml's own endpoint, but a model pick
	// (from /model in this session, or saved with /model default) still
	// shadows the model the file names. %s is the model
	// actually running. /model reset drops that saved pick.
	SettingsModelOverride = " · not in use: this session is running the model %s · /model reset to use config.yaml's"
	// SettingsModelOverrideNoReset is SettingsModelOverride's counterpart for
	// a model-less default endpoint: config.yaml's own endpoint names no
	// model of its own, so /model reset would only fail (ResetModel refuses
	// when there is nothing to fall back to). %s is the model actually
	// running; the escape hatch is picking a different one, not resetting.
	SettingsModelOverrideNoReset = " · not in use: this session is running the model %s · config.yaml's default endpoint names no model of its own; pick one with /model"

	// SettingsSourceOverridden marks a provider-owned key in the listing and
	// detail view whose file value the running session is not using.
	SettingsSourceOverridden = "file (overridden)"

	// Completion descriptions for the value popup.
	SettingsValueCurrent = "current"
	SettingsValueDefault = "remove from the file, use the default"
)
