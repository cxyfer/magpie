package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// agy here is one that takes the gateway unless a test says otherwise:
// the agy on PATH is never asked
func init() { agyVersion = func() string { return agyGatewaySince } }

// agyVersionIs holds this machine's agy at version v ("" not known).
func agyVersionIs(t *testing.T, v string) {
	t.Helper()
	was := agyVersion
	agyVersion = func() string { return v }
	t.Cleanup(func() { agyVersion = was })
}

func agyTestHome(t *testing.T) (home, dir, settings, mark string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if err := provider.Save(provider.Provider{ID: "deepseek", Name: "DeepSeek", Chat: "https://api.deepseek.com/v1", Key: "k", Models: []string{"pro", "flash"}}); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, dir, filepath.Join(dir, "settings.json"), agyStateKey(filepath.Join(dir, "settings.json"))
}

func agyFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func agyWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func agyMarkOf(t *testing.T, key string) (model, restoreModel, restoreProvider string, ok bool) {
	t.Helper()
	s := agyReadState(key)
	rm := gjson.ParseBytes(s.Before["model"]).String()
	rp := ""
	if s.ProviderRemoved {
		rp = gjson.ParseBytes(s.Before["modelProvider"]).String()
	}
	return s.Model, rm, rp, s.On()
}
func agyWriteStateJSON(t *testing.T, key, body string) {
	t.Helper()
	var s agyState
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	s.Before = map[string]json.RawMessage{}

	b, _ := json.Marshal(s)
	stash(map[string]string{key: string(b)})
}

// agy is on magpie without one byte of its settings changing: magpie's
// record in its stash says which model it starts on, and the launch
// command carries the Enterprise Gateway variables and a key that names
// agy. Picking another of magpie's models moves the record, and Default
// takes it out and gives agy's own model back.
func TestAgy(t *testing.T) {
	_, _, path, mark := agyTestHome(t)
	orig := `{
  "customModelsConfig": {
    "customModels": {
      "mine": {
        "apiProvider": "API_PROVIDER_GOOGLE_GEMINI",
        "modelName": "gemini-3-pro"
      }
    }
  },
  "model": "mine",
  "modelProvider": "vertex",
  "trustedWorkspaces": ["/w"]
}
`
	agyWrite(t, path, orig)
	a, err := Find("agy")
	if err != nil {
		t.Fatal(err)
	}
	if a.Launch() != "" || a.Typed() != "" {
		t.Fatalf("before: %q %q", a.Launch(), a.Typed())
	}
	if env, m, err := AgyStart(); err != nil || env != nil || m != "" {
		t.Fatalf("AgyStart before: %v %q %v", env, m, err)
	}
	if err := a.Apply("model", "magpie/deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	if got := agyFile(t, path); got != orig {
		t.Fatalf("agy's settings changed:\n%s", got)
	}
	if model, rm, rp, ok := agyMarkOf(t, mark); !ok || model != "deepseek/pro" || rm != "mine" || rp != "" {
		t.Fatalf("mark: %q %q %q %v", model, rm, rp, ok)
	}
	if !a.Wired() || a.Field("model").Get() != "magpie/deepseek/pro" || a.Drift() != nil {
		t.Fatalf("wired %v, model %q, drift %+v", a.Wired(), a.Field("model").Get(), a.Drift())
	}
	// Windows' command is TestAgyLaunchPowerShell's
	want := "AGY_LLM_GATEWAY_URL=" + gateway.URL() + " AGY_LLM_GATEWAY_API_KEY=" + gateway.TokenFor("agy") +
		" AGY_LLM_GATEWAY_WIRE_PROTOCOL=openai AGY_LLM_GATEWAY_MODELS= agy --model 'deepseek/pro'"
	if l := a.Launch(); runtime.GOOS != "windows" && l != want {
		t.Fatalf("launch: %q, want %q", l, want)
	}
	if !strings.Contains(a.Notice(), a.Launch()) || !strings.Contains(a.Notice(), "agy") {
		t.Fatalf("notice: %q", a.Notice())
	}
	// no native model is offered: they are agy's own, picked in its
	// /model after stepping out
	for _, o := range a.Field("model").Options(a.Values()) {
		if o.Value == "mine" || o.Ref == "" {
			t.Fatalf("a native model is offered: %+v", o)
		}
	}
	if err := a.Field("model").Set("mine"); err == nil {
		t.Fatal("a native model was accepted")
	}

	// another of magpie's keeps the user's own settings where they were
	if err := a.Apply("model", "magpie/deepseek/flash"); err != nil {
		t.Fatal(err)
	}
	if model, _, _, _ := agyMarkOf(t, mark); model != "deepseek/flash" {
		t.Fatalf("mark after switch: %q", model)
	}
	if got := agyFile(t, path); got != orig {
		t.Fatalf("settings after switch:\n%s", got)
	}

	// a model magpie no longer serves is drift, and Check says why
	agyWriteStateJSON(t, mark, `{"model":"gone/model"}`)
	if d := a.Drift(); d == nil || d.Kind != "unwired" || !strings.Contains(d.Detail, "no longer in magpie's catalog") {
		t.Fatalf("drift: %+v", d)
	}
	if err := a.Apply("model", "magpie/deepseek/pro"); err != nil {
		t.Fatal(err)
	}

	// Default: agy's own model back, magpie's record and function out
	if err := a.Apply("model", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := agyMarkOf(t, mark); ok {
		t.Fatal("mark left after Default")
	}
	if got := agyFile(t, path); got != orig {
		t.Fatalf("settings after Default:\n%s", got)
	}
	if a.Wired() || a.Launch() != "" || a.Typed() != "" {
		t.Fatalf("after Default: wired %v, launch %q, typed %q", a.Wired(), a.Launch(), a.Typed())
	}
	if m := a.Field("model").Get(); m != "mine" {
		t.Fatalf("model after Default: %q", m)
	}

	// a user's own modelProvider "gemini" would send agy's chats to
	// /v1beta; it is taken out while connected and put back on Default
	agyWrite(t, path, `{"model":"mine","modelProvider":"gemini"}`)
	if err := a.Apply("model", "magpie/deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	if p, set := edit.GetJSON(path, "modelProvider"); set {
		t.Fatalf("modelProvider left while connected: %q", p)
	}
	if model, rm, rp, _ := agyMarkOf(t, mark); model != "deepseek/pro" || rm != "mine" || rp != "gemini" {
		t.Fatalf("mark: %q %q %q", model, rm, rp)
	}
	if err := a.Apply("model", ""); err != nil {
		t.Fatal(err)
	}
	if p, _ := edit.GetJSON(path, "modelProvider"); p != "gemini" {
		t.Fatalf("modelProvider after Default: %q", p)
	}
	if m, _ := edit.GetJSON(path, "model"); m != "mine" {
		t.Fatalf("model after Default: %q", m)
	}
}

// The settings the old magpie wrote are cleaned, a magpie model left
// without its custom models too, and what the user had is restored. The old field stash is the record; a polluted one falls back
// to Connect's snapshot, and with no snapshot at all its magpie model is
// not taken for the user's.
func TestAgyMigrateOldSettings(t *testing.T) {
	const old = `{
  "customModelsConfig": {
    "customModels": {
      "mine": {"apiProvider": "API_PROVIDER_GOOGLE_GEMINI", "modelName": "gemini-3-pro"},
      "magpie/deepseek/pro": {"apiProvider": "API_PROVIDER_GOOGLE_GEMINI", "modelName": "deepseek/pro"}
    }
  },
  "model": "magpie/deepseek/pro",
  "modelProvider": "gemini",
  "trustedWorkspaces": ["/w"]
}

`

	type want struct {
		model, provider                      string
		modelSet, providerSet, mark          bool
		markModel, restoreModel, restoreProv string
		afterModel, afterProvider            string
		afterModelSet, afterProviderSet      bool
		cleaned                              bool
	}
	for _, c := range []struct {
		name, settings, origModel, origProvider, connectWas string
		want                                                want
	}{
		{
			name: "no original provider or model",
			want: want{mark: true, markModel: "deepseek/pro", cleaned: true},
		},
		{
			name: "the user's own gemini", origModel: "mine", origProvider: "gemini",
			want: want{model: "mine", modelSet: true, mark: true, markModel: "deepseek/pro", restoreModel: "mine", restoreProv: "gemini",
				afterModel: "mine", afterModelSet: true, afterProvider: "gemini", afterProviderSet: true, cleaned: true},
		},
		{
			name: "the user's own vertex", origModel: "gemini-3-pro", origProvider: "vertex",
			want: want{model: "gemini-3-pro", modelSet: true, provider: "vertex", providerSet: true,
				mark: true, markModel: "deepseek/pro", restoreModel: "gemini-3-pro",
				afterModel: "gemini-3-pro", afterModelSet: true, afterProvider: "vertex", afterProviderSet: true, cleaned: true},
		},
		{
			name:      "a polluted stash falls back to Connect's snapshot",
			origModel: "magpie/deepseek/pro", origProvider: "gemini", connectWas: `{"model":"mine"}`,
			want: want{model: "mine", modelSet: true, mark: true, markModel: "deepseek/pro", restoreModel: "mine", restoreProv: "gemini",
				afterModel: "mine", afterModelSet: true, afterProvider: "gemini", afterProviderSet: true, cleaned: true},
		},
		{
			name:      "a polluted stash with no snapshot",
			origModel: "magpie/deepseek/pro", origProvider: "gemini",
			want: want{mark: true, markModel: "deepseek/pro", restoreProv: "gemini", afterProvider: "gemini", afterProviderSet: true, cleaned: true},
		},
		{
			name: "the user moved to a model of their own", origModel: "mine", origProvider: "vertex",
			settings: strings.Replace(old, `"model": "magpie/deepseek/pro"`, `"model": "gemini-3-pro"`, 1),
			want:     want{model: "gemini-3-pro", modelSet: true, provider: "vertex", providerSet: true, cleaned: true},
		},
		{
			name: "external provider survives legacy cleanup", settings: strings.Replace(old, `"modelProvider": "gemini"`, `"modelProvider": "vertex"`, 1),
			want: want{provider: "vertex", providerSet: true, mark: true, markModel: "deepseek/pro", afterProvider: "vertex", afterProviderSet: true, cleaned: true},
		},
		{
			name:     "a magpie model with its custom models gone",
			settings: `{"model":"magpie/deepseek/pro","trustedWorkspaces":["/w"]}`,
			want:     want{mark: true, markModel: "deepseek/pro"},
		},
		{
			name:     "not the old wiring: no magpie custom model",
			settings: `{"model":"mine","modelProvider":"gemini","customModelsConfig":{"customModels":{"mine":{"apiProvider":"API_PROVIDER_GOOGLE_GEMINI","modelName":"gemini-3-pro"}}},"trustedWorkspaces":["/w"]}`,
			want:     want{model: "mine", modelSet: true, provider: "gemini", providerSet: true},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, path, mark := agyTestHome(t)
			settings := c.settings
			if settings == "" {
				settings = old
			}
			agyWrite(t, path, settings)
			stash(map[string]string{
				"agy:" + path + ":model":         c.origModel,
				"agy:" + path + ":modelProvider": c.origProvider,
			})
			if c.connectWas != "" {
				stash(map[string]string{"agy.connect.was": c.connectWas})
			}
			a, err := Find("agy")
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			model, modelSet := edit.GetJSON(path, "model")
			provider, providerSet := edit.GetJSON(path, "modelProvider")
			if model != c.want.model || modelSet != c.want.modelSet || provider != c.want.provider || providerSet != c.want.providerSet {
				t.Fatalf("settings: model %q (%v), provider %q (%v); want %q (%v), %q (%v)\n%s",
					model, modelSet, provider, providerSet, c.want.model, c.want.modelSet, c.want.provider, c.want.providerSet, agyFile(t, path))
			}
			if c.want.cleaned && (!strings.Contains(agyFile(t, path), `"mine"`) || strings.Contains(agyFile(t, path), "magpie/deepseek/pro")) {
				t.Fatalf("custom models:\n%s", agyFile(t, path))
			}
			if !strings.Contains(agyFile(t, path), `"/w"`) {
				t.Fatalf("another key was lost:\n%s", agyFile(t, path))
			}
			mm, rm, rp, ok := agyMarkOf(t, mark)
			if ok != c.want.mark || mm != c.want.markModel || rm != c.want.restoreModel || rp != c.want.restoreProv {
				t.Fatalf("mark %q %q %q (%v); want %q %q %q (%v)", mm, rm, rp, ok, c.want.markModel, c.want.restoreModel, c.want.restoreProv, c.want.mark)
			}
			if c.want.mark {
				env, m, err := AgyStart()
				if err != nil || m != "deepseek/pro" || !strings.Contains(strings.Join(env, " "), "AGY_LLM_GATEWAY_URL=") {
					t.Fatalf("AgyStart: %v %q %v", env, m, err)
				}
			} else if env, m, err := AgyStart(); err != nil || env != nil || m != "" {
				t.Fatalf("AgyStart off magpie: %v %q %v", env, m, err)
			}
			// the old field stash is spent; a later Default doesn't put
			// magpie's values back
			if _, ok := stashShared()["agy:"+path+":model"]; ok {
				t.Fatal("the old model stash is still there")
			}
			if !c.want.mark {
				return
			}
			if err := a.Apply("model", ""); err != nil {
				t.Fatal(err)
			}
			model, modelSet = edit.GetJSON(path, "model")
			provider, providerSet = edit.GetJSON(path, "modelProvider")
			if model != c.want.afterModel || modelSet != c.want.afterModelSet ||
				provider != c.want.afterProvider || providerSet != c.want.afterProviderSet {
				t.Fatalf("after Default: model %q (%v), provider %q (%v)", model, modelSet, provider, providerSet)
			}
		})
	}
}

// agy takes magpie's gateway from 1.4.12, the oldest tried. An older one,
// here or in a distro, is refused Connect and a pick with the update to
// make, its old wiring, which it runs on, left as it is by them and by a
// sync; once on magpie, Check says it. One whose version can't be read
// is connected as before, its notice saying so.
func TestAgyVersion(t *testing.T) {
	const old = `{"model":"magpie/deepseek/pro","modelProvider":"gemini","customModelsConfig":{"customModels":{"magpie/deepseek/pro":{"apiProvider":"API_PROVIDER_GOOGLE_GEMINI","modelName":"deepseek/pro"}}}}`
	refusal := "agy 1.4.11 can't start on magpie's gateway, which needs agy 1.4.12 or later — update agy first."
	for _, v := range []string{"1.4.11", ""} {
		t.Run("version "+v, func(t *testing.T) {
			_, _, path, mark := agyTestHome(t)
			agyVersionIs(t, v)
			agyWrite(t, path, old)
			a, err := Find("agy")
			if err != nil {
				t.Fatal(err)
			}
			d, _ := agyDistro(t, "")
			d.Mirrored, d.Versions = true, map[string]string{"agy": v}
			wa := wslAgent(wslKindOf("agy"), d)
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			errs := []error{a.Connect(), a.Pick("model", "magpie/deepseek/flash"), wa.Field("model").Set("magpie/deepseek/pro")}
			if v == "" {
				for _, err := range errs {
					if err != nil {
						t.Fatal(err)
					}
				}
				if !a.Wired() || agyOldWiring(path) || !strings.HasPrefix(a.Notice(), "magpie couldn't read agy's version; ") ||
					!strings.HasPrefix(wa.Notice(), "magpie couldn't read agy's version; ") {
					t.Fatalf("wired %v, notices %q %q:\n%s", a.Wired(), a.Notice(), wa.Notice(), agyFile(t, path))
				}
				return
			}
			for _, err := range errs {
				if err == nil || err.Error() != refusal {
					t.Fatalf("not refused: %v", err)
				}
			}
			if got := agyFile(t, path); got != old {
				t.Fatalf("the old wiring was changed:\n%s", got)
			}
			if _, _, _, ok := agyMarkOf(t, mark); ok || stashShared()["agy.connect.was"] != "" {
				t.Fatal("a record was made")
			}
			// on magpie from a newer agy, then the older one put back
			agyVersionIs(t, "1.5.0")
			if err := a.Pick("model", "magpie/deepseek/pro"); err != nil {
				t.Fatal(err)
			}
			agyVersionIs(t, v)
			if got := a.Check(); got != refusal {
				t.Fatalf("check: %q", got)
			}
		})
	}
}

// Check says what keeps agy off magpie: the old wiring left in its
// settings, modelProvider gemini taking its chats to /v1beta, a model
// magpie no longer serves, or magpie's own record unreadable. Sync repairs
// the first three; an unreadable record is left for a pick.
func TestAgyCheck(t *testing.T) {
	_, _, path, mark := agyTestHome(t)
	agyWrite(t, path, `{"model":"magpie/deepseek/pro","modelProvider":"gemini","customModelsConfig":{"customModels":{"magpie/deepseek/pro":{"apiProvider":"API_PROVIDER_GOOGLE_GEMINI","modelName":"deepseek/pro"}}}}`)
	agyWriteStateJSON(t, mark, `{"model":"deepseek/pro"}`)
	a, err := Find("agy")
	if err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); !strings.Contains(c, "old magpie's") {
		t.Fatalf("old wiring: %q", c)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); c != "" {
		t.Fatalf("after Sync: %q\n%s", c, agyFile(t, path))
	}
	// the old magpie's custom models are the old wiring under a
	// modelProvider the user moved to since too, as Sync cleans them
	agyWrite(t, path, `{"modelProvider":"vertex","customModelsConfig":{"customModels":{"magpie/deepseek/pro":{"apiProvider":"API_PROVIDER_GOOGLE_GEMINI","modelName":"deepseek/pro"}}}}`)
	if c := a.Check(); !strings.Contains(c, "old magpie's") {
		t.Fatalf("old custom models under vertex: %q", c)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); c != "" || strings.Contains(agyFile(t, path), "magpie/") {
		t.Fatalf("after Sync: %q\n%s", c, agyFile(t, path))
	}
	// a modelProvider gemini set again while connected is drift, and Sync
	// takes it out
	if err := edit.SetJSON(path, edit.KV{Path: "modelProvider", Value: "gemini"}); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); !strings.Contains(c, "modelProvider gemini") {
		t.Fatalf("gemini while connected: %q", c)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); c != "" {
		t.Fatalf("after Sync: %q", c)
	}
	// a model magpie no longer serves
	agyWriteStateJSON(t, mark, `{"model":"gone/model"}`)
	if c := a.Check(); !strings.Contains(c, "no longer in magpie's catalog") {
		t.Fatalf("gone model: %q", c)
	}
	// an unreadable record is unknown, not "off"
	stashMu.Lock()
	m, _ := stashLoadKnown()
	m[mark] = "not-json"
	_ = stashWrite(m)
	stashMu.Unlock()
	if c := a.Check(); !strings.Contains(c, "can't be read") {
		t.Fatalf("unreadable record: %q", c)
	}
	if err := a.Sync(); err == nil {
		t.Fatal("Sync accepted an unreadable record")
	}
	// a stash that can't be read at all: the old wiring is still told,
	// and refused a sync; with none, a sync leaves both as they are
	os.WriteFile(stashPath(), []byte("{"), 0o600)
	agyWrite(t, path, `{"model":"magpie/deepseek/pro","trustedWorkspaces":["/w"]}`)
	if c := a.Check(); !strings.Contains(c, "old magpie's") {
		t.Fatalf("old model under an unreadable stash: %q", c)
	}
	if err := a.Sync(); err == nil {
		t.Fatal("Sync said nothing of the old wiring it can't clean over an unreadable stash")
	}
	agyWrite(t, path, `{"model":"mine"}`)
	if err := a.Sync(); err != nil {
		t.Fatalf("Sync over an unreadable stash: %v", err)
	}
	if agyFile(t, path) != `{"model":"mine"}` || agyFile(t, stashPath()) != "{" {
		t.Fatalf("written over an unreadable stash:\n%s\n%s", agyFile(t, path), agyFile(t, stashPath()))
	}
}

// Disconnect restores a provider only while it remains absent, and never
// rewrites a model the user changed after connecting.
func TestAgyGatewayRestore(t *testing.T) {
	cases := []struct {
		name, before, changed, want string
	}{
		{"absent", `{}`, ``, `{}`},
		{"own gemini", `{"model":"mine","modelProvider":"gemini"}`, ``, `{"model":"mine","modelProvider":"gemini"}`},
		{"native pick", `{"model":"mine","modelProvider":"gemini"}`, `{"model":"native"}`, `{"model":"native","modelProvider":"gemini"}`},
		{"changed provider", `{"modelProvider":"gemini"}`, `{"modelProvider":"vertex"}`, `{"modelProvider":"vertex"}`},
		{"empty provider", `{"modelProvider":"gemini"}`, `{"modelProvider":""}`, `{"modelProvider":""}`},
		// agy 1.4.12 does not write modelProvider in Enterprise Gateway mode;
		// a value that appears while connected is therefore an external edit.
		{"new gemini", `{"modelProvider":"vertex"}`, `{"modelProvider":"gemini"}`, `{"modelProvider":"gemini"}`},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, _, path, _ := agyTestHome(t)
			agyWrite(t, path, c.before)
			// the Antigravity app's settings, once: the CLI's wiring never
			// writes them
			desktop := filepath.Join(home, ".antigravity", "settings.json")
			const app = `{"gateway":{"enabled":true,"url":"http://desktop.example","apiKey":"desktop-key"}}`
			if i == 0 {
				if err := os.MkdirAll(filepath.Dir(desktop), 0o700); err != nil {
					t.Fatal(err)
				}
				agyWrite(t, desktop, app)
			}
			a := agy(home)
			if err := a.Connect(); err != nil {
				t.Fatal(err)
			}
			if err := a.Apply("model", "magpie/deepseek/flash"); err != nil {
				t.Fatal(err)
			}
			if c.changed != "" {
				agyWrite(t, path, c.changed)
			}
			if err := a.Disconnect(); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := a.Sync(); err != nil {
					t.Fatal(err)
				}
			}
			var got, want map[string]any
			if err := json.Unmarshal([]byte(agyFile(t, path)), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(c.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("settings %v, want %v", got, want)
			}
			if a.Wired() || agyReadState(agyStateKey(path)).On() {
				t.Fatal("connection remains after Disconnect")
			}
			if i == 0 && agyFile(t, desktop) != app {
				t.Fatal("Antigravity desktop settings changed")
			}
		})
	}
}

// An agy whose model magpie no longer serves is still on magpie: magpie's
// record says so, not the catalog. Disconnect, which a quit's and
// uninstall's disconnect are, takes it out all the same: the provider
// magpie took out back, its record and function gone. Old settings on a
// model gone are cleaned, and leave no record behind.
func TestAgyDisconnectModelGone(t *testing.T) {
	gone := func(t *testing.T) {
		t.Helper()
		if err := provider.Delete("deepseek"); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("on the gateway", func(t *testing.T) {
		home, _, path, mark := agyTestHome(t)
		mine := "alias ll='ls -l'\n"
		agyWrite(t, filepath.Join(home, ".bashrc"), mine)
		agyWrite(t, path, `{"model":"mine","modelProvider":"gemini"}`)
		a := agy(home)
		if err := a.Apply("model", "magpie/deepseek/pro"); err != nil {
			t.Fatal(err)
		}
		gone(t)
		if a.Wired() || !a.Owned() {
			t.Fatalf("wired %v, owned %v", a.Wired(), a.Owned())
		}
		if err := a.Reapply(); err == nil || !strings.Contains(err.Error(), "no longer serves deepseek/pro") {
			t.Fatalf("Reapply: %v", err)
		}
		if err := a.Disconnect(); err != nil {
			t.Fatal(err)
		}
		if _, ok := stashShared()[mark]; ok || a.Owned() {
			t.Fatalf("magpie's record left: %q", stashShared()[mark])
		}
		if m, _ := edit.GetJSON(path, "model"); m != "mine" {
			t.Fatalf("model after Disconnect: %q", m)
		}
		if p, _ := edit.GetJSON(path, "modelProvider"); p != "gemini" {
			t.Fatalf("modelProvider after Disconnect: %q\n%s", p, agyFile(t, path))
		}
		if env, m, err := AgyStart(); err != nil || env != nil || m != "" {
			t.Fatalf("AgyStart after Disconnect: %v %q %v", env, m, err)
		}
		if got := agyFile(t, filepath.Join(home, ".bashrc")); got != mine {
			t.Fatalf(".bashrc after Disconnect:\n%s", got)
		}
	})
	t.Run("old settings", func(t *testing.T) {
		home, _, path, mark := agyTestHome(t)
		agyWrite(t, path, `{"model":"magpie/deepseek/pro","modelProvider":"gemini","customModelsConfig":{"customModels":{"magpie/deepseek/pro":{"apiProvider":"API_PROVIDER_GOOGLE_GEMINI","modelName":"deepseek/pro"}}}}`)
		stash(map[string]string{"agy:" + path + ":model": "mine", "agy:" + path + ":modelProvider": "vertex"})
		gone(t)
		a := agy(home)
		if a.Wired() || !a.Owned() {
			t.Fatalf("wired %v, owned %v", a.Wired(), a.Owned())
		}
		if err := a.Disconnect(); err != nil {
			t.Fatal(err)
		}
		if _, ok := stashShared()[mark]; ok || a.Owned() {
			t.Fatalf("a record was made: %q", stashShared()[mark])
		}
		m, _ := edit.GetJSON(path, "model")
		p, _ := edit.GetJSON(path, "modelProvider")
		if m != "mine" || p != "vertex" || strings.Contains(agyFile(t, path), "magpie/") {
			t.Fatalf("settings after Disconnect:\n%s", agyFile(t, path))
		}
	})
}

func TestAgyUnknownStateStopsEdits(t *testing.T) {
	home, _, path, key := agyTestHome(t)
	agyWrite(t, path, `{"modelProvider":"gemini"}`)
	a := agy(home)
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	stashMu.Lock()
	m, err := stashLoadKnown()
	if err != nil {
		stashMu.Unlock()
		t.Fatal(err)
	}
	m[key] = "{" // present but unparseable state is unknown, not disconnected
	if err := stashWrite(m); err != nil {
		stashMu.Unlock()
		t.Fatal(err)
	}
	stashMu.Unlock()
	settings, stashBefore := agyFile(t, path), agyFile(t, stashPath())
	for name, run := range map[string]func() error{
		"connect":    a.Connect,
		"disconnect": a.Disconnect,
		"sync":       a.Sync,
		"pick":       func() error { return a.Apply("model", "magpie/deepseek/pro") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("operation accepted unknown state")
			}
			if agyFile(t, path) != settings || agyFile(t, stashPath()) != stashBefore {
				t.Fatal("unknown state was overwritten")
			}
		})
	}
}
