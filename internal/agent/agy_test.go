package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/filememo"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

func TestAgy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if err := provider.Save(provider.Provider{ID: "deepseek", Name: "DeepSeek", Chat: "https://api.deepseek.com/v1", Key: "k", Models: []string{"pro", "flash"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	// the user's own: a model of theirs on their own Gemini key, and a
	// setting magpie has nothing to do with
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
	os.WriteFile(path, []byte(orig), 0o644)
	read := func() map[string]any {
		t.Helper()
		b, _ := os.ReadFile(path)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%v\n%s", err, b)
		}
		return m
	}
	a, err := Find("agy")
	if err != nil || a.Path != path {
		t.Fatalf("find: %v %v", a, err)
	}
	if a.Launch() != "" {
		t.Fatalf("launch while on the user's own model: %q", a.Launch())
	}
	if err := a.Apply("model", "magpie/deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	m := read()
	if m["model"] != "magpie/deepseek/pro" || m["modelProvider"] != "gemini" {
		t.Fatalf("not wired: %v", m)
	}
	cm := m["customModelsConfig"].(map[string]any)["customModels"].(map[string]any)
	want := map[string]any{"apiProvider": "API_PROVIDER_GOOGLE_GEMINI", "modelName": "deepseek/pro"}
	if got, _ := json.Marshal(cm["magpie/deepseek/pro"]); string(got) != agyJSON(want) {
		t.Fatalf("custom model: %s", got)
	}
	if cm["magpie/deepseek/flash"] == nil || cm["mine"] == nil {
		t.Fatalf("custom models: %v", cm)
	}
	if ws, _ := m["trustedWorkspaces"].([]any); len(ws) != 1 {
		t.Fatalf("other keys lost: %v", m)
	}
	if d := a.Drift(); d != nil {
		t.Fatalf("drift: %+v", d)
	}
	l := a.Launch()
	if want := "GEMINI_API_KEY=" + gateway.TokenFor("agy") + " GOOGLE_GEMINI_BASE_URL=" + gateway.URL() + " agy --model 'magpie/deepseek/pro'"; runtime.GOOS != "windows" && l != want {
		t.Fatalf("launch: %q", l)
	}
	if n := a.Notice(); !strings.Contains(n, l) {
		t.Fatalf("notice: %q", n)
	}
	// another of magpie's keeps the user's stashed
	if err := a.Apply("model", "magpie/deepseek/flash"); err != nil {
		t.Fatal(err)
	}
	// something else sets modelProvider back: agy asks Google directly
	os.WriteFile(path, []byte(strings.Replace(agyRead(t, path), `"gemini"`, `"vertex"`, 1)), 0o644)
	if d := a.Drift(); d == nil || d.Kind != "unwired" {
		t.Fatalf("drift: %+v", d)
	}
	// the options: the user's own, then magpie's
	opts := a.Field("model").Options(a.Values())
	if len(opts) == 0 || opts[0].Value != "mine" || opts[0].Group != "Antigravity CLI" {
		t.Fatalf("options: %+v", opts)
	}
	// as installed: the user's model and provider back, magpie's models out
	if err := a.Apply("model", ""); err != nil {
		t.Fatal(err)
	}
	var back, was map[string]any
	json.Unmarshal([]byte(agyRead(t, path)), &back)
	json.Unmarshal([]byte(orig), &was)
	if agyJSON(back) != agyJSON(was) {
		t.Fatalf("not restored:\n%s", agyRead(t, path))
	}
	if a.Launch() != "" {
		t.Fatal("launch after reset")
	}

	// with nothing of the user's, reset leaves nothing of magpie's
	os.WriteFile(path, []byte("{\n  \"trustedWorkspaces\": []\n}\n"), 0o644)
	a.Apply("model", "magpie/deepseek/pro")
	a.Apply("model", "")
	if got := strings.TrimSpace(agyRead(t, path)); got != "{\n  \"trustedWorkspaces\": []\n}" {
		t.Fatalf("left behind:\n%s", got)
	}
}

func agyJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func agyRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// agy's /model changes only model. Connection ownership must outlive it,
// including when Connect writes its generic record again before the Set.
func TestAgyRestoreConnection(t *testing.T) {
	for _, c := range []struct {
		name, original, want, action, reconnect, changed string
		native, legacy, wsl, unowned, lostSnapshot       bool
	}{
		{name: "native-default", original: `{}`, native: true, want: `{"model":"native"}`},
		{name: "native-reconnect", original: `{"model":"mine","modelProvider":"vertex"}`, native: true, reconnect: "apply", want: `{"model":"mine","modelProvider":"vertex"}`},
		{name: "native-reapply", original: `{"model":"mine","modelProvider":"vertex"}`, native: true, reconnect: "reapply", want: `{"model":"mine","modelProvider":"vertex"}`},
		{name: "changed-provider", original: `{}`, changed: "vertex", action: "disconnect", want: `{"modelProvider":"vertex"}`},
		{name: "own-gemini", original: `{"modelProvider":"gemini"}`, want: `{"modelProvider":"gemini"}`},
		{name: "absent-originals", original: `{}`, want: `{}`},
		{name: "empty-originals", original: `{"model":"","modelProvider":""}`, want: `{"model":"","modelProvider":""}`},
		{name: "legacy-stash", original: `{"model":"mine","modelProvider":"vertex"}`, legacy: true, native: true, reconnect: "apply", want: `{"model":"mine","modelProvider":"vertex"}`},
		{name: "native-sync", original: `{"modelProvider":"gemini"}`, native: true, action: "sync", want: `{"model":"native","modelProvider":"gemini"}`},
		{name: "native-default-absent", original: `{"model":"mine"}`, native: true, action: "disconnect", want: `{}`},
		{name: "wsl-native-disconnect", original: `{"model":"mine","modelProvider":"vertex"}`, native: true, wsl: true, action: "disconnect", want: `{"model":"native","modelProvider":"vertex"}`},
		{name: "unowned-gemini", original: `{"model":"native","modelProvider":"gemini"}`, unowned: true, want: `{"modelProvider":"gemini"}`},
		{name: "unowned-disconnect", original: `{"model":"native","modelProvider":"gemini"}`, unowned: true, action: "disconnect", want: `{"model":"native","modelProvider":"gemini"}`},
		{name: "lost-snapshot-disconnect", original: `{}`, lostSnapshot: true, action: "disconnect", want: `{"modelProvider":"gemini"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := syncHome(t)
			a := agy(home)
			if c.wsl {
				d, _ := agyDistro(t, "alias mine=true\n")
				a = wslAgent(wslKindOf("agy"), d)
				home = d.local(d.Home)
			} else {
				writeFile(t, filepath.Join(home, ".bashrc"), "alias mine=true\n")
			}
			// Every row also guards settings and custom models magpie doesn't own.
			withOwn := func(fields string) map[string]any {
				var m map[string]any
				if err := json.Unmarshal([]byte(fields), &m); err != nil {
					t.Fatal(err)
				}
				m["trustedWorkspaces"] = []string{"/w"}
				m["customModelsConfig"] = map[string]any{"customModels": map[string]any{"mine": map[string]string{"modelName": "gemini-3-pro"}}}
				return m
			}
			writeFile(t, a.Path, agyJSON(withOwn(c.original)))
			if !c.unowned {
				if _, err := a.ConnectHow(); err != nil {
					t.Fatal(err)
				}
			}
			if c.legacy {
				forget("agy:" + a.Path + ":before")
				stash(map[string]string{"agy:" + a.Path + ":model": "mine", "agy:" + a.Path + ":modelProvider": "vertex"})
			}
			if c.lostSnapshot {
				a.Keep()
				forget("agy:"+a.Path+":before", a.ID+".connect.was")
			}
			if c.native {
				if err := edit.SetJSON(a.Path, edit.KV{Path: "model", Value: "native"}); err != nil {
					t.Fatal(err)
				}
				if c.name == "native-default-absent" {
					if err := edit.DelJSON(a.Path, "model"); err != nil {
						t.Fatal(err)
					}
				}
			}
			if c.changed != "" {
				if err := edit.SetJSON(a.Path, edit.KV{Path: "modelProvider", Value: c.changed}); err != nil {
					t.Fatal(err)
				}
			}
			if c.reconnect != "" {
				var err error
				if c.reconnect == "reapply" {
					err = a.Reapply()
				} else {
					err = a.Pick("model", "magpie/relay/glm-4.6")
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch c.action {
			case "sync":
				err = a.Sync()
			case "disconnect":
				err = a.Disconnect()
			default:
				err = a.Apply("model", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			// A second Sync must not mistake a restored gemini for magpie's.
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(agyRead(t, a.Path)), &got); err != nil {
				t.Fatal(err)
			}
			if want := withOwn(c.want); agyJSON(got) != agyJSON(want) {
				t.Fatalf("restored %s, want %s", agyJSON(got), agyJSON(want))
			}
			if got := agyRead(t, filepath.Join(home, ".bashrc")); got != "alias mine=true\n" {
				t.Fatalf("shell not restored: %q", got)
			}
		})
	}
}

// agy's own /model changes only model. Follow, as the page looks, takes
// out the rest of the connection as Disconnect does, the native model kept;
// without a saved original, or still on magpie, it leaves the file alone.
func TestAgyFollowAfterNativePick(t *testing.T) {
	for _, c := range []struct {
		name, original, want string
		lost, wsl, connected bool
	}{
		{name: "connected", original: `{"modelProvider":"vertex"}`, connected: true},
		{name: "absent-provider", original: `{}`, want: `{"model":"native"}`},
		{name: "own-provider", original: `{"modelProvider":"vertex"}`, want: `{"model":"native","modelProvider":"vertex"}`},
		{name: "no-snapshot", original: `{}`, lost: true},
		{name: "wsl", original: `{"modelProvider":"vertex"}`, want: `{"model":"native","modelProvider":"vertex"}`, wsl: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := syncHome(t)
			a := agy(home)
			running := true
			if c.wsl {
				d, dhome := agyDistro(t, "alias mine=true\n")
				d.up = func() bool { return running }
				a, home = wslAgent(wslKindOf("agy"), d), dhome
			}
			profile := filepath.Join(home, ".bashrc")
			writeFile(t, profile, "alias mine=true\n")
			writeFile(t, a.Path, c.original)
			if _, err := a.ConnectHow(); err != nil {
				t.Fatal(err)
			}
			if c.lost {
				a.Keep()
				forget("agy:"+a.Path+":before", a.ID+".connect.was")
			}
			if !c.connected {
				if err := edit.SetJSON(a.Path, edit.KV{Path: "model", Value: "native"}); err != nil {
					t.Fatal(err)
				}
			}
			if !strings.Contains(agyRead(t, profile), "magpie agy") {
				t.Fatal("connect gave no shell function")
			}
			before := agyRead(t, a.Path)
			if c.wsl {
				// a distro stopped since isn't started for the page's look
				running = false
				if err := a.Follow(); err != nil || agyRead(t, a.Path) != before {
					t.Fatalf("followed in a stopped distro: %v", err)
				}
				running = true
			}
			if err := a.Follow(); err != nil {
				t.Fatal(err)
			}
			if c.lost || c.connected {
				if got := agyRead(t, a.Path); got != before || a.Disconnected() || c.connected && !a.Wired() {
					t.Fatalf("followed with nothing to take out: %s", got)
				}
				return
			}
			var got, want map[string]any
			if err := json.Unmarshal([]byte(agyRead(t, a.Path)), &got); err != nil {
				t.Fatal(err)
			}
			json.Unmarshal([]byte(c.want), &want)
			if agyJSON(got) != agyJSON(want) {
				t.Fatalf("followed to %s, want %s", agyJSON(got), c.want)
			}
			if stashShared()["agy:"+a.Path+":before"] != "" || a.Owned() {
				t.Fatal("snapshot left after Follow")
			}
			if !a.Disconnected() || a.setUp() {
				t.Fatal("Follow left magpie's record of the connection")
			}
			if got := agyRead(t, profile); got != "alias mine=true\n" {
				t.Fatalf("shell function left: %q", got)
			}
		})
	}
}

// A stash that can't be written puts settings.json back as it was, on
// magpie's model for Disconnect: the shell function, which gives agy its
// key there, stays with it.
func TestAgyShellStaysWhenTheStashIsntWritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions don't hold on windows or as root")
	}
	for _, how := range []string{"disconnect", "follow"} {
		t.Run(how, func(t *testing.T) {
			home := syncHome(t)
			a := agy(home)
			profile := filepath.Join(home, ".bashrc")
			writeFile(t, profile, "alias mine=true\n")
			writeFile(t, a.Path, `{"modelProvider":"vertex"}`)
			if _, err := a.ConnectHow(); err != nil {
				t.Fatal(err)
			}
			if how == "follow" {
				if err := edit.SetJSON(a.Path, edit.KV{Path: "model", Value: "native"}); err != nil {
					t.Fatal(err)
				}
			}
			before := agyRead(t, a.Path)
			dir := filepath.Dir(stashPath())
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(dir, 0o755) })
			var err error
			if how == "follow" {
				err = a.Follow()
			} else {
				err = a.Disconnect()
			}
			os.Chmod(dir, 0o755)
			if err == nil {
				t.Fatal("no error with the stash unwritable")
			}
			if got := agyRead(t, a.Path); got != before {
				t.Fatalf("settings not put back: %s", got)
			}
			if !strings.Contains(agyRead(t, profile), "magpie agy") {
				t.Fatal("shell function taken out though settings were put back")
			}
		})
	}
}

func TestAgyCleansMagpieArtifactsWithoutSnapshot(t *testing.T) {
	for _, c := range []struct {
		name, action, settings string
		artifacts              bool
	}{
		{name: "sync-cleans-magpie-custom-models", action: "sync", settings: `{"model":"native","modelProvider":"gemini","customModelsConfig":{"customModels":{"magpie/relay/glm-4.6":{},"mine":{"modelName":"gemini-3-pro"}}}}`, artifacts: true},
		{name: "disconnect-cleans-magpie-custom-models", action: "disconnect", settings: `{"model":"native","modelProvider":"gemini","customModelsConfig":{"customModels":{"magpie/relay/glm-4.6":{},"mine":{"modelName":"gemini-3-pro"}}}}`, artifacts: true},
		{name: "sync-leaves-native-settings-alone", action: "sync", settings: `{"model":"native","modelProvider":"vertex","trustedWorkspaces":["/w"]}`},
		{name: "disconnect-leaves-native-settings-alone", action: "disconnect", settings: `{"model":"native","modelProvider":"vertex","trustedWorkspaces":["/w"]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := syncHome(t)
			a := agy(home)
			writeFile(t, a.Path, c.settings)
			profile := filepath.Join(home, ".bashrc")
			if c.artifacts {
				if a.Owned() {
					t.Fatal("cleanup-only artifacts were reported as a routed agent")
				}
				writeFile(t, profile, "# >>> magpie agy >>>\nfunction agy { :; }\n# <<< magpie agy <<<\n")
			} else {
				writeFile(t, profile, "alias mine=true\n")
			}
			before := agyRead(t, a.Path)
			var err error
			if c.action == "sync" {
				err = a.Sync()
			} else {
				err = a.Disconnect()
			}
			if err != nil {
				t.Fatal(err)
			}
			if !c.artifacts {
				if got := agyRead(t, a.Path); got != before {
					t.Fatalf("native settings changed: %s", got)
				}
				return
			}
			if model, _ := edit.GetJSON(a.Path, "model"); model != "native" {
				t.Fatalf("native model %q, want native", model)
			}
			if provider, _ := edit.GetJSON(a.Path, "modelProvider"); provider != "gemini" {
				t.Fatalf("modelProvider %q, want unchanged gemini", provider)
			}
			if custom := agyCustom(a.Path); len(custom) != 1 || custom["mine"] == nil {
				t.Fatalf("custom models after cleanup: %v", custom)
			}
			if strings.Contains(agyRead(t, profile), "magpie agy") {
				t.Fatal("magpie shell function remained")
			}
			if got := a.Check(); got != "" {
				t.Fatalf("cleanup warning must not appear as reconnect drift: %q", got)
			}
			if d := a.Drift(); d != nil {
				t.Fatalf("cleanup warning became reconnect drift: %+v", d)
			}
			if got := a.Notice(); !strings.Contains(got, "magpie cannot tell whether modelProvider gemini") {
				t.Fatalf("Notice warning %q", got)
			}
		})
	}
}

func TestAgyConnectAndLegacyRestoreKeepOriginalProvider(t *testing.T) {
	for _, c := range []struct {
		name, provider string
	}{
		{name: "residual-model-gemini", provider: "gemini"},
		{name: "residual-model-vertex", provider: "vertex"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := syncHome(t)
			a := agy(home)
			writeFile(t, a.Path, `{"model":"native","modelProvider":"`+c.provider+`","customModelsConfig":{"customModels":{"magpie/relay/glm-4.6":{},"mine":{"modelName":"gemini-3-pro"}}}}`)
			if err := a.Apply("model", "magpie/relay/glm-4.6"); err != nil {
				t.Fatal(err)
			}
			if err := a.Disconnect(); err != nil {
				t.Fatal(err)
			}
			if model, _ := edit.GetJSON(a.Path, "model"); model != "native" {
				t.Fatalf("model %q, want native", model)
			}
			if provider, _ := edit.GetJSON(a.Path, "modelProvider"); provider != c.provider {
				t.Fatalf("provider %q, want %q", provider, c.provider)
			}
		})
	}

	t.Run("legacy-model-stash-proves-provider-absence", func(t *testing.T) {
		home := syncHome(t)
		a := agy(home)
		writeFile(t, a.Path, `{"model":"magpie/relay/glm-4.6","modelProvider":"gemini","customModelsConfig":{"customModels":{"magpie/relay/glm-4.6":{}}}}`)
		stash(map[string]string{"agy:" + a.Path + ":model": "native"})
		if err := a.Disconnect(); err != nil {
			t.Fatal(err)
		}
		if model, _ := edit.GetJSON(a.Path, "model"); model != "native" {
			t.Fatalf("model %q, want native", model)
		}
		if provider, ok := edit.GetJSON(a.Path, "modelProvider"); ok {
			t.Fatalf("provider remains %q, want original absent", provider)
		}
	})
}

func TestAgyOwnedOnColdWSLDoesNotReadSettingsWhenStashFails(t *testing.T) {
	syncHome(t)
	d, _ := agyDistro(t, "")
	k := wslKindOf("agy")
	live := wslAgent(k, d)
	writeFile(t, live.Path, `{"model":"magpie/relay/glm-4.6"}`)
	if got := live.Field("model").Get(); got != "magpie/relay/glm-4.6" {
		t.Fatalf("live model %q, want magpie model", got)
	}
	if err := os.Remove(stashPath()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(stashPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	d.Running = false
	cold := wslAgent(k, d)
	if cold.Owned() {
		t.Fatal("cold WSL with an unreadable stash was read as owned")
	}
}

func TestAgyWSLStashKeyUsesUNCPathAndDistroID(t *testing.T) {
	d, _ := agyDistro(t, "")
	d.Root = `\\wsl.localhost\Ubuntu`
	at := d.place("agy@wsl:Ubuntu")
	a := wslAgent(wslKindOf("agy"), d)
	wantPath := filepath.Join(d.Root, "home", "me", ".gemini", "antigravity-cli", "settings.json")
	if a.ID != "agy@wsl:Ubuntu" || a.Path != wantPath {
		t.Fatalf("agy WSL identity/path = %q %q, want agy@wsl:Ubuntu %q", a.ID, a.Path, wantPath)
	}
	key := "agy:" + wantPath + ":before"
	if _, owned, err := agyBefore(map[string]string{key: `{}`}, a.Path, a.ID, false); err != nil || !owned {
		t.Fatalf("snapshot at UNC path %q: owned=%v err=%v", key, owned, err)
	}
	if got, want := at.key("agy.connect.was"), "agy@wsl:Ubuntu.connect.was"; got != want {
		t.Fatalf("row stash key %q, want %q", got, want)
	}
	if !isAgy(a.ID) {
		t.Fatalf("WSL agy id %q is not recognized", a.ID)
	}
}

// Check the three entry points before their shared bookkeeping can consume
// unknown originals. These are representative failures, not every I/O error.
func TestAgyUnknownOriginal(t *testing.T) {
	for _, broken := range []string{"settings-default", "stash-disconnect", "snapshot-connect"} {
		t.Run(broken, func(t *testing.T) {
			home := syncHome(t)
			a := agy(home)
			writeFile(t, a.Path, `{"model":"mine","modelProvider":"vertex"}`)
			if err := a.Apply("model", "magpie/relay/glm-4.6"); err != nil {
				t.Fatal(err)
			}
			if err := edit.SetJSON(a.Path, edit.KV{Path: "model", Value: "native"}); err != nil {
				t.Fatal(err)
			}
			switch broken {
			case "settings-default":
				writeFile(t, a.Path, `{"model":`)
			case "stash-disconnect":
				writeFile(t, stashPath(), `{"agy:`)
			case "snapshot-connect":
				stash(map[string]string{"agy:" + a.Path + ":before": `{`})
			}
			settings, saved := agyRead(t, a.Path), agyRead(t, stashPath())
			var err error
			switch broken {
			case "settings-default":
				err = a.Apply("model", "")
			case "stash-disconnect":
				err = a.Disconnect()
			case "snapshot-connect":
				_, err = a.ConnectHow()
			}
			if err == nil {
				t.Fatal("unknown original reported success")
			}
			if got := agyRead(t, stashPath()); got != saved {
				t.Fatalf("unknown snapshot changed: %s", got)
			}
			if got := agyRead(t, a.Path); got != settings {
				t.Fatalf("unknown settings changed: %s", got)
			}
		})
	}
}

// The review boundaries use the same sandbox setup; filememo.Hold simulates
// a poll that saw a native pick before another process reconnected agy.
func TestAgyReviewBoundaries(t *testing.T) {
	for _, scenario := range []string{"stale-sync", "unread-shell", "blank-files", "shared-stash", "manual-prefix", "unowned-disconnect", "wsl-unreached", "wsl-asleep", "wsl-cache", "unknown-unowned", "unknown-model", "unknown-catalog", "sync-unknown-stash"} {
		t.Run(scenario, func(t *testing.T) {
			home := syncHome(t)
			a := agy(home)
			writeFile(t, a.Path, `{"model":"mine","modelProvider":"vertex"}`)
			switch scenario {
			case "blank-files":
				writeFile(t, a.Path, " \n\t")
				writeFile(t, stashPath(), " \n\t")
			case "shared-stash":
				writeFile(t, stashPath(), `{"codex.login":"api","n":1}`)
				if _, err := stashLoadKnown(); err == nil {
					t.Fatal("strict stash accepted non-string")
				}
				if stashShared()["codex.login"] != "api" {
					t.Fatal("strict reader erased shared stash values")
				}
				return
			case "manual-prefix":
				writeFile(t, a.Path, `{"model":"magpie/relay/glm-4.6","modelProvider":"gemini"}`)
			case "unowned-disconnect":
				record(a.ID, "model", "mine")
				before := agyRead(t, appliedPath())
				if err := a.Disconnect(); err != nil {
					t.Fatal(err)
				}
				if len(stashLoad()) != 0 || agyRead(t, appliedPath()) != before {
					t.Fatal("unowned Disconnect changed bookkeeping")
				}
				return
			case "unknown-unowned", "unknown-model", "unknown-catalog":
				if scenario == "unknown-model" {
					writeFile(t, a.Path, `{"model":"magpie/relay/glm-4.6"}`)
				}
				if scenario == "unknown-catalog" {
					writeFile(t, a.Path, `{"customModelsConfig":{"customModels":{"magpie/relay/glm-4.6":{}}}}`)
				}
				// A directory cannot be read as a stash, even as root.
				if err := os.MkdirAll(stashPath(), 0o755); err != nil {
					t.Fatal(err)
				}
				if got, want := a.Owned(), scenario == "unknown-model"; got != want {
					t.Fatalf("owned %v, want %v", got, want)
				}
				return
			case "wsl-cache":
				d, _ := agyDistro(t, "")
				k := wslKindOf("agy")
				a = wslAgent(k, d)
				writeFile(t, a.Path, `{"model":"mine","modelProvider":"vertex"}`)
				if err := a.Apply("model", "magpie/relay/glm-4.6"); err != nil {
					t.Fatal(err)
				}
				wsl.Lock()
				seen, dirty := wsl.seen, wsl.dirty
				d.Values = map[string]string{k.memo("model"): "magpie/relay/glm-4.6"}
				wsl.seen = map[string]*distro{d.Name: &d}
				wsl.Unlock()
				old := wslRun
				t.Cleanup(func() {
					wslRun = old
					wsl.Lock()
					wsl.seen, wsl.dirty = seen, dirty
					wsl.Unlock()
				})
				wslRun = func(time.Duration, ...string) ([]byte, error) { return nil, nil }
				d.Running = false
				a = wslAgent(k, d)
				if err := a.Disconnect(); err != nil {
					t.Fatal(err)
				}
				var rec reconnection
				json.Unmarshal([]byte(stashLoad()[a.ID+".reconnect"]), &rec)
				if got := a.Values()["model"]; got != "mine" || rec.Left["model"] != "mine" {
					t.Fatalf("cached model %q, reconnect left %q, want mine", got, rec.Left["model"])
				}
				var saved map[string]*distro
				if err := json.Unmarshal([]byte(agyRead(t, wslStatePath())), &saved); err != nil {
					t.Fatal(err)
				}
				if saved[d.Name].Values[k.memo("model")] != "mine" {
					t.Fatal("restored WSL model was not saved")
				}
				return
			}
			if err := a.Apply("model", "magpie/relay/glm-4.6"); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "sync-unknown-stash":
				writeFile(t, stashPath(), `{"broken":`)
				if err := edit.DelJSON(a.Path, "customModelsConfig"); err != nil {
					t.Fatal(err)
				}
				if err := a.Sync(); err != nil {
					t.Fatal(err)
				}
				if _, ok := agyCustom(a.Path)["magpie/relay/glm-4.6"]; !ok {
					t.Fatal("connected Sync did not refresh catalog")
				}
				if got := agyRead(t, stashPath()); got != `{"broken":` {
					t.Fatalf("Sync replaced unknown stash: %s", got)
				}
				return
			case "stale-sync":
				if err := edit.SetJSON(a.Path, edit.KV{Path: "model", Value: "native"}); err != nil {
					t.Fatal(err)
				}
				oldTime := time.Now().Add(-time.Hour)
				if err := os.Chtimes(a.Path, oldTime, oldTime); err != nil {
					t.Fatal(err)
				}
				release := filememo.Hold()
				defer release()
				if a.Field("model").Get() != "native" {
					t.Fatal("did not cache native pick")
				}
				raw := strings.Replace(agyRead(t, a.Path), `"native"`, `"magpie/relay/glm-4.6"`, 1)
				if err := os.WriteFile(a.Path, []byte(raw), 0o644); err != nil {
					t.Fatal(err)
				}
				before := agyRead(t, stashPath())
				if err := a.Sync(); err != nil {
					t.Fatal(err)
				}
				release()
				if m, _ := edit.GetJSON(a.Path, "model"); !usesMagpie(m) || agyRead(t, stashPath()) != before {
					t.Fatal("stale Sync undid a fresh connection")
				}
				return
			case "unread-shell":
				// A directory is unreadable as a profile even with elevated rights.
				rc := filepath.Join(home, ".bashrc")
				os.Remove(rc)
				if err := os.Mkdir(rc, 0o755); err != nil {
					t.Fatal(err)
				}
			case "wsl-unreached":
				expected := errors.New("WSL link unresolved")
				a = unreached(a, expected)
				if err := a.Disconnect(); !errors.Is(err, expected) {
					t.Fatalf("unreached Disconnect: %v", err)
				}
				return
			case "wsl-asleep":
				a.ID = "agy@wsl:Stopped"
				old := wslRun
				t.Cleanup(func() { wslRun = old })
				wslRun = func(time.Duration, ...string) ([]byte, error) { return nil, errors.New("cannot start") }
				a = asleep(a, wslKindOf("agy"), distro{Name: "Stopped", Home: "/home/me"})
				if err := a.Disconnect(); err == nil {
					t.Fatal("stopped WSL reported cleanup without opening files")
				}
				return
			}
			if err := a.Disconnect(); err != nil {
				t.Fatal(err)
			}
			want := "mine"
			if scenario == "blank-files" || scenario == "manual-prefix" {
				want = ""
			}
			if model, _ := edit.GetJSON(a.Path, "model"); model != want {
				t.Fatalf("model %q, want %q", model, want)
			}
			if scenario == "unread-shell" {
				if p, _ := edit.GetJSON(a.Path, "modelProvider"); p != "vertex" {
					t.Fatalf("shell failure blocked settings restore: %q", p)
				}
			}
		})
	}
}
