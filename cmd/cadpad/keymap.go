package main

import (
	"github.com/NimbleMarkets/ds4go-apps/internal/editmode"
)

func (m model) keymap() editmode.Keymap {
	def := editmode.DefaultEditBindings()

	edit := make([]editmode.Binding, 0, len(def)+2)
	edit = append(edit, editmode.Binding{Keys: "ctrl+n", Desc: "log"})
	edit = append(edit, def...)

	cmd := []editmode.Binding{
		{Keys: "e", Desc: "edit prompt"},
		{Keys: "j/k", Desc: "nav objects"},
		{Keys: "1/2/3/4/tab", Desc: "proj"},
		{Keys: "p", Desc: "preview"},
		{Keys: "s", Desc: "save (json + timestamped .lua)"},
		{Keys: "r", Desc: "refresh"},
		{Keys: "pgup/pgdown", Desc: "browse prev .lua files"},
		{Keys: "v", Desc: "view lua source"},
		{Keys: "t", Desc: "thinking"},
		{Keys: "ctrl+n", Desc: "log"},
		{Keys: "?", Desc: "help"},
		{Keys: "ctrl+c", Desc: "quit"},
	}

	return editmode.Keymap{Edit: edit, Command: cmd}
}
