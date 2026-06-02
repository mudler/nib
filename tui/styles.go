package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/mudler/nib/theme"
)

// Style vars kept for existing call sites, now sourced from the shared theme.
var (
	headerStyle        = theme.Brand
	userStyle          = theme.LabelYou
	assistantStyle     = theme.LabelNib
	errorStyle         = theme.Error
	statusStyle        = theme.Reasoning
	reasoningStyle     = theme.Reasoning
	agentStyle         = theme.Subtle
	helpStyle          = theme.Help
	thinkingStyle      = theme.Running
	toolNameStyle      = theme.Gutter
	sectionHeaderStyle = theme.Brand
	promptHintStyle    = theme.ApproveKey
	dimmedStyle        = theme.Help

	// Retained so any remaining reference compiles; the redesign no longer
	// boxes content. Safe to delete once no call sites remain.
	borderStyle = lipgloss.NewStyle()

	// Compatibility aliases for vars whose last call sites are removed by a
	// later task; kept here so the package compiles in the meantime.
	toolExecutingStyle  = theme.Done
	thinkingBoxStyle    = lipgloss.NewStyle().Padding(0, 1)
	toolRequestBoxStyle = lipgloss.NewStyle().Padding(0, 1)
	toolStyle           = lipgloss.NewStyle()
)
