package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

// classifierConfig is m.cfg with the session's /classifier choice, if any,
// in place of the configured classifier block.
func (m Model) classifierConfig() types.Config {
	cfg := m.cfg
	if o := m.classifierOverride; o != nil {
		cfg.Classifier = *o
	}
	return cfg
}

// useClassifier switches the session's classifier to model on endpointID,
// or turns it off, for the rest of the session. The configured classifier's
// other settings (api, timeout) carry over.
func (m *Model) useClassifier(endpointID, model string, off bool) {
	if m.session == nil {
		return
	}
	var c types.ClassifierConfig
	if !off {
		var err error
		if c, err = chat.ClassifierChoice(m.cfg.Classifier, endpointID, model); err != nil {
			m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
			return
		}
	}
	cfg := m.cfg
	cfg.Classifier = c
	fellBack, err := m.session.SetClassifier(cfg)
	if err != nil {
		m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
		return
	}
	m.classifierOverride = &c
	notice := theme.ClassifierOffNotice
	if info := m.session.ClassifierInfo(); info != "" {
		notice = fmt.Sprintf(theme.ClassifierSet, info)
	}
	if fellBack {
		notice += theme.ClassifierFellBack
	}
	m.appendMessage(ChatMessage{Role: "agent", Content: notice})
}

// applyClassifierOverride re-installs the session's /classifier choice on a
// rebuilt session, which starts from config.yaml.
func (m *Model) applyClassifierOverride() {
	if m.classifierOverride == nil || m.session == nil {
		return
	}
	if _, err := m.session.SetClassifier(m.classifierConfig()); err != nil {
		m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
	}
}

// openClassifierModelPicker is Enter in /classifier's endpoint picker: the
// endpoint's models, the pick going to the classifier, not the chat.
func (m *Model) openClassifierModelPicker(e chat.ProviderEntry) tea.Cmd {
	cmd := m.openProviderModelPicker(e)
	m.modelPicker.forClassifier = true
	return cmd
}
