//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/update"
)

// Typing agy just works (bestony on Discord): picking one of magpie's
// models for agy gives the user's shells a function agy that starts it
// through magpie, the next start puts it back for a user wired before,
// and stepping out takes it out, the user's own lines as they were.
func TestAgyShell(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ZDOTDIR", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if err := provider.Save(provider.Provider{ID: "deepseek", Name: "DeepSeek", Chat: "https://api.deepseek.com/v1", Key: "k", Models: []string{"pro"}}); err != nil {
		t.Fatal(err)
	}
	mine := "alias ll='ls -l'\n"
	rcs := []string{".zshrc", ".bashrc", ".bash_profile"}
	for _, n := range rcs {
		os.WriteFile(filepath.Join(home, n), []byte(mine), 0o644)
	}
	a, err := Find("agy")
	if err != nil {
		t.Fatal(err)
	}
	if env, m, err := AgyStart(); err != nil || env != nil || m != "" {
		t.Fatalf("AgyStart before: %v %q", env, m)
	}
	if err := a.Apply("model", "magpie/deepseek/pro"); err != nil {
		t.Fatal(err)
	}
	in := update.ShellFunctionIn("agy")
	if len(in) == 0 {
		t.Fatal("no shell's profile has agy")
	}
	for _, p := range in {
		b, _ := os.ReadFile(p)
		if s := string(b); !strings.HasPrefix(s, mine+"\n# >>> magpie agy >>>\nfunction agy {\n") || !strings.Contains(s, " agy run \"$@\"\n    if [ $? -eq 0 ]; then return; fi") || !strings.HasSuffix(s, "}\n# <<< magpie agy <<<\n") {
			t.Fatalf("%s:\n%s", p, s)
		}
	}
	if a.Typed() != "agy" {
		t.Fatalf("typed: %q", a.Typed())
	}
	if n := a.Notice(); !strings.HasPrefix(n, "Type agy in a new terminal: magpie added it to ~/") || !strings.HasSuffix(n, "(in a terminal open now: "+a.Launch()+").") {
		t.Fatalf("notice: %q", n)
	}
	env, m, err := AgyStart()
	if err != nil || m != "deepseek/pro" || strings.Join(env, " ") != "AGY_LLM_GATEWAY_URL="+gateway.URL()+
		" AGY_LLM_GATEWAY_API_KEY="+gateway.TokenFor("agy")+" AGY_LLM_GATEWAY_WIRE_PROTOCOL=openai AGY_LLM_GATEWAY_MODELS=" {
		t.Fatalf("AgyStart: %v %q %v", env, m, err)
	}

	// wired before the function was given: the next start gives it
	for _, p := range in {
		os.WriteFile(p, []byte(mine), 0o644)
	}
	if a.Typed() != "" {
		t.Fatal("typed with no function")
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := update.ShellFunctionIn("agy"); strings.Join(got, ",") != strings.Join(in, ",") {
		t.Fatalf("after Sync: %v, want %v", got, in)
	}

	// out: as the user had them
	if err := a.Apply("model", ""); err != nil {
		t.Fatal(err)
	}
	for _, n := range rcs {
		if b, _ := os.ReadFile(filepath.Join(home, n)); string(b) != mine {
			t.Fatalf("%s after stepping out:\n%s", n, b)
		}
	}
	if a.Typed() != "" {
		t.Fatal("typed after stepping out")
	}
	// and a start off magpie doesn't give it again
	a.Sync()
	if got := update.ShellFunctionIn("agy"); len(got) != 0 {
		t.Fatalf("Sync off magpie: %v", got)
	}
}

// The function in each shell runs this magpie's `agy run`, the agy on
// PATH when magpie is gone, its path quoted for that shell.
func TestAgyFunction(t *testing.T) {
	prog := "/Users/o'neil/magpie"
	for kind, want := range map[string]string{
		"posix":      `'/Users/o'\''neil/magpie' agy run "$@"`,
		"fish":       `'/Users/o\'neil/magpie' agy run $argv`,
		"powershell": `& '/Users/o''neil/magpie' agy run @args`,
	} {
		if got := agyFunction(kind, prog); !strings.Contains(got, want) {
			t.Errorf("%s:\n%s\nwant it to have %s", kind, got, want)
		}
	}
	if got := agyFunction("fish", prog); !strings.Contains(got, "if test $status -eq 0") {
		t.Errorf("fish function does not fall back after magpie failure:\n%s", got)
	}
	if got := agyFunction("powershell", prog); !strings.Contains(got, "$LASTEXITCODE -eq 0") {
		t.Errorf("PowerShell function does not fall back after magpie failure:\n%s", got)
	}
	fish := agyDistroFunction("fish", "agy-key", "http://127.0.0.1:3425", "deepseek/pro")
	if !strings.Contains(fish, "AGY_LLM_GATEWAY_WIRE_PROTOCOL=openai agy $argv") || strings.Contains(fish, "command agy") {
		t.Fatalf("fish gateway function doesn't invoke agy through env:\n%s", fish)
	}
}

// The Windows command sets agy's gateway variables, clears the old
// Gemini API ones, and asks for every model the gateway lists.
func TestAgyLaunchPowerShell(t *testing.T) {
	l := agyLaunchAt("http://127.0.0.1:3425", "magpie-agy", "deepseek/pro", true)
	for _, want := range []string{
		`$env:AGY_LLM_GATEWAY_URL="http://127.0.0.1:3425"`,
		"$env:AGY_LLM_GATEWAY_API_KEY=\"" + "magpie-agy" + "\"",
		`$env:AGY_LLM_GATEWAY_WIRE_PROTOCOL="openai"`,
		`$env:AGY_LLM_GATEWAY_MODELS=""`,
		`agy --model 'deepseek/pro'`,
	} {
		if !strings.Contains(l, want) {
			t.Errorf("%q doesn't have %q", l, want)
		}
	}
	if strings.Contains(l, "GEMINI_API_KEY") || strings.Contains(l, "GOOGLE_GEMINI_BASE_URL") {
		t.Errorf("the old Gemini variables are still in %q", l)
	}
}
