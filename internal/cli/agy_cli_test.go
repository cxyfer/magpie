package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// `magpie agy run`, what the function agy in the user's shells runs
// (bestony on Discord): the agy on PATH, given magpie's gateway, key and
// --model while settings.json's model is magpie's, every argument agy's;
// as it is off magpie.
func TestAgyRun(t *testing.T) {
	home := helpHome(t)
	settings := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	t.Setenv("GEMINI_API_KEY", "users-own")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "")
	var bin string
	var args, env []string
	oldExec, oldLook := agyExec, agyLook
	t.Cleanup(func() { agyExec, agyLook = oldExec, oldLook })
	agyExec = func(b string, a, e []string) error {
		bin, args, env = b, a, e
		return nil
	}
	real := filepath.Join(home, "bin", "agy")
	agyLook = func() (string, error) { return real, nil }
	envOf := func(k string) []string {
		var out []string
		for _, kv := range env {
			if strings.HasPrefix(kv, k+"=") {
				out = append(out, strings.TrimPrefix(kv, k+"="))
			}
		}
		return out
	}

	os.WriteFile(settings, []byte(`{"model": "magpie/deepseek/pro", "modelProvider": "gemini"}`), 0o644)
	for _, c := range []struct{ in, want []string }{
		{nil, []string{"--model", "magpie/deepseek/pro"}},
		{[]string{"--help"}, []string{"--model", "magpie/deepseek/pro", "--help"}},
		{[]string{"-p", "hi", "--model", "mine"}, []string{"-p", "hi", "--model", "mine"}},
		{[]string{"--model=mine"}, []string{"--model=mine"}},
		{[]string{"-model", "mine"}, []string{"-model", "mine"}},
	} {
		if _, err := runOut(t, append([]string{"agy", "run"}, c.in...)...); err != nil {
			t.Fatal(err)
		}
		if bin != real || !slices.Equal(args, c.want) {
			t.Fatalf("agy run %v: %s %v, want %v", c.in, bin, args, c.want)
		}
		if k, u := envOf("GEMINI_API_KEY"), envOf("GOOGLE_GEMINI_BASE_URL"); !slices.Equal(k, []string{gateway.TokenFor("agy")}) || !slices.Equal(u, []string{gateway.URL()}) {
			t.Fatalf("env: key %v, url %v", k, u)
		}
	}
	out, err := runOut(t, "agy", "env")
	if want := "GEMINI_API_KEY=" + gateway.TokenFor("agy") + "\nGOOGLE_GEMINI_BASE_URL=" + gateway.URL() + "\nmodel=magpie/deepseek/pro\n"; err != nil || out != want {
		t.Fatalf("agy env: %q %v", out, err)
	}

	// off magpie: as it is
	os.WriteFile(settings, []byte(`{"model": "mine"}`), 0o644)
	if _, err := runOut(t, "agy", "run", "-p", "hi"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"-p", "hi"}) || !slices.Equal(envOf("GEMINI_API_KEY"), []string{"users-own"}) {
		t.Fatalf("off magpie: %v %v", args, envOf("GEMINI_API_KEY"))
	}
	if out, err := runOut(t, "agy", "env"); err != nil || out != "" {
		t.Fatalf("agy env off magpie: %q %v", out, err)
	}

	// what --help and a stray value say
	if out, _ := runOut(t, "agy", "--help"); !strings.Contains(out, "magpie agy run [args…]") {
		t.Fatalf("agy --help:\n%s", out)
	}
	if _, err := runOut(t, "agy", "env", "x"); err == nil {
		t.Fatal("agy env x: no error")
	}
	agyLook = func() (string, error) { return "", errors.New("not found") }
	if _, err := runOut(t, "agy", "run"); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Fatalf("no agy: %v", err)
	}
}

// agyNativePick is agy connected to magpie on vertex, then on a model
// picked in its own /model, which leaves magpie's provider behind; its
// settings.json.
func agyNativePick(t *testing.T) (home, settings string) {
	t.Helper()
	home = helpHome(t)
	if err := provider.Save(provider.Provider{ID: "relay", Chat: "http://127.0.0.1:1/v1", Key: "k", Models: []string{"pro"}}); err != nil {
		t.Fatal(err)
	}
	settings = filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	os.WriteFile(settings, []byte(`{"modelProvider":"vertex"}`), 0o644)
	if err := run([]string{"agy", "relay/pro"}); err != nil {
		t.Fatal(err)
	}
	if err := edit.SetJSON(settings, edit.KV{Path: "model", Value: "gemini-native"}); err != nil {
		t.Fatal(err)
	}
	return home, settings
}

// agyTidied fails unless raw is settings.json on the native model and
// vertex again, nothing of magpie's left.
func agyTidied(t *testing.T, raw string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(got); string(b) != `{"model":"gemini-native","modelProvider":"vertex"}` {
		t.Fatalf("agy starts on %s", raw)
	}
}

// magpie's provider, left by agy's own /model, would have agy ask for
// GEMINI_API_KEY: `magpie agy run` takes it out before agy starts.
func TestAgyRunAfterNativePick(t *testing.T) {
	home, settings := agyNativePick(t)
	var started string
	var args []string
	oldExec, oldLook := agyExec, agyLook
	t.Cleanup(func() { agyExec, agyLook = oldExec, oldLook })
	agyExec = func(_ string, a, _ []string) error {
		b, _ := os.ReadFile(settings)
		started, args = string(b), a
		return nil
	}
	agyLook = func() (string, error) { return filepath.Join(home, "bin", "agy"), nil }
	if _, err := runOut(t, "agy", "run", "-p", "hi"); err != nil {
		t.Fatal(err)
	}
	agyTidied(t, started)
	if !slices.Equal(args, []string{"-p", "hi"}) {
		t.Fatalf("agy started with %v", args)
	}
}

// PowerShell's function asks `magpie agy env`, then starts agy as it is
// on its empty answer: env takes it out too.
func TestAgyEnvAfterNativePick(t *testing.T) {
	_, settings := agyNativePick(t)
	out, err := runOut(t, "agy", "env")
	if err != nil || out != "" {
		t.Fatalf("agy env: %q %v", out, err)
	}
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	agyTidied(t, string(b))
}

func TestAgyDefaultAfterNativePick(t *testing.T) {
	for _, c := range []struct {
		name                    string
		reconnect, lostSnapshot bool
	}{{name: "native"}, {name: "reconnect", reconnect: true}, {name: "lost-snapshot", lostSnapshot: true}} {
		t.Run(c.name, func(t *testing.T) {
			home := helpHome(t)
			if err := provider.Save(provider.Provider{ID: "relay", Chat: "http://127.0.0.1:1/v1", Key: "k", Models: []string{"pro"}}); err != nil {
				t.Fatal(err)
			}
			settings := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
			os.MkdirAll(filepath.Dir(settings), 0o755)
			os.WriteFile(settings, []byte(`{"model":"before","modelProvider":"vertex"}`), 0o644)
			if err := run([]string{"agy", "relay/pro"}); err != nil {
				t.Fatal(err)
			}
			if c.lostSnapshot {
				a, err := agent.Find("agy")
				if err != nil {
					t.Fatal(err)
				}
				a.Keep()
				if err := os.WriteFile(filepath.Join(filepath.Dir(provider.Path()), "stash.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := edit.SetJSON(settings, edit.KV{Path: "model", Value: "gemini-native"}); err != nil {
					t.Fatal(err)
				}
			}
			if c.reconnect {
				if err := run([]string{"agy", "relay/pro"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := run([]string{"agy", "default"}); err != nil {
				t.Fatal(err)
			}
			wantProvider := "vertex"
			if c.lostSnapshot {
				wantProvider = "gemini"
			}
			if p, _ := edit.GetJSON(settings, "modelProvider"); p != wantProvider {
				t.Errorf("default left provider %q, want %s", p, wantProvider)
			}
			if c.lostSnapshot {
				if m, _ := edit.GetJSON(settings, "model"); m != "" {
					t.Errorf("default left magpie model %q", m)
				}
				if m, _ := edit.GetJSON(settings, "customModelsConfig.customModels"); strings.Contains(m, "magpie/") {
					t.Errorf("default left magpie catalog %s", m)
				}
			} else if !c.reconnect {
				if m, _ := edit.GetJSON(settings, "model"); m != "gemini-native" {
					t.Errorf("default lost native model: %q", m)
				}
			}
		})
	}
}
