package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/edit"
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
	// an agy without --settings isn't put on magpie
	version := agyVersion
	t.Cleanup(func() { agyVersion = version })
	agyVersion = func() string { return "1.4.9" }
	if err := a.Apply("model", "magpie/deepseek/pro"); err == nil || agyRead(t, path) != orig {
		t.Fatalf("1.4.9 put on magpie: %v\n%s", err, agyRead(t, path))
	}
	agyVersion = func() string { return "1.4.10" }
	if err := a.Apply("model", "magpie/deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	// modelProvider stays the user's: agy started on magpie is given
	// gemini by --settings (#2924)
	m := read()
	if m["model"] != "magpie/deepseek/pro" || m["modelProvider"] != "vertex" {
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
	if want := "GEMINI_API_KEY=" + gateway.TokenFor("agy") + " GOOGLE_GEMINI_BASE_URL=" + gateway.URL() + ` agy --settings '{"modelProvider":"gemini"}' --model 'magpie/deepseek/pro'`; runtime.GOOS != "windows" && l != want {
		t.Fatalf("launch: %q", l)
	}
	if ps := agyLaunchAt("http://gw", "k", "magpie/o'x", true); !strings.HasSuffix(ps, "; agy --settings '"+AgySettingsPath()+"' --model 'magpie/o''x'") {
		t.Fatalf("PowerShell's launch: %q", ps)
	}
	// agy updated back to one without --settings: Check says so
	agyVersion = func() string { return "1.4.9" }
	if d := a.Drift(); d == nil || d.Kind != "unwired" {
		t.Fatalf("drift on 1.4.9: %+v", d)
	}
	agyVersion = func() string { return "1.4.10" }
	if n := a.Notice(); !strings.Contains(n, l) {
		t.Fatalf("notice: %q", n)
	}
	// another of magpie's keeps the user's stashed
	if err := a.Apply("model", "magpie/deepseek/flash"); err != nil {
		t.Fatal(err)
	}
	// gemini the user set while on magpie is theirs: a start keeps it
	os.WriteFile(path, []byte(strings.Replace(agyRead(t, path), `"vertex"`, `"gemini"`, 1)), 0o644)
	if err := a.Sync(); err != nil || read()["modelProvider"] != "gemini" {
		t.Fatalf("after Sync on the user's gemini: %v\n%s", err, agyRead(t, path))
	}
	// gemini as an older magpie wrote it over the user's, with no mark of
	// --settings: a start puts theirs back once, but not from a stash gone
	// or unparsed, which says nothing of theirs
	older := "agy:" + path + ":settings"
	forget(older)
	kept, _ := os.ReadFile(stashPath())
	for _, bad := range []string{"gone", `{"oops`} {
		if bad == "gone" {
			os.Remove(stashPath())
		} else {
			os.WriteFile(stashPath(), []byte(bad), 0o600)
		}
		if err := a.Sync(); err != nil || read()["modelProvider"] != "gemini" {
			t.Fatalf("after Sync on a stash %s: %v\n%s", bad, err, agyRead(t, path))
		}
	}
	os.WriteFile(stashPath(), kept, 0o600)
	if err := a.Sync(); err != nil || read()["modelProvider"] != "vertex" {
		t.Fatalf("after Sync: %v\n%s", err, agyRead(t, path))
	}
	os.WriteFile(path, []byte(strings.Replace(agyRead(t, path), `"vertex"`, `"gemini"`, 1)), 0o644)
	if err := a.Sync(); err != nil || read()["modelProvider"] != "gemini" {
		t.Fatalf("a second Sync: %v\n%s", err, agyRead(t, path))
	}
	// the options: the user's own, then magpie's
	opts := a.Field("model").Options(a.Values())
	if len(opts) == 0 || opts[0].Value != "mine" || opts[0].Group != "Antigravity CLI" {
		t.Fatalf("options: %+v", opts)
	}
	// as installed: the user's model back, their provider as they set it,
	// magpie's models out
	if err := a.Apply("model", ""); err != nil {
		t.Fatal(err)
	}
	var back, was map[string]any
	json.Unmarshal([]byte(agyRead(t, path)), &back)
	json.Unmarshal([]byte(strings.Replace(orig, `"vertex"`, `"gemini"`, 1)), &was)
	if agyJSON(back) != agyJSON(was) {
		t.Fatalf("not restored:\n%s", agyRead(t, path))
	}
	if a.Launch() != "" {
		t.Fatal("launch after reset")
	}

	// with nothing of the user's, reset leaves nothing of magpie's, and a
	// start takes out the gemini an older magpie wrote where they had none
	os.WriteFile(path, []byte("{\n  \"trustedWorkspaces\": []\n}\n"), 0o644)
	a.Apply("model", "magpie/deepseek/pro")
	edit.SetJSON(path, edit.KV{Path: "modelProvider", Value: "gemini"})
	forget(older)
	if err := a.Sync(); err != nil || read()["modelProvider"] != nil {
		t.Fatalf("after Sync: %v\n%s", err, agyRead(t, path))
	}
	a.Apply("model", "")
	if got := strings.TrimSpace(agyRead(t, path)); got != "{\n  \"trustedWorkspaces\": []\n}" {
		t.Fatalf("left behind:\n%s", got)
	}

	// the user's own gemini stays, on magpie and after
	os.WriteFile(path, []byte(`{"modelProvider": "gemini"}`), 0o644)
	a.Apply("model", "magpie/deepseek/pro")
	if err := a.Sync(); err != nil || read()["modelProvider"] != "gemini" {
		t.Fatalf("after Sync: %v\n%s", err, agyRead(t, path))
	}
	a.Apply("model", "")
	if read()["modelProvider"] != "gemini" {
		t.Fatalf("after reset:\n%s", agyRead(t, path))
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
