package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
	// on Windows agy is given --settings by a file: one that can't be
	// written refuses the pick, settings.json as it was
	system := agyOS
	t.Cleanup(func() { agyOS = system })
	agyOS = "windows"
	os.MkdirAll(AgySettingsPath(), 0o755)
	if err := a.Apply("model", "magpie/deepseek/pro"); err == nil || agyRead(t, path) != orig {
		t.Fatalf("picked with no settings file: %v\n%s", err, agyRead(t, path))
	}
	os.Remove(AgySettingsPath())
	if err := a.Apply("model", "magpie/deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	// magpie's file, beside agy's own
	if b, _ := os.ReadFile(AgySettingsPath()); string(b) != AgySettings || AgySettingsPath() != filepath.Join(filepath.Dir(path), "magpie-settings.json") {
		t.Fatalf("settings file %s: %q", AgySettingsPath(), b)
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
	// a Sync that can't write the file says so
	os.Remove(AgySettingsPath())
	os.MkdirAll(AgySettingsPath(), 0o755)
	if err := a.Sync(); err == nil {
		t.Fatal("Sync with no settings file")
	}
	os.Remove(AgySettingsPath())
	// the options: the user's own, then magpie's
	opts := a.Field("model").Options(a.Values())
	if len(opts) == 0 || opts[0].Value != "mine" || opts[0].Group != "Antigravity CLI" {
		t.Fatalf("options: %+v", opts)
	}
	// as installed: the user's model and provider back, magpie's models
	// out, the provider also where an older magpie wrote gemini over it
	os.WriteFile(path, []byte(strings.Replace(agyRead(t, path), `"vertex"`, `"gemini"`, 1)), 0o644)
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
	// nor its --settings file beside it
	if es, _ := os.ReadDir(filepath.Dir(path)); len(es) != 1 {
		t.Fatalf("agy's folder: %v", es)
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
