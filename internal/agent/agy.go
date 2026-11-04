package agent

// Antigravity's CLI, agy, keeps its settings in
// ~/.gemini/antigravity-cli/settings.json. With "modelProvider": "gemini" it
// asks a Gemini API endpoint rather than Google's own backend, and lists the
// models of customModelsConfig.customModels, each {apiProvider, modelName},
// the one picked being "model". magpie's gateway speaks the Gemini API, so
// magpie adds one custom model per catalog model, keyed
// "magpie/<provider>/<model>", and sets modelProvider and model; the user's
// own are stashed and put back when magpie steps out. Removing magpie's
// models and shell function is separate from restoring modelProvider:
// only an exact saved original (the snapshot or old per-field stash) lets
// magpie restore or delete that field. Without one, it leaves the current
// provider untouched. A v0.1.2862 snapshot that saved magpie's "gemini" as
// the original cannot be distinguished from a user's original "gemini".
// WSL snapshots use the opened path, including
// `\\wsl.localhost\Ubuntu\...`, under `agy:<path>:before`; other agy stash
// keys use the row id, such as `agy@wsl:Ubuntu.connect.was`. The endpoint and key
// agy takes only from its environment (GOOGLE_GEMINI_BASE_URL and
// GEMINI_API_KEY; no .env file), so magpie can't set them: the agent's row
// gives the command that starts agy with them (see Agent.Launch). That
// names the model with --model too: agy 1.2 asks for a custom model picked
// in settings.json or its /model picker with no model name ("model is
// empty"); only one given on the command line resolves.
//
// So that typing agy just works (bestony on Discord: the command was
// copied at each start), magpie puts a function named agy in the profile
// of each shell the user has, between "# >>> magpie agy >>>" and
// "# <<< magpie agy <<<" (update.ShellFunction), while agy is on one of
// magpie's models. It runs `magpie agy run`, which starts the real agy
// with the two variables and --model when settings.json's model is still
// magpie's and as it is otherwise (cli/agy_cli.go); PowerShell's asks
// `magpie agy env` for them, since the app it would run is a GUI program
// PowerShell doesn't wait for. A terminal open before takes the command
// from the row. agy's own /model changes only model, so a model picked
// there leaves magpie's provider, custom models and function behind;
// Follow takes them out as Disconnect does, as the Agents page looks and
// in `magpie agy run` and `env` before agy starts as it is. A distro's
// function is shell alone, with no magpie to ask, so there it waits for
// the page or a Sync.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/jsonc"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/update"
)

const agyModels = "customModelsConfig.customModels"

func agyHasMagpieModels(path string) bool {
	for k := range agyCustom(path) {
		if strings.HasPrefix(k, magpieID+"/") {
			return true
		}
	}
	return false
}

// AgyHasMagpieArtifacts reports whether agy's settings still contain magpie
// artifacts that need cleaning, even when its model is native.
func AgyHasMagpieArtifacts(a *Agent) bool {
	if a == nil || !isAgy(a.ID) {
		return false
	}
	model := ""
	if f := a.Field("model"); f != nil {
		model = f.Get()
	}
	return usesMagpie(model) || agyHasMagpieModels(a.Path)
}

// AgyLaunch is the command that starts agy on magpie's gateway and one of
// its custom models, in the shell of this system. Its key names agy: its
// requests go out as Google's Gemini SDK's (User-Agent google-genai-sdk/…),
// with nothing of agy's own, so the gateway knows them by the key
// (x-goog-api-key) alone. A command copied before, with the plain token,
// still reaches magpie.
func AgyLaunch(model string) string {
	return agyLaunchAt(gateway.URL(), agentKey("agy"), model, runtime.GOOS == "windows")
}

// agyLaunchAt is AgyLaunch for agy reaching the gateway at gw with key, in
// PowerShell or a POSIX shell.
func agyLaunchAt(gw, key, model string, powershell bool) string {
	if powershell {
		return `$env:GEMINI_API_KEY="` + key + `"; $env:GOOGLE_GEMINI_BASE_URL="` + gw + `"; agy --model '` + strings.ReplaceAll(model, "'", "''") + `'`
	}
	return "GEMINI_API_KEY=" + key + " GOOGLE_GEMINI_BASE_URL=" + gw + " agy --model '" + strings.ReplaceAll(model, "'", `'\''`) + "'"
}

// AgyStart is what starts agy on magpie, the variables and the model,
// while settings.json's model is one of magpie's; nil and "" otherwise,
// when agy starts as it is.
func AgyStart() (env []string, model string) {
	home, _ := homes()
	m := jsonGet(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), "model")()
	if !usesMagpie(m) {
		return nil, ""
	}
	return []string{"GEMINI_API_KEY=" + agentKey("agy"), "GOOGLE_GEMINI_BASE_URL=" + gateway.URL()}, m
}

// AgyFollow is Follow for agy on this machine, for `magpie agy run` to
// take out what magpie left once agy's own /model picked a model of its
// own, before it starts agy.
func AgyFollow() error {
	home, _ := homes()
	return agy(home).Follow()
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
// (this magpie) to start it; the agy on PATH when prog is gone.
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
        foreach ($l in @(& ` + q + ` agy env)) { $k, $v = "$l" -split '=', 2; if ($v) { $start[$k] = $v } }
    }
    if (-not $start.model) { & $real @args; return }
    $was = $env:GEMINI_API_KEY, $env:GOOGLE_GEMINI_BASE_URL
    $env:GEMINI_API_KEY = $start.GEMINI_API_KEY; $env:GOOGLE_GEMINI_BASE_URL = $start.GOOGLE_GEMINI_BASE_URL
    try {
        if (@($args | Where-Object { "$_" -match '^--?model(=|$)' }).Count) { & $real @args } else { & $real --model $start.model @args }
    } finally { $env:GEMINI_API_KEY = $was[0]; $env:GOOGLE_GEMINI_BASE_URL = $was[1] }
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

// agyDistroModel is the sed script that prints settings.json's model while
// it is one of magpie's, for the function in a distro, where no magpie
// runs to read it. Its model is a line of its own as magpie writes the
// file; one written on a single line is found too, so long as nothing
// after it is named "model".
const agyDistroModel = `s|^.*"model"[[:space:]]*:[[:space:]]*"\(` + magpieID + `/[^"]*\)".*$|\1|p`

// agyDistroFunction is the function agy in a distro's shell of kind:
// while settings.json's model is magpie's, the agy on PATH started with
// the gateway at gw, its key and --model (none when one is given), as
// `magpie agy run` does on this machine; as it is otherwise.
func agyDistroFunction(kind, key, gw string) string {
	q := func(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }
	settings := `"$HOME/.gemini/antigravity-cli/settings.json"`
	if kind == "fish" {
		fq := func(v string) string { return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(v) + "'" }
		return "function agy\n" +
			"    set -l m (sed -n " + fq(agyDistroModel) + " " + settings + " 2>/dev/null | head -n 1)\n" +
			"    if test -z \"$m\"\n        command agy $argv\n        return\n    end\n" +
			"    for a in $argv\n        switch $a\n            case '--model' '--model=*' '-model' '-model=*'\n                set m ''\n        end\n    end\n" +
			"    if test -n \"$m\"\n        set argv --model $m $argv\n    end\n" +
			"    env GEMINI_API_KEY=" + fq(key) + " GOOGLE_GEMINI_BASE_URL=" + fq(gw) + " agy $argv\nend"
	}
	return "function agy {\n" +
		"  local m a\n" +
		"  m=$(sed -n " + q(agyDistroModel) + " " + settings + " 2>/dev/null | head -n 1)\n" +
		`  if [ -z "$m" ]; then command agy "$@"; return; fi` + "\n" +
		`  for a in "$@"; do case "$a" in --model|--model=*|-model|-model=*) m=;; esac; done` + "\n" +
		`  if [ -n "$m" ]; then set -- --model "$m" "$@"; fi` + "\n" +
		"  GEMINI_API_KEY=" + q(key) + " GOOGLE_GEMINI_BASE_URL=" + q(gw) + ` command agy "$@"` + "\n}"
}

// agyCustom reads customModels as it is, raw JSON by key.
func agyCustom(path string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	raw, err := edit.Read(path)
	if err != nil || len(raw) == 0 {
		return out
	}
	gjson.GetBytes(jsonc.ToJSONInPlace(raw), agyModels).ForEach(func(k, v gjson.Result) bool {
		out[k.String()] = json.RawMessage(v.Raw)
		return true
	})
	return out
}

func agy(home string) *Agent { return agyIn(here(home)) }

// agyIn is agy at a place: this machine's home, or a WSL distro's, whose
// shells get a function of their own (agyDistroFunction).
func agyIn(at place) *Agent {
	home := at.home
	id := "agy"
	if at.id != "" {
		id = at.id
	}
	dir := at.open(filepath.Join(home, ".gemini", "antigravity-cli"))
	path := filepath.Join(dir, "settings.json")
	get := jsonGet(path, "model")
	provider := jsonGet(path, "modelProvider")
	// setModels writes customModels with the user's own and, when with,
	// magpie's; none left takes the key out
	setModels := func(with map[string]any) error {
		ms := map[string]any{}
		for k, v := range agyCustom(path) {
			if !strings.HasPrefix(k, magpieID+"/") {
				ms[k] = v
			}
		}
		for k, v := range with {
			ms[k] = v
		}
		if len(ms) > 0 {
			return edit.SetJSON(path, edit.KV{Path: agyModels, Value: ms})
		}
		if err := edit.DelJSON(path, agyModels); err != nil {
			return err
		}
		if v, ok := edit.GetJSON(path, "customModelsConfig"); ok && strings.TrimSpace(v) == "{}" {
			return edit.DelJSON(path, "customModelsConfig")
		}
		return nil
	}
	// the function agy, in this machine's shells or the distro's
	distro := at.sys != nil
	gw, key := at.gw, func() string { return agentKeyAt("agy", at.gw()) }
	shell := func(on bool) {
		if !distro {
			agyShell(home, on)
			return
		}
		if on {
			k, g := key(), gw()
			update.ShellFunctionAt(agyDistroProfiles(at), "agy", func(kind string) string { return agyDistroFunction(kind, k, g) })
		} else {
			update.DropShellFunctionAt(agyDistroProfiles(at), "agy")
		}
	}
	dropShell := func() {
		var err error
		if distro {
			err = update.DropShellFunctionAt(agyDistroProfiles(at), "agy")
		} else if here, _ := homes(); here == home {
			err = update.DropShellFunction("agy")
		}
		if err != nil {
			log.Printf("agy shell function cleanup: %v", err)
		}
	}
	shellIn := func() []string {
		if !distro {
			return update.ShellFunctionIn("agy")
		}
		return update.ShellFunctionInAt(agyDistroProfiles(at), "agy")
	}
	launch := func(model string) string {
		if !distro {
			return AgyLaunch(model)
		}
		return agyLaunchAt(gw(), key(), model, false)
	}
	magpiePick := func(v string) bool {
		ref, ok := strings.CutPrefix(v, magpieID+"/")
		return ok && isMagpie(ref)
	}
	// The snapshot owns these two fields until cleanup commits. A native
	// /model pick changes only model, so it cannot also mean ownership ended.
	change := func(v, how string) error {
		on := magpiePick(v)
		var models map[string]any
		if on || how == "sync" {
			models = map[string]any{}
			for _, m := range sharedModels("agy") {
				models[magpieID+"/"+m.ID] = map[string]string{"apiProvider": "API_PROVIDER_GOOGLE_GEMINI", "modelName": m.ID}
			}
		}
		stashMu.Lock()
		defer stashMu.Unlock()
		// Follow checks under the lock, before Atomically: with nothing
		// to take out, or an unreadable file, stash or snapshot (unknown),
		// it writes nothing, and puts nothing back.
		if how == "follow" {
			raw, err := edit.Read(path)
			m, merr := stashLoadKnown()
			settings := gjson.ParseBytes(jsonc.ToJSON(raw))
			if err != nil || merr != nil || !edit.ValidJSONC(raw) || !settings.IsObject() || !agyLeft(path, settings, m) {
				return errAgyStays
			}
			if _, _, err := agyBefore(m, path, id, false); err != nil {
				return errAgyStays
			}
		}
		// the shell function goes once every write has: one that fails puts
		// settings back on magpie, which still needs the function's key
		stayed, drop := false, false
		err := edit.Atomically(func() error {
			raw, err := edit.Read(path)
			if err != nil {
				return err
			}
			// A missing file is known empty; malformed bytes are unknown.
			settings := gjson.ParseBytes(jsonc.ToJSON(raw))
			if len(bytes.TrimSpace(raw)) > 0 && (!edit.ValidJSONC(raw) || !settings.IsObject()) {
				return fmt.Errorf("%s: invalid settings JSON object", path)
			}
			cur := settings.Get("model").String()
			if how == "sync" && usesMagpie(cur) {
				return setModels(models)
			}
			m, err := stashLoadKnown()
			if err != nil {
				return err
			}
			// Older versions had no explicit snapshot for absent originals.
			// Their applied record proves ownership only beside their writes;
			// a new Connect's .connect.was record predates its first write.
			hasMagpieModels := agyHasMagpieModels(path)
			legacy := usesMagpie(cur) || hasMagpieModels
			before, owned, err := agyBefore(m, path, id, legacy)
			if err != nil {
				return err
			}
			_, hasSnapshot := m["agy:"+path+":before"]
			_, hasLegacyModel := m["agy:"+path+":model"]
			_, hasLegacyProvider := m["agy:"+path+":modelProvider"]
			hasOriginal := hasSnapshot || hasLegacyModel || hasLegacyProvider
			// the file as read again: agy, or another magpie, may have
			// written it since
			if how == "follow" && !agyLeft(path, settings, m) {
				stayed = true
				return nil
			}
			// A magpie model or custom-model entry is sufficient ownership
			// for cleanup, not evidence of the original provider value.
			owned = owned || legacy
			if on {
				delete(m, "agy:"+path+":provider-unknown")
				if !hasOriginal {
					before = map[string]json.RawMessage{}
					if !usesMagpie(cur) {
						for _, k := range []string{"model", "modelProvider"} {
							if value := settings.Get(k); value.Exists() {
								before[k] = json.RawMessage(value.Raw)
							}
						}
					}
				}
				b, _ := json.Marshal(before)
				m["agy:"+path+":before"] = string(b)
				if err := stashWrite(m); err != nil {
					return err
				}
				if err := setModels(models); err != nil {
					return err
				}
				return edit.SetJSON(path, edit.KV{Path: "modelProvider", Value: "gemini"}, edit.KV{Path: "model", Value: v})
			}
			if how != "pick" && !owned && !usesMagpie(cur) && !hasMagpieModels {
				drop = true
				return nil
			}
			providerOriginalKnown := hasSnapshot || hasLegacyProvider || hasLegacyModel
			var set []edit.KV
			var del []string
			restore := func(k string) {
				if value, ok := before[k]; ok {
					set = append(set, edit.KV{Path: k, Value: value})
				} else {
					del = append(del, k)
				}
			}
			if providerOriginalKnown && settings.Get("modelProvider").String() == "gemini" {
				restore("modelProvider")
			}
			switch {
			case v != "":
				set = append(set, edit.KV{Path: "model", Value: v})
			case owned && usesMagpie(cur):
				restore("model")
			case !owned && (usesMagpie(cur) || how == "pick"):
				// Without originals, remove a disconnected magpie model,
				// but leave the provider alone.
				del = append(del, "model")
			}
			if err := setModels(nil); err != nil {
				return err
			}
			raw, err = edit.Read(path)
			if err != nil {
				return err
			}
			out, err := edit.PatchJSON(raw, set, del)
			if err != nil {
				return err
			}
			if err := edit.WriteAtomic(path, out); err != nil {
				return err
			}
			drop = true
			if legacy && !providerOriginalKnown && settings.Get("modelProvider").String() == "gemini" {
				m["agy:"+path+":provider-unknown"] = "1"
			} else if legacy || providerOriginalKnown {
				delete(m, "agy:"+path+":provider-unknown")
			}
			if !owned {
				return nil
			}
			delete(m, "agy:"+path+":before")
			delete(m, "agy:"+path+":model")
			delete(m, "agy:"+path+":modelProvider")
			delete(m, id+".connect.was")
			if how == "follow" {
				// what Disconnect records, in this one write under the
				// lock: a Pick waiting on it comes after all of it
				m[id+disconnectedKey] = "1"
				b, _ := json.Marshal(reconnection{Magpie: map[string]string{}, Left: map[string]string{"model": cur}})
				m[id+".reconnect"] = string(b)
			}
			if err := stashWrite(m); err != nil {
				return err
			}
			if how == "follow" {
				forgetSetUpOf(id)
			}
			return nil
		}, path, stashPath())
		if err == nil && drop {
			dropShell()
		}
		if err == nil && stayed {
			return errAgyStays
		}
		return err
	}
	unwire := func() error { return change("", "disconnect") }
	owned := func() bool {
		stashMu.Lock()
		defer stashMu.Unlock()
		m, err := stashLoadKnown()
		if err != nil {
			return !at.cold && usesMagpie(get())
		}
		_, pending, err := agyBefore(m, path, id, false)
		return pending || err != nil
	}
	a := &Agent{
		ID: "agy", Name: "Antigravity CLI", Icon: "antigravity-color", Aliases: []string{"antigravity-cli"}, Spelled: prefixed, NoNames: true,
		Bin: "agy", Dir: dir, Path: path,
		Launch: func() string {
			if v := get(); usesMagpie(v) {
				return launch(v)
			}
			return ""
		},
		Typed: func() string {
			if !usesMagpie(get()) || len(shellIn()) == 0 {
				return ""
			}
			return "agy"
		},
		Owned:  owned,
		Unwire: unwire,
		Sync: func() error {
			if err := change("", "sync"); err != nil {
				return err
			}
			if usesMagpie(get()) {
				shell(true)
			}
			return nil
		},
		Notice: func() string {
			v := get()
			if !usesMagpie(v) {
				if agyProviderUnknown(path) && provider() == "gemini" {
					return noticeAgyProviderUnknown.say("path", at.native(path))
				}
				return ""
			}
			if in := shellIn(); len(in) > 0 {
				for i := range in {
					if distro {
						in[i] = at.native(in[i])
					} else {
						in[i] = update.TildeHome(in[i])
					}
				}
				return noticeAgyTyped.say("profiles", strings.Join(in, ", "), "command", launch(v))
			}
			return noticeAgy.say("command", launch(v))
		},
		Check: func() string {
			v := get()
			if !usesMagpie(v) {
				return ""
			}
			if _, ok := agyCustom(path)[v]; !ok {
				return "Antigravity CLI's custom model " + v + " (settings.json) is gone, so it no longer reaches magpie"
			}
			if p := provider(); p != "gemini" {
				return "Antigravity CLI's modelProvider (settings.json) is " + orDefault(p) + ", not gemini, so it asks Google directly"
			}
			// a distro's function names the gateway's address: one WSL
			// changed on a restart (NAT) no longer reaches it
			if distro {
				for _, p := range shellIn() {
					if b, err := os.ReadFile(p); err == nil && !strings.Contains(string(b), "GOOGLE_GEMINI_BASE_URL='"+gw()+"'") {
						return "Antigravity CLI's function agy in " + at.native(p) + " names magpie at an address it no longer has"
					}
				}
			}
			return ""
		},
		Fields: []Field{{
			Key: "model", Label: "model",
			Get: get,
			Set: func(v string) error {
				if err := change(v, "pick"); err != nil {
					return err
				}
				if magpiePick(v) {
					shell(true)
				}
				return nil
			},
			Options: func(cur map[string]string) []Option {
				custom := agyCustom(path)
				// its own say they go to their provider, not through magpie,
				// as every agent's own do (EZN7L2C3, #834)
				var own []Option
				for k := range custom {
					if !strings.HasPrefix(k, magpieID+"/") {
						to := "Google"
						if p := gjson.GetBytes(custom[k], "apiProvider").String(); strings.Contains(p, "ANTHROPIC") {
							to = "Anthropic"
						} else if strings.Contains(p, "OPENAI") {
							to = "OpenAI"
						}
						own = append(own, Option{Value: k, Icon: modelIcon("", gjson.GetBytes(custom[k], "modelName").String()), Direct: to})
					}
				}
				sort.Slice(own, func(i, j int) bool { return own[i].Value < own[j].Value })
				if c := cur["model"]; c != "" && !usesMagpie(c) && custom[c] == nil {
					own = append([]Option{{Value: c, Icon: modelIcon("", c), Direct: "Google"}}, own...)
				}
				return withVia(group("Antigravity CLI", own), "agy", magpieID+"/")
			},
		}},
	}
	// agy's own /model changes only model: magpie's provider, custom
	// models and shell function stay, and its next start, given no
	// GEMINI_API_KEY, fails. Once the page looks (or `magpie agy run` or
	// `env` starts agy as it is), that connection is taken out as
	// Disconnect does, the model picked there kept. A poll reads what is
	// held and takes the stash lock only once a saved original and a
	// leftover of magpie's are both there; an unreadable stash or
	// snapshot is unknown, and nothing is written.
	a.Follow = func() error {
		if at.cold || usesMagpie(get()) {
			return nil
		}
		custom, _ := edit.GetJSON(path, agyModels)
		if provider() != "gemini" && !agyMagpieModels(gjson.Parse(custom)) || !agySaved(stashShared(), path) {
			return nil
		}
		err := change("", "follow")
		if errors.Is(err, errAgyStays) {
			return nil
		}
		a.rewired(err)
		return err
	}
	return a
}

// errAgyStays is Follow's change finding, under the stash lock, nothing
// agy's own /model left: on magpie again, or already tidied.
var errAgyStays = errors.New("agy has nothing of magpie's to take out")

// agyLeft says what agy's own /model left at path: its original saved,
// a model of agy's own, and magpie's provider or custom models still in
// settings.
func agyLeft(path string, settings gjson.Result, m map[string]string) bool {
	return agySaved(m, path) && !usesMagpie(settings.Get("model").String()) &&
		(settings.Get("modelProvider").String() == "gemini" || agyMagpieModels(settings.Get(agyModels)))
}

// agySaved says the stash m has path's original: the snapshot, or an old
// per-field entry.
func agySaved(m map[string]string, path string) bool {
	for _, k := range []string{"before", "model", "modelProvider"} {
		if _, ok := m["agy:"+path+":"+k]; ok {
			return true
		}
	}
	return false
}

// agyMagpieModels says customModels has one of magpie's.
func agyMagpieModels(custom gjson.Result) bool {
	found := false
	custom.ForEach(func(k, _ gjson.Result) bool {
		found = strings.HasPrefix(k.String(), magpieID+"/")
		return !found
	})
	return found
}

// agyBefore reads the explicit snapshot, or the old per-field stash. Old
// versions left no keys for absent originals; an applied record
// still proves that magpie owns that connection. Empty legacy values were
// never recorded, so only the new snapshot can distinguish them from absent.
func agyBefore(m map[string]string, path, id string, legacy bool) (map[string]json.RawMessage, bool, error) {
	key := "agy:" + path + ":"
	if b, ok := m[key+"before"]; ok {
		var before map[string]json.RawMessage
		if err := json.Unmarshal([]byte(b), &before); err != nil {
			return nil, true, fmt.Errorf("agy snapshot: %w", err)
		}
		if before == nil {
			return nil, true, fmt.Errorf("agy snapshot is not a JSON object")
		}
		return before, true, nil
	}
	before := map[string]json.RawMessage{}
	owned := false
	for _, k := range []string{"model", "modelProvider"} {
		if value, ok := m[key+k]; ok {
			b, _ := json.Marshal(value)
			before[k], owned = b, true
		}
	}
	if !owned && legacy {
		owned = usesMagpie(appliedOf(id).Fields["model"])
	}
	return before, owned, nil
}

func isAgy(id string) bool { return id == "agy" || strings.HasPrefix(id, "agy@wsl:") }

func agyProviderUnknown(path string) bool {
	stashMu.Lock()
	defer stashMu.Unlock()
	m, err := stashLoadKnown()
	return err == nil && m["agy:"+path+":provider-unknown"] == "1"
}

// agyConnectKnown checks the owner before Connect's generic stash writes.
// Otherwise forget/connect.was could replace an unreadable stash before
// the field setter gets the chance to reject an unknown original.
func agyConnectKnown(a *Agent) error {
	stashMu.Lock()
	defer stashMu.Unlock()
	m, err := stashLoadKnown()
	if err != nil {
		return err
	}
	_, _, err = agyBefore(m, a.Path, a.ID, false)
	return err
}

// what agy says after a change (notice.go)
var (
	noticeAgy                = newNotice("agy takes magpie's gateway and model only from how it is started — start it with {command}")
	noticeAgyTyped           = newNotice("Type agy in a new terminal: magpie added it to {profiles}, to start agy on magpie's gateway and model (in a terminal open now: {command}).")
	noticeAgyProviderUnknown = newNotice("magpie cannot tell whether modelProvider gemini in {path} was your original setting. If agy cannot start because GEMINI_API_KEY is missing, remove modelProvider from settings.json yourself.")
)
