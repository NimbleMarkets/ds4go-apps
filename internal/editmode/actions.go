package editmode

import "slices"

// Action describes a shortcut in the caller's current state. ID is the command
// dispatched by the caller; Keys includes aliases. Disabled actions are still
// matched, so an unavailable shortcut never leaks into the prompt editor.
type Action struct {
	ID       string
	Keys     []string
	Label    string
	Group    string
	Disabled string
}

type Actions []Action

func (a Actions) Match(k string) (Action, bool) {
	for _, action := range a {
		if slices.Contains(action.Keys, k) {
			return action, true
		}
	}
	return Action{}, false
}

func (a Actions) Bindings() []Binding {
	var bindings []Binding
	for _, action := range a {
		if action.Disabled == "" && len(action.Keys) > 0 {
			bindings = append(bindings, Binding{Keys: action.Keys[0], Desc: action.Label})
		}
	}
	return bindings
}
