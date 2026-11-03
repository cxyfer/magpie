package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// `magpie agy run`, what the function agy in the user's shells runs
// (bestony on Discord): the agy on PATH, given magpie's Enterprise Gateway
// variables and --model while magpie's stash names a model, every argument
// agy's; as it is off magpie.
func TestAgyRun(t *testing.T) {
	home := helpHome(t)
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	settings := filepath.Join(dir, "settings.json")

	os.MkdirAll(dir, 0o755)
	t.Setenv("GEMINI_API_KEY", "users-own")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "https://users.example")
	t.Setenv("AGY_LLM_GATEWAY_URL", "https://users.example/gateway")
	t.Setenv("AGY_LLM_GATEWAY_API_KEY", "users-key")
	t.Setenv("AGY_LLM_GATEWAY_WIRE_PROTOCOL", "genai")
	t.Setenv("AGY_LLM_GATEWAY_MODELS", "only-one")
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

	if err := provider.Save(provider.Provider{ID: "deepseek", Name: "DeepSeek", Chat: "https://api.deepseek.com/v1", Key: "k", Models: []string{"pro"}}); err != nil {
		t.Fatal(err)
	}
	a, err := agent.Find("agy")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply("model", "magpie/deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ in, want []string }{
		{nil, []string{"--model", "deepseek/pro"}},
		{[]string{"--help"}, []string{"--model", "deepseek/pro", "--help"}},
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
		if u, k, w, m := envOf("AGY_LLM_GATEWAY_URL"), envOf("AGY_LLM_GATEWAY_API_KEY"), envOf("AGY_LLM_GATEWAY_WIRE_PROTOCOL"), envOf("AGY_LLM_GATEWAY_MODELS"); !slices.Equal(u, []string{gateway.URL()}) ||
			!slices.Equal(k, []string{gateway.TokenFor("agy")}) || !slices.Equal(w, []string{"openai"}) || !slices.Equal(m, []string{""}) {
			t.Fatalf("env: url %v, key %v, wire %v, models %v", u, k, w, m)
		}
		// the old Gemini API variables do not go to the child even when
		// the user's shell has them
		if len(envOf("GEMINI_API_KEY")) != 0 || len(envOf("GOOGLE_GEMINI_BASE_URL")) != 0 {
			t.Fatalf("old Gemini variables reached agy: %v %v", envOf("GEMINI_API_KEY"), envOf("GOOGLE_GEMINI_BASE_URL"))
		}
	}
	out, err := runOut(t, "agy", "env")
	want := "AGY_LLM_GATEWAY_URL=" + gateway.URL() + "\nAGY_LLM_GATEWAY_API_KEY=" + gateway.TokenFor("agy") +
		"\nAGY_LLM_GATEWAY_WIRE_PROTOCOL=openai\nAGY_LLM_GATEWAY_MODELS=\nmodel=deepseek/pro\n"
	if err != nil || out != want {
		t.Fatalf("agy env: %q %v, want %q", out, err, want)
	}

	// off magpie: as it is, the user's own variables left alone
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	if _, err := runOut(t, "agy", "run", "-p", "hi"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"-p", "hi"}) || !slices.Equal(envOf("GEMINI_API_KEY"), []string{"users-own"}) ||
		!slices.Equal(envOf("AGY_LLM_GATEWAY_URL"), []string{"https://users.example/gateway"}) {
		t.Fatalf("off magpie: %v %v %v", args, envOf("GEMINI_API_KEY"), envOf("AGY_LLM_GATEWAY_URL"))
	}
	if out, err := runOut(t, "agy", "env"); err != nil || out != "" {
		t.Fatalf("agy env off magpie: %q %v", out, err)
	}

	// an old magpie's settings are cleaned before agy starts, and the
	// model it was on carries over
	os.WriteFile(settings, []byte(`{"model":"magpie/deepseek/pro","modelProvider":"gemini","customModelsConfig":{"customModels":{"magpie/deepseek/pro":{"apiProvider":"API_PROVIDER_GOOGLE_GEMINI","modelName":"deepseek/pro"},"mine":{"apiProvider":"API_PROVIDER_GOOGLE_GEMINI","modelName":"gemini-3-pro"}}}}`), 0o644)
	if _, err := runOut(t, "agy", "run", "-p", "hi"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"--model", "deepseek/pro", "-p", "hi"}) {
		t.Fatalf("after migration: %v", args)
	}
	b, _ := os.ReadFile(settings)
	if strings.Contains(string(b), "modelProvider") || strings.Contains(string(b), "magpie/deepseek/pro") || !strings.Contains(string(b), `"mine"`) {
		t.Fatalf("old settings left:\n%s", b)
	}

	// An unreadable stash must not prevent the shell wrapper from starting
	// agy as it is, with the caller's arguments and environment intact.
	if err := os.WriteFile(filepath.Join(filepath.Dir(provider.Path()), "stash.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runOut(t, "agy", "run", "-p", "hi"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"-p", "hi"}) || !slices.Equal(envOf("GEMINI_API_KEY"), []string{"users-own"}) ||
		!slices.Equal(envOf("AGY_LLM_GATEWAY_URL"), []string{"https://users.example/gateway"}) {
		t.Fatalf("unknown state did not use raw agy: args=%v gemini=%v url=%v", args, envOf("GEMINI_API_KEY"), envOf("AGY_LLM_GATEWAY_URL"))
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
