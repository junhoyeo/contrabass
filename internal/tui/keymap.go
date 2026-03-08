package tui

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
)

// ViewMode tracks whether the TUI is in list overview or agent detail mode.
type ViewMode int

const (
	ViewOverview ViewMode = iota
	ViewDetail
)

// FocusedPanel tracks which table panel the cursor is in.
type FocusedPanel int

const (
	PanelAgents FocusedPanel = iota
	PanelTeam
	PanelBackoff
)

type KeyMap struct {
	Quit     key.Binding
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Home     key.Binding
	End      key.Binding
	Help     key.Binding
	Enter    key.Binding
	Back     key.Binding
	Tab      key.Binding
	viewMode ViewMode
}

func NewKeyMap() KeyMap {
	return KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown"),
			key.WithHelp("pgdn", "page down"),
		),
		Home: key.NewBinding(
			key.WithKeys("home"),
			key.WithHelp("home", "top"),
		),
		End: key.NewBinding(
			key.WithKeys("end"),
			key.WithHelp("end", "bottom"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "toggle help"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("⏎", "view detail"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("⇥", "next panel"),
		),
	}
}

func (k KeyMap) SetViewMode(vm ViewMode) KeyMap {
	k.viewMode = vm
	return k
}

func (k KeyMap) ShortHelp() []key.Binding {
	if k.viewMode == ViewDetail {
		return []key.Binding{k.Up, k.Down, k.Back, k.Help, k.Quit}
	}
	return []key.Binding{k.Up, k.Down, k.Enter, k.Tab, k.Help, k.Quit}
}

func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End},
		{k.Enter, k.Back, k.Tab},
		{k.Help, k.Quit},
	}
}

var _ help.KeyMap = KeyMap{}
