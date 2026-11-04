package agent

// Antigravity CLI (agy) takes an Enterprise Gateway from its environment:
// AGY_LLM_GATEWAY_URL, AGY_LLM_GATEWAY_API_KEY and
// AGY_LLM_GATEWAY_WIRE_PROTOCOL=openai make it ask <url>/v1/models and
// <url>/v1/chat/completions instead of Google, and its /model list is the
// gateway's models alone (agy 1.4.12; docs.cloud.google.com/gemini/enterprise/
// docs/ai-developer-tools/connect-enterprise-gateway). magpie starts agy so,
// with a key naming it (AgyStart, agyLaunchAt), and writes nothing of agy's
// settings to connect it: the old modelProvider/customModels route is only
// cleaned up when it is found (agyMigrate), and a modelProvider of "gemini",
// which would send agy's chats to /v1beta, is taken out while connected and
// put back on Disconnect.
//
// Whether agy is on magpie is kept in magpie's stash, keyed by the absolute
// settings path. No file is added to agy's folder: the CLI and the desktop
// app must continue to see only their own settings. The launch command and
// shell function are the other half: a terminal opened before needs the
// command.
//
// Typing agy starts it on magpie too (bestony on Discord: the command was
// copied at each start), through a function named agy in the profile of
// each shell the user has, between "# >>> magpie agy >>>" and
// "# <<< magpie agy <<<" (update.ShellFunction). It runs `magpie agy run`,
// which reads magpie's stash and starts the real agy with the gateway
// variables and --model when one isn't given (cli/agy_cli.go).
// PowerShell's function asks `magpie agy env` for them, since the app it
// would run is a GUI program PowerShell doesn't wait for.
//
// A WSL distro has no magpie to ask, so its function carries the address,
// key and model itself; Sync writes it again when the address or model
// moves (agyDistroFunction).
//
// Native models are agy's own: the gateway lists none, and magpie does not
// write agy's settings to put one there. They are picked in agy's own
// /model after stepping out, so agy's model field offers magpie's models
// alone.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/jsonc"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/update"
)

const agyModels = "customModelsConfig.customModels"

// AgyEnvKey says whether k is one of the variables that start agy on
// magpie's gateway, or the old Gemini API ones: `magpie agy run` replaces
// them for the child so an inherited value can't point it elsewhere. An
// empty AGY_LLM_GATEWAY_MODELS asks for every model the gateway lists.
func AgyEnvKey(k string) bool {
	switch strings.ToUpper(k) {
	case "GEMINI_API_KEY", "GOOGLE_GEMINI_BASE_URL",
		"AGY_LLM_GATEWAY_URL", "AGY_LLM_GATEWAY_API_KEY",
		"AGY_LLM_GATEWAY_WIRE_PROTOCOL", "AGY_LLM_GATEWAY_MODELS":
		return true
	}
	return false
}

// AgyLaunch is the command that starts agy on magpie's gateway and one of
// its models, in the shell of this system. Its key names agy: agy's
// requests go out as antigravity/cli/…, which the gateway would read as
// the Antigravity app, so the key (Authorization) is what says they are
// agy's. A command copied before the gateway route, with GEMINI_API_KEY
// and GOOGLE_GEMINI_BASE_URL, belongs to the settings migration: the row's
// command is the current one.
func AgyLaunch(model string) string {
	return agyLaunchAt(gateway.URL(), agentKey("agy"), model, runtime.GOOS == "windows")
}

// agyLaunchAt is AgyLaunch for agy reaching the gateway at gw with key, in
// PowerShell or a POSIX shell.
func agyLaunchAt(gw, key, model string, powershell bool) string {
	model = strings.TrimPrefix(model, magpieID+"/")
	if powershell {
		return `$env:AGY_LLM_GATEWAY_URL="` + gw + `"; $env:AGY_LLM_GATEWAY_API_KEY="` + key + `"; $env:AGY_LLM_GATEWAY_WIRE_PROTOCOL="openai"; $env:AGY_LLM_GATEWAY_MODELS=""; agy --model '` + strings.ReplaceAll(model, "'", "''") + `'`
	}
	return "AGY_LLM_GATEWAY_URL=" + gw + " AGY_LLM_GATEWAY_API_KEY=" + key + " AGY_LLM_GATEWAY_WIRE_PROTOCOL=openai AGY_LLM_GATEWAY_MODELS= agy --model '" + strings.ReplaceAll(model, "'", `'\''`) + "'"
}

// AgyStart is what starts agy on magpie, the variables and the model,
// while magpie's stash has a model; nil and "" otherwise, when agy starts as
// it is. The error is magpie's record that can't be read. It only reads:
// `magpie agy run` runs beside the app, whose stash lock (stashMu) is its
// own process's, so old settings are cleaned by the app's Sync and Connect,
// and agy's settings.json isn't read at all.
func AgyStart() (env []string, model string, err error) {
	home, _ := homes()
	st := agyReadState(agyStateKey(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")))
	if !st.Known {
		return nil, "", fmt.Errorf("magpie's Antigravity CLI record can't be read")
	}
	if !st.On() {
		return nil, "", nil
	}
	return agyEnv(gateway.URL(), agentKey("agy")), st.Model, nil
}

// agyEnv is what starts agy on the gateway at gw, with key.
func agyEnv(gw, key string) []string {
	return []string{
		"AGY_LLM_GATEWAY_URL=" + gw,
		"AGY_LLM_GATEWAY_API_KEY=" + key,
		"AGY_LLM_GATEWAY_WIRE_PROTOCOL=openai",
		"AGY_LLM_GATEWAY_MODELS=",
	}
}

// agyShell gives the user's shells the function agy, or takes it out of
// them; only for agy on this machine. What fails leaves the setting as it
// is: the row's command still starts it.
func agyShell(home string, on bool) {
	if here, _ := homes(); here != home {
		return
	}
	if on {
		update.ShellFunction("agy", agyFunction)
	} else {
		update.DropShellFunction("agy")
	}
}

// agyFunction is the function agy in a shell of kind, which runs prog
// (this magpie) to start it; the agy on PATH only when prog is gone:
// `agy run` is agy itself, so agy's exit code is the function's, and one
// that failed or was interrupted isn't started again off magpie.
// PowerShell's asks prog's `agy env` instead and starts agy itself, the
// variables put back as they were after it: prog is a GUI program there,
// which PowerShell neither waits for nor takes an exit code from.
func agyFunction(kind, prog string) string {
	switch kind {
	case "fish":
		q := "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(prog) + "'"
		return "function agy\n    if test -x " + q + "\n        " + q + " agy run $argv\n    else\n        command agy $argv\n    end\nend"
	case "powershell":
		q := "'" + strings.ReplaceAll(prog, "'", "''") + "'"
		return `function agy {
    $real = Get-Command agy -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $real) { Write-Error 'agy is not installed'; return }
    $start = @{}
    if (Test-Path -LiteralPath ` + q + `) {
        foreach ($l in @(& ` + q + ` agy env)) { $k, $v = "$l" -split '=', 2; if ($k) { $start[$k] = $v } }
    }
    if (-not $start.model) { & $real @args; return }
    $names = 'AGY_LLM_GATEWAY_URL', 'AGY_LLM_GATEWAY_API_KEY', 'AGY_LLM_GATEWAY_WIRE_PROTOCOL', 'AGY_LLM_GATEWAY_MODELS', 'GEMINI_API_KEY', 'GOOGLE_GEMINI_BASE_URL'
    $was = @{}
    foreach ($n in $names) { $was[$n] = [Environment]::GetEnvironmentVariable($n); [Environment]::SetEnvironmentVariable($n, $start[$n]) }
    try {
        if (@($args | Where-Object { "$_" -match '^--?model(=|$)' }).Count) { & $real @args } else { & $real --model $start.model @args }
    } finally { foreach ($n in $names) { [Environment]::SetEnvironmentVariable($n, $was[$n]) } }
}`
	}
	q := "'" + strings.ReplaceAll(prog, "'", `'\''`) + "'"
	// "function": a name the user aliased isn't expanded where it is defined
	return "function agy {\n  if [ -x " + q + " ]; then " + q + ` agy run "$@"; else command agy "$@"; fi` + "\n}"
}

// agyDistroProfiles are the profiles of a WSL distro's shells the
// function goes in, as magpie opens them: bash's ~/.bashrc (which the
// ~/.profile of a login bash reads on Debian and Ubuntu), zsh's ~/.zshrc
// and fish's config.fish, each where it is already, and the one of the
// user's login shell in the distro's /etc/passwd (bash when it says
// nothing) made where it isn't, as fish's is where ~/.config/fish is. The
// distro's ZDOTDIR and XDG_CONFIG_HOME aren't read.
func agyDistroProfiles(at place) []update.ShellProfile {
	login := distroLoginShell(at)
	fish := at.join(".config", "fish", "config.fish")
	return []update.ShellProfile{
		{Kind: "posix", Path: at.join(".bashrc"), Create: login == "bash"},
		{Kind: "posix", Path: at.join(".zshrc"), Create: login == "zsh"},
		{Kind: "fish", Path: fish, Create: login == "fish" || at.isDir(filepath.Dir(fish))},
	}
}

// distroLoginShell is the name of the login shell /etc/passwd gives the
// distro's user, the one whose home is at's; "bash" when it can't say.
func distroLoginShell(at place) string {
	if at.sys == nil {
		return "bash"
	}
	b, err := os.ReadFile(at.sys("/etc/passwd"))
	if err != nil {
		return "bash"
	}
	home := at.native(at.home)
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(strings.TrimRight(l, "\r"), ":")
		if len(f) >= 7 && f[5] == home && f[6] != "" {
			return path.Base(f[6])
		}
	}
	return "bash"
}

// agyDistroFunction is the function agy in a distro's shell of kind: the
// gateway at gw, its key and the model (none when one is given), as
// `magpie agy run` does on this machine. No magpie runs there to read
// magpie's stash, so all of it is in the function; Sync writes it again when
// the address or model moves.
func agyDistroFunction(kind, key, gw, model string) string {
	model = strings.TrimPrefix(model, magpieID+"/")
	q := func(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }
	if kind == "fish" {
		fq := func(v string) string { return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(v) + "'" }
		return "function agy\n" +
			"    set -l m " + fq(model) + "\n" +
			"    for a in $argv\n" +
			"        switch $a\n" +
			"            case '--model' '--model=*' '-model' '-model=*'\n" +
			"                set m ''\n" +
			"        end\n" +
			"    end\n" +
			"    if test -n \"$m\"\n        set argv --model $m $argv\n    end\n" +
			"    env GEMINI_API_KEY= GOOGLE_GEMINI_BASE_URL= AGY_LLM_GATEWAY_MODELS= AGY_LLM_GATEWAY_URL=" + fq(gw) + " AGY_LLM_GATEWAY_API_KEY=" + fq(key) + " AGY_LLM_GATEWAY_WIRE_PROTOCOL=openai agy $argv\nend"
	}
	return "function agy {\n" +
		"  local m=" + q(model) + " a\n" +
		`  for a in "$@"; do case "$a" in --model|--model=*|-model|-model=*) m=;; esac; done` + "\n" +
		`  if [ -n "$m" ]; then set -- --model "$m" "$@"; fi` + "\n" +
		"  GEMINI_API_KEY= GOOGLE_GEMINI_BASE_URL= AGY_LLM_GATEWAY_MODELS= AGY_LLM_GATEWAY_URL=" + q(gw) + " AGY_LLM_GATEWAY_API_KEY=" + q(key) + " AGY_LLM_GATEWAY_WIRE_PROTOCOL=openai command agy \"$@\"\n}"
}

func agyStateKey(path string) string { return "agy.state:" + filepath.Clean(path) }

// Before distinguishes absent keys from present empty values. ProviderRemoved
// says magpie temporarily removed modelProvider while using the gateway.
// Model is the gateway id; it is never written to agy's settings.
type agyState struct {
	Model           string                     `json:"model"`
	Before          map[string]json.RawMessage `json:"before"`
	ProviderRemoved bool                       `json:"providerRemoved,omitempty"`
	Known           bool                       `json:"-"`
}

func (s agyState) On() bool { return s.Known && s.Model != "" }

func agyStateOf(m map[string]string, key string) (agyState, error) {
	s := agyState{Known: true}
	if b, ok := m[key]; ok {
		if err := json.Unmarshal([]byte(b), &s); err != nil || s.Before == nil {
			return agyState{}, fmt.Errorf("invalid Antigravity CLI stash record for %s", key)
		}
	}
	return s, nil
}

func agyReadState(key string) agyState {
	stashMu.Lock()
	defer stashMu.Unlock()
	m, err := stashLoadKnown()
	if err != nil {
		return agyState{}
	}
	s, _ := agyStateOf(m, key)
	return s
}

// agyStashKnown says magpie's stash can be read as a whole.
func agyStashKnown() bool {
	stashMu.Lock()
	defer stashMu.Unlock()
	_, err := stashLoadKnown()
	return err == nil
}

// agyChange serializes the fresh settings read with every stash mutation.
// Settings and stash are rolled back together on failure; no snapshot is
// consumed before restoration succeeds. Catalog work happens before the lock:
// "set" to "" steps out whatever the catalog says of the model.
func agyChange(id, path, ref, how string) error {
	stashMu.Lock()
	defer stashMu.Unlock()
	return edit.Atomically(func() error {
		m, err := stashLoadKnown()
		if err != nil {
			return err
		}
		initial := maps.Clone(m)
		key := agyStateKey(path)
		s, err := agyStateOf(m, key)
		if err != nil {
			return err
		}
		raw, err := edit.Read(path)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			raw = []byte("{}\n")
		}
		original := bytes.Clone(raw)
		clean := jsonc.ToJSON(raw)
		if !json.Valid(clean) || !gjson.ParseBytes(clean).IsObject() {
			return fmt.Errorf("%s: invalid settings JSON object", path)
		}
		raw, s, err = agyMigrate(id, path, raw, m, s)
		if err != nil {
			return err
		}
		settings := gjson.ParseBytes(jsonc.ToJSON(raw))
		switch {
		case how == "set" && ref == "":
			var sets []edit.KV
			var dels []string
			// Magpie only removes this key. Restore only while it is still
			// absent; an explicit empty string is an external change too.
			if s.ProviderRemoved && !settings.Get("modelProvider").Exists() {
				if before, ok := s.Before["modelProvider"]; ok {
					sets = append(sets, edit.KV{Path: "modelProvider", Value: before})
				} else {
					dels = append(dels, "modelProvider")
				}
			}
			if len(sets)+len(dels) > 0 {
				raw, err = edit.PatchJSON(raw, sets, dels)
				if err != nil {
					return err
				}
			}
			s = agyState{Known: true}
		default:
			if how == "set" {
				if !s.On() {
					s = agyState{Before: map[string]json.RawMessage{}, Known: true}
					for _, k := range []string{"model", "modelProvider"} {
						if v := settings.Get(k); v.Exists() {
							s.Before[k] = json.RawMessage(v.Raw)
						}
					}
				}
				s.Model = ref
			}
			if s.On() && settings.Get("modelProvider").String() == "gemini" {
				// A new user choice while connected is temporarily removed too,
				// but it is that choice, not an older snapshot, which returns.
				s.Before["modelProvider"] = json.RawMessage(settings.Get("modelProvider").Raw)
				s.ProviderRemoved = true
				raw, err = edit.PatchJSON(raw, nil, []string{"modelProvider"})
				if err != nil {
					return err
				}
			}
		}
		if !bytes.Equal(raw, original) && !(len(original) == 0 && bytes.Equal(raw, []byte("{}\n"))) {
			if err := edit.WriteAtomic(path, raw); err != nil {
				return err
			}
		}
		if s.On() {
			b, err := json.Marshal(s)
			if err != nil {
				return err
			}
			m[key] = string(b)
		} else {
			delete(m, key)
		}
		// Migration also consumes the legacy entries; avoid creating a stash
		// for an untouched, disconnected CLI.
		if !maps.Equal(initial, m) {
			return stashWrite(m)
		}
		return nil
	}, path, stashPath())
}

func agy(home string) *Agent { return agyIn(here(home)) }

// agyIn is agy at a place: this machine's home, or a WSL distro's, whose
// shells get a function of their own (agyDistroFunction).
func agyIn(at place) *Agent {
	home := at.home
	id := at.id
	if id == "" {
		id = "agy"
	}
	dir := at.open(filepath.Join(home, ".gemini", "antigravity-cli"))
	path := filepath.Join(dir, "settings.json")
	stateKey := agyStateKey(path)
	// get is magpie's model while magpie's stash says so; otherwise agy's
	// own model, which magpie leaves alone. A model that is one of
	// magpie's without magpie's stash is not magpie's route (an old file,
	// or one typed by hand): it isn't claimed.
	get := func() string {
		st := agyReadState(stateKey)
		if st.On() {
			return magpieID + "/" + st.Model
		}
		if !st.Known {
			return ""
		}
		m, _ := edit.GetJSON(path, "model")
		if m == "" || strings.HasPrefix(m, magpieID+"/") || isMagpie(m) {
			return ""
		}
		return m
	}
	// the function agy, in this machine's shells or the distro's
	distro := at.sys != nil
	gw, key := at.gw, func() string { return agentKeyAt("agy", at.gw()) }
	shell := func(on bool) {
		if !distro {
			agyShell(home, on)
			return
		}
		if !on {
			update.DropShellFunctionAt(agyDistroProfiles(at), "agy")
			return
		}
		ref := agyReadState(stateKey).Model
		if ref == "" {
			update.DropShellFunctionAt(agyDistroProfiles(at), "agy")
			return
		}
		k, g := key(), gw()
		update.ShellFunctionAt(agyDistroProfiles(at), "agy", func(kind string) string {
			return agyDistroFunction(kind, k, g, ref)
		})
	}
	shellIn := func() []string {
		if !distro {
			return update.ShellFunctionIn("agy")
		}
		return update.ShellFunctionInAt(agyDistroProfiles(at), "agy")
	}
	launch := func(ref string) string {
		if !distro {
			return AgyLaunch(ref)
		}
		return agyLaunchAt(gw(), key(), ref, false)
	}
	// set is the model field's Set; "" steps out
	set := func(v string) error {
		ref, ours := strings.CutPrefix(v, magpieID+"/")
		if ref != "" && !isMagpie(ref) {
			if ours {
				return fmt.Errorf("magpie no longer serves %s; pick another of its models for Antigravity CLI, or Default to step out", ref)
			}
			return fmt.Errorf("Antigravity CLI's own models are picked in agy's /model after disconnecting; pick one of magpie's models, or Default to step out")
		}
		if err := agyChange(id, path, ref, "set"); err != nil {
			return err
		}
		shell(agyReadState(stateKey).On())
		return nil
	}
	return &Agent{
		ID: "agy", Name: "Antigravity CLI", Icon: "antigravity-color", Aliases: []string{"antigravity-cli"}, NoNames: true,
		Bin: "agy", Dir: dir, Path: path,
		Launch: func() string {
			if st := agyReadState(stateKey); st.On() {
				return launch(st.Model)
			}
			return ""
		},
		Typed: func() string {
			if !agyReadState(stateKey).On() || len(shellIn()) == 0 {
				return ""
			}
			return "agy"
		},
		Sync: func() error {
			// a stash that can't be read at all is left to Check: with
			// nothing of the old magpie's to clean, an agy that may never
			// have been on magpie isn't refused a sync over it
			if !agyStashKnown() && !agyOldWiring(path) {
				return nil
			}
			if err := agyChange(id, path, "", "sync"); err != nil {
				return err
			}
			shell(agyReadState(stateKey).On())
			return nil
		},
		Notice: func() string {
			st := agyReadState(stateKey)
			if !st.On() {
				return ""
			}
			command := launch(st.Model)
			if in := shellIn(); len(in) > 0 {
				for i := range in {
					if distro {
						in[i] = at.native(in[i])
					} else {
						in[i] = update.TildeHome(in[i])
					}
				}
				return noticeAgyTyped.say("profiles", strings.Join(in, ", "), "command", command)
			}
			n := noticeAgy.say("command", command)
			if distro {
				n += " A WSL terminal already open needs reopening, or its Launch command rerun, after Default or a model change."
			}
			return n
		},
		Check: func() string {
			st := agyReadState(stateKey)
			if !st.Known {
				if agyOldWiring(path) {
					return "Antigravity CLI's settings.json still has the old magpie's model or custom models, which magpie couldn't clean"
				}
				return "magpie's Antigravity CLI stash record can't be read; it is stored as " + stateKey + "; Pick agy's model again to rebuild it"
			}
			if !st.On() {
				return ""
			}
			if _, err := edit.Read(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "Antigravity CLI's settings.json can't be read, so magpie can't tell what agy runs on"
			}
			if !isMagpie(st.Model) {
				return "Antigravity CLI's model " + st.Model + " is no longer in magpie's catalog, so it no longer reaches magpie"
			}
			if agyOldWiring(path) {
				return "Antigravity CLI's settings.json still has the old magpie's model or custom models, which magpie couldn't clean"
			}
			if p, _ := edit.GetJSON(path, "modelProvider"); p == "gemini" {
				return "Antigravity CLI's settings.json has modelProvider gemini, so it asks Google's Gemini API instead of magpie's gateway; Sync takes it out"
			}
			if distro {
				return agyDistroCheck(at, gw(), key(), st.Model)
			}
			return ""
		},
		Fields: []Field{{
			Key: "model", Label: "model",
			Get: get,
			Set: set,
			Options: func(map[string]string) []Option {
				// agy's own models aren't in the gateway's list, and
				// writing one into its settings is what this route
				// doesn't do: they are picked in agy's /model after
				// Default steps out.
				return withVia(nil, "agy", magpieID+"/")
			},
		}},
		Owned: func() bool { return agyOwned(path, !at.cold) },
		// Disconnect's whole step-out, its fields left alone: the old
		// wiring cleaned, the modelProvider magpie took out put back,
		// magpie's record and the function gone
		Unwire: func() error { return set("") },
	}
}

// agyOwned says agy, its settings at path, is on magpie: while magpie's
// record says so, whatever the catalog says of its model now; while the
// record can't be read, so Disconnect says why it can't take it out rather
// than finding nothing to; and, when look, while the old wiring is in its
// settings. A stopped distro's settings aren't looked at: opening them
// through \\wsl.localhost starts it (#3896).
func agyOwned(path string, look bool) bool {
	if st := agyReadState(agyStateKey(path)); !st.Known || st.On() {
		return true
	}
	return look && agyOldWiring(path)
}

// agyDistroCheck says what in the distro's function agy no longer matches
// the gateway address, key or model magpie has now; "" when it does, or
// when nothing can be read to tell.
func agyDistroCheck(at place, gw, key, ref string) string {
	profiles := agyDistroProfiles(at)
	in := update.ShellFunctionInAt(profiles, "agy")
	if len(in) == 0 {
		readable := false
		for _, p := range profiles {
			if _, err := os.ReadFile(p.Path); err == nil {
				readable = true
			}
		}
		if readable {
			return "Antigravity CLI's function agy is gone from the distro's shells; Sync adds it again"
		}
		return ""
	}
	for _, p := range profiles {
		if !slices.Contains(in, p.Path) {
			continue
		}
		b, err := os.ReadFile(p.Path)
		if err != nil {
			continue // unknown
		}
		if !strings.Contains(string(b), agyDistroFunction(p.Kind, key, gw, ref)) {
			return "Antigravity CLI's function agy in " + at.native(p.Path) + " names another gateway or model; Sync updates it"
		}
	}
	return ""
}

// agyOldModel says k, v, one of agy's custom models, is one the old magpie
// wrote: under magpie/, through Google's Gemini API.
func agyOldModel(k, v gjson.Result) bool {
	return strings.HasPrefix(k.String(), magpieID+"/") && v.Get("apiProvider").String() == "API_PROVIDER_GOOGLE_GEMINI"
}

// agyOld says agy's settings have what the old magpie wrote, which
// agyMigrate cleans: a model of magpie's, or one of its custom models.
func agyOld(settings gjson.Result) bool {
	if ref, ok := strings.CutPrefix(settings.Get("model").String(), magpieID+"/"); ok && ref != "" {
		return true
	}
	old := false
	settings.Get(agyModels).ForEach(func(k, v gjson.Result) bool {
		old = agyOldModel(k, v)
		return !old
	})
	return old
}

// agyOldWiring says agy's settings at path have the old wiring (agyOld);
// a file that can't be read, or isn't a JSON object, doesn't.
func agyOldWiring(path string) bool {
	raw, err := edit.Read(path)
	if err != nil || len(raw) == 0 {
		return false
	}
	clean := jsonc.ToJSON(raw)
	if !json.Valid(clean) || !gjson.ParseBytes(clean).IsObject() {
		return false
	}
	return agyOld(gjson.ParseBytes(clean))
}

// agyMigrate removes the old Gemini custom-model route. Each original is
// restored only while the current field is still what old magpie wrote.
// Legacy stash entries did not record empty values or absence. A stashed
// "gemini" already polluted by an old disconnect is indistinguishable from
// the user's own "gemini"; preserve it, rather than inventing an original.
func agyMigrate(id, path string, raw []byte, m map[string]string, s agyState) ([]byte, agyState, error) {
	settings := gjson.ParseBytes(jsonc.ToJSON(raw))
	if !agyOld(settings) {
		return raw, s, nil
	}
	keep := map[string]json.RawMessage{}
	settings.Get(agyModels).ForEach(func(k, v gjson.Result) bool {
		if !agyOldModel(k, v) {
			keep[k.String()] = json.RawMessage(v.Raw)
		}
		return true
	})
	before := map[string]json.RawMessage{}
	prefix := "agy:" + path + ":"
	// Also accept the snapshot used by the disconnect-restoration fix.
	if b, ok := m[prefix+"before"]; ok {
		if err := json.Unmarshal([]byte(b), &before); err != nil || before == nil {
			return nil, s, fmt.Errorf("invalid agy legacy snapshot")
		}
	} else {
		for _, k := range []string{"model", "modelProvider"} {
			if v, ok := m[prefix+k]; ok {
				b, _ := json.Marshal(v)
				before[k] = b
			}
		}
	}
	if v := before["model"]; len(v) == 0 || strings.HasPrefix(gjson.ParseBytes(v).String(), magpieID+"/") {
		var was map[string]string
		if b := m[id+".connect.was"]; b != "" {
			if err := json.Unmarshal([]byte(b), &was); err != nil {
				return nil, s, err
			}
		}
		delete(before, "model")
		if v := was["model"]; v != "" && !strings.HasPrefix(v, magpieID+"/") {
			b, _ := json.Marshal(v)
			before["model"] = b
		}
	}
	var sets []edit.KV
	var dels []string
	restore := func(k string) {
		if v, ok := before[k]; ok {
			sets = append(sets, edit.KV{Path: k, Value: v})
		} else {
			dels = append(dels, k)
		}
	}
	cur := settings.Get("model").String()
	connected := strings.HasPrefix(cur, magpieID+"/")
	if connected {
		restore("model")
	}
	if settings.Get("modelProvider").String() == "gemini" {
		restore("modelProvider")
	}
	if len(keep) > 0 {
		sets = append(sets, edit.KV{Path: agyModels, Value: keep})
	} else {
		other := false
		settings.Get("customModelsConfig").ForEach(func(k, _ gjson.Result) bool { other = other || k.String() != "customModels"; return true })
		if other {
			dels = append(dels, agyModels)
		} else {
			dels = append(dels, "customModelsConfig")
		}
	}
	if connected && !s.On() {
		s = agyState{Model: strings.TrimPrefix(cur, magpieID+"/"), Before: before, Known: true}
	}
	out, err := edit.PatchJSON(raw, sets, dels)
	delete(m, prefix+"before")
	delete(m, prefix+"model")
	delete(m, prefix+"modelProvider")
	return out, s, err
}

func isAgy(id string) bool { return id == "agy" || strings.HasPrefix(id, "agy@wsl:") }

// what agy says after a change (notice.go)
var (
	noticeAgy      = newNotice("agy takes magpie's gateway and model only from how it is started — start it with {command}")
	noticeAgyTyped = newNotice("Type agy in a new terminal: magpie added it to {profiles}, to start agy on magpie's gateway and model (in a terminal open now: {command}).")
)
