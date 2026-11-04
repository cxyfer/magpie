package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/proc"
)

// `magpie agy run [args…]` is what the function agy magpie puts in the
// user's shells runs (agent/agy.go): the agy on PATH, given magpie's
// gateway, key and model while settings.json's model is one of magpie's,
// and as it is otherwise, once what magpie left after agy's own /model
// picked a model of its own is taken out (agent.AgyFollow). Every argument
// is agy's, --help too. `magpie agy env` prints those variables and the
// model, one NAME=value a line and nothing off magpie, for PowerShell's
// function; off magpie it takes out the same first.

const agyRunUsage = `magpie agy run [args…]   start agy, on magpie while its model is one of magpie's

The agy on your PATH starts with GEMINI_API_KEY, GOOGLE_GEMINI_BASE_URL and
--model <its model> (not when you give --model yourself); every argument is
agy's. Off magpie's models it starts as it is, after taking out the provider
and models magpie left in settings.json when agy's own /model picked another.
The function agy magpie puts in your shell's profile runs this.

magpie agy env           the variables and model it starts agy with, a NAME=value
                         a line; nothing while agy isn't on magpie, what magpie
                         left in settings.json taken out as run does
`

// agyCmd is `magpie agy run|env …`; ok false for another form.
func agyCmd(args []string) (ok bool, err error) {
	if len(args) < 2 || args[0] != "agy" && args[0] != "antigravity-cli" {
		return false, nil
	}
	switch args[1] {
	case "run":
		return true, agyRun(args[2:])
	case "env":
		if len(args) > 2 {
			if slices.Contains([]string{"-h", "--help", "help"}, args[2]) {
				fmt.Print(agyRunUsage)
				return true, nil
			}
			return true, fmt.Errorf("magpie agy env takes nothing after it, not %q", args[2])
		}
		env, model := agent.AgyStart()
		if model == "" {
			// PowerShell's function then starts agy as it is
			agyFollow()
			return true, nil
		}
		for _, kv := range env {
			fmt.Println(kv)
		}
		fmt.Println("model=" + model)
		return true, nil
	}
	return false, nil
}

// agyExec starts the program bin as agy with args and env, in place of
// magpie where the system can (agy_exec_*.go); tests set it.
var agyExec = agyExecOS

// agyLook is the agy on PATH; tests set it.
var agyLook = func() (string, error) { return proc.Look("agy") }

func agyRun(args []string) error {
	bin, err := agyLook()
	if err != nil {
		return errors.New("agy isn't installed, or isn't on your PATH")
	}
	if self, err := os.Executable(); err == nil && sameProgram(self, bin) {
		return errors.New("the agy on your PATH is magpie itself; install Antigravity CLI")
	}
	env := os.Environ()
	start, model := agent.AgyStart()
	if model == "" {
		agyFollow()
	}
	if model != "" {
		env = slices.DeleteFunc(env, func(kv string) bool {
			k, _, _ := strings.Cut(kv, "=")
			return strings.EqualFold(k, "GEMINI_API_KEY") || strings.EqualFold(k, "GOOGLE_GEMINI_BASE_URL")
		})
		env = append(env, start...)
		if !slices.ContainsFunc(args, modelFlag) {
			args = append([]string{"--model", model}, args...)
		}
	}
	return agyExec(bin, args, env)
}

// agyFollow takes out what magpie left once agy's own /model picked a
// model of its own (agent.AgyFollow), before agy starts as it is: magpie's
// provider would have it ask for GEMINI_API_KEY. A failure is told on
// stderr, and agy starts all the same.
func agyFollow() {
	if err := agent.AgyFollow(); err != nil {
		fmt.Fprintln(os.Stderr, "magpie: couldn't take magpie out of agy's settings.json:", err)
	}
}

// modelFlag says a is agy's --model, which it takes as -model too.
func modelFlag(a string) bool {
	a, _, _ = strings.Cut(a, "=")
	return a == "--model" || a == "-model"
}

func sameProgram(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}
