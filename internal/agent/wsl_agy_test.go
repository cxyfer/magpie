package agent

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
)

// mimi001234 on Discord: magpie's Agents page didn't find Antigravity CLI
// installed in WSL. The probe asks after its folder and its command, and
// finds it by either, and asks its version (agyGatewaySince); a distro
// with neither has no agy row.
func TestWSLAgyFound(t *testing.T) {
	k := wslKindOf("agy")
	if !strings.Contains(wslProbeScript, `[ -d "$HOME/.gemini/antigravity-cli" ] && echo dir:.gemini/antigravity-cli; `) ||
		!strings.Contains(wslProbeScript, `command -v agy `) || !strings.Contains(wslProbeScript, `echo "ver:agy `) {
		t.Fatal("the probe doesn't ask after agy")
	}
	for _, p := range []string{"dir:.gemini/antigravity-cli\n", "bin:agy /home/me/.local/bin/agy\n"} {
		if d := parseProbe("U", "home:/home/me\n"+p); d == nil || !k.found(*d) {
			t.Errorf("not found by %q", p)
		}
	}
	// Gemini CLI's ~/.gemini, or Windows' agy on the PATH WSL inherits, is not it
	for _, p := range []string{"dir:.gemini\n", "bin:gemini /usr/bin/gemini\n", "bin:agy /mnt/c/Users/me/AppData/Local/Programs/agy/agy\n"} {
		if d := parseProbe("U", "home:/home/me\n"+p); d != nil && k.found(*d) {
			t.Errorf("found by %q", p)
		}
	}

	syncHome(t)
	with, without := t.TempDir(), t.TempDir()
	fakeWSL(t, "Ubuntu\r\nDebian\r\n", "Ubuntu\r\nDebian\r\n", map[string]string{
		"Ubuntu": "home:/home/me\nbin:agy /home/me/.local/bin/agy\ndir:.gemini/antigravity-cli\nroute:default via 172.20.0.1 dev eth0\n",
		"Debian": "home:/home/me\ndir:.codex\nroute:default via 172.20.0.1 dev eth0\n",
	}, map[string]string{"Ubuntu": with, "Debian": without})
	var ids []string
	for _, a := range wslAgentsOf(wslDistros()) {
		ids = append(ids, a.ID)
		if a.ID == "agy@wsl:Ubuntu" && (a.Name != "Antigravity CLI · WSL Ubuntu" || a.Bin != "" ||
			a.Path != filepath.Join(with, "home", "me", ".gemini", "antigravity-cli", "settings.json")) {
			t.Errorf("%+v", a)
		}
	}
	if got := strings.Join(ids, ","); got != "agy@wsl:Ubuntu,codex@wsl:Debian" {
		t.Fatalf("agents %s", got)
	}
}

// agyDistro is a running distro under NAT whose user logs in to zsh and has
// a .bashrc of their own, with agy found by its command.
func agyDistro(t *testing.T, mine string) (d distro, dhome string) {
	t.Helper()
	root := t.TempDir()
	dhome = filepath.Join(root, "home", "me")
	writeFile(t, filepath.Join(root, "etc", "passwd"), "root:x:0:0:root:/root:/bin/bash\nme:x:1000:1000:,,,:/home/me:/usr/bin/zsh\n")
	writeFile(t, filepath.Join(dhome, ".bashrc"), mine)
	return distro{Name: "Ubuntu", Root: root, Home: "/home/me", Has: map[string]bool{"bin:agy": true},
		Gateway: "172.20.0.1", Running: true}, dhome
}

func agyDistroMark(t *testing.T, dhome string) (string, bool) {
	t.Helper()
	key := agyStateKey(filepath.Join(dhome, ".gemini", "antigravity-cli", "settings.json"))
	s := agyReadState(key)
	return s.Model, s.On()
}

// Picking one of magpie's models for agy in a distro puts the function agy
// in the distro's shells' profiles — its .bashrc, and the .zshrc of the
// zsh it logs in to, made — naming the gateway as the distro reaches it
// under NAT, with the WSL key and the model. The model is magpie's own
// record in its stash, never agy's settings. A sync after WSL moved the
// address names the new one, a switch moves the model, and Default takes
// the function out, the user's lines as they were.
func TestWSLAgyPick(t *testing.T) {
	home := syncHome(t)
	fakeWSLNet(t, "172.20.0.1/20")
	mine := "alias ll='ls -l'\n"
	d, dhome := agyDistro(t, mine)
	bashrc, zshrc := filepath.Join(dhome, ".bashrc"), filepath.Join(dhome, ".zshrc")
	settings := filepath.Join(dhome, ".gemini", "antigravity-cli", "settings.json")
	a := wslAgent(wslKindOf("agy"), d)
	if a.Typed() != "" || a.Launch() != "" {
		t.Fatalf("before: %q %q", a.Typed(), a.Launch())
	}
	if err := a.Field("model").Set("magpie/relay/glm-4.6"); err != nil {
		t.Fatal(err)
	}
	gw := "http://172.20.0.1:" + gateway.Port()
	key := agentKeyAt("agy", gw)
	if !strings.HasPrefix(key, "wsl-") || !strings.HasSuffix(key, "-agy") {
		t.Fatalf("key %q", key)
	}
	if m, ok := agyDistroMark(t, dhome); !ok || m != "relay/glm-4.6" {
		t.Fatalf("mark %q %v", m, ok)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatalf("agy's settings were written:\n%s", readFile(settings))
	}
	for _, p := range []string{bashrc, zshrc} {
		body := readFile(p)
		if !strings.Contains(body, "# >>> magpie agy >>>\nfunction agy {\n") ||
			!strings.Contains(body, "AGY_LLM_GATEWAY_URL='"+gw+"'") ||
			!strings.Contains(body, "AGY_LLM_GATEWAY_API_KEY='"+key+"'") ||
			!strings.Contains(body, "AGY_LLM_GATEWAY_WIRE_PROTOCOL=openai") ||
			!strings.Contains(body, "local m='relay/glm-4.6'") ||
			strings.Contains(body, "127.0.0.1") {
			t.Fatalf("%s:\n%s", p, body)
		}
	}
	if !strings.HasPrefix(readFile(bashrc), mine+"\n# >>> magpie agy >>>") {
		t.Fatalf(".bashrc:\n%s", readFile(bashrc))
	}
	if _, err := os.Stat(filepath.Join(dhome, ".config", "fish")); !os.IsNotExist(err) {
		t.Fatal("a fish config for a distro without fish")
	}
	if a.Typed() != "agy" || !strings.Contains(a.Launch(), "AGY_LLM_GATEWAY_URL="+gw+" ") ||
		!strings.Contains(a.Launch(), "--model 'relay/glm-4.6'") {
		t.Fatalf("typed %q, launch %q", a.Typed(), a.Launch())
	}
	if msg := a.Check(); msg != "" {
		t.Fatal(msg)
	}
	if !strings.Contains(a.Notice(), "mirrored") {
		t.Errorf("notice: %s", a.Notice())
	}
	for _, n := range []string{".zshrc", ".bashrc", ".bash_profile", ".config/fish", ".gemini"} {
		if _, err := os.Stat(filepath.Join(home, n)); !os.IsNotExist(err) {
			t.Fatalf("this machine's %s was touched", n)
		}
	}

	// WSL restarted on another address: Check says so, and a sync follows
	d.Gateway = "172.20.5.1"
	a = wslAgent(wslKindOf("agy"), d)
	if a.Check() == "" {
		t.Fatal("the old address passes")
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	moved := "http://172.20.5.1:" + gateway.Port()
	for _, p := range []string{bashrc, zshrc} {
		if body := readFile(p); !strings.Contains(body, "AGY_LLM_GATEWAY_URL='"+moved+"'") || strings.Contains(body, "172.20.0.1") {
			t.Fatalf("%s after the move:\n%s", p, body)
		}
	}
	if msg := a.Check(); msg != "" {
		t.Fatal(msg)
	}

	// another model of magpie's moves the function too
	if err := a.Field("model").Set("magpie/relay/glm-4.5"); err != nil {
		t.Fatal(err)
	}
	if m, _ := agyDistroMark(t, dhome); m != "relay/glm-4.5" {
		t.Fatalf("mark after switch: %q", m)
	}
	for _, p := range []string{bashrc, zshrc} {
		if body := readFile(p); !strings.Contains(body, "local m='relay/glm-4.5'") {
			t.Fatalf("%s after the switch:\n%s", p, body)
		}
	}
	if msg := a.Check(); msg != "" {
		t.Fatal(msg)
	}

	// default: out of every profile and out of magpie's record
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	if _, ok := agyDistroMark(t, dhome); ok {
		t.Fatal("mark left after Default")
	}
	if got := readFile(bashrc); got != mine {
		t.Fatalf(".bashrc:\n%s", got)
	}
	if got := readFile(zshrc); strings.Contains(got, "magpie agy") {
		t.Fatalf(".zshrc:\n%s", got)
	}
	if a.Typed() != "" || a.Launch() != "" {
		t.Fatalf("after: %q %q", a.Typed(), a.Launch())
	}
}

// The function in a distro carries the gateway, key and model itself,
// there being no magpie there to ask: agy starts with them and --model
// (none when one is given); off magpie it starts as it is. Run in bash and
// zsh.
func TestWSLAgyFunctionRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the distro's shells")
	}
	syncHome(t)
	d, dhome := agyDistro(t, "")
	a := wslAgent(wslKindOf("agy"), d)
	if err := a.Field("model").Set("magpie/relay/glm-4.6"); err != nil {
		t.Fatal(err)
	}
	gw := "http://172.20.0.1:" + gateway.Port()
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "agy"), "#!/bin/sh\necho \"url=$AGY_LLM_GATEWAY_URL key=$AGY_LLM_GATEWAY_API_KEY wire=$AGY_LLM_GATEWAY_WIRE_PROTOCOL models=$AGY_LLM_GATEWAY_MODELS gem=$GEMINI_API_KEY args=$*\"\n")
	os.Chmod(filepath.Join(bin, "agy"), 0o755)
	on := "url=" + gw + " key=" + agentKeyAt("agy", gw) + " wire=openai models= gem= args="
	for _, c := range []struct{ sh, rc string }{{"bash", ".bashrc"}, {"zsh", ".zshrc"}} {
		sh, err := exec.LookPath(c.sh)
		if err != nil {
			t.Logf("no %s", c.sh)
			continue
		}
		run := func(args string) string {
			cmd := exec.Command(sh, "-c", `. "$HOME/`+c.rc+`"; agy `+args)
			cmd.Env = []string{"HOME=" + dhome, "PATH=" + bin + ":/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", c.sh, err, out)
			}
			return strings.TrimSpace(string(out))
		}
		if got := run("-p 'hi there'"); got != on+"--model relay/glm-4.6 -p hi there" {
			t.Errorf("%s: %s", c.sh, got)
		}
		if got := run("--model=own -p hi"); got != on+"--model=own -p hi" {
			t.Errorf("%s, a model given: %s", c.sh, got)
		}
	}
	// default: the function is gone, agy starts as it is
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `. "$HOME/.bashrc"; agy -p hi`)
	cmd.Env = []string{"HOME=" + dhome, "PATH=" + bin + ":/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "url= key= wire= models= gem= args=-p hi" {
		t.Fatalf("off magpie: %v %s", err, out)
	}
}

// A stopped distro's agy is never opened: its model is shown as last seen,
// its options come from magpie alone, and a pick starts the distro and
// writes magpie's record and the function there.
func TestWSLAgyStopped(t *testing.T) {
	syncHome(t)
	root := t.TempDir()
	b, _ := json.Marshal(map[string]*distro{
		"Stopped": {Name: "Stopped", Home: "/home/me", Root: root, Has: map[string]bool{"bin:agy": true}, Probe: wslProbeVersion,
			Values: map[string]string{"agy.model": "gemini-3-pro"}},
	})
	os.MkdirAll(filepath.Dir(wslStatePath()), 0o755)
	os.WriteFile(wslStatePath(), b, 0o600)
	asked, opened := fakeWSL(t, "Stopped\r\n", "", nil, nil)
	var a *Agent
	for _, x := range wslAgentsOf(wslDistros()) {
		if x.ID == "agy@wsl:Stopped" {
			a = x
		}
	}
	if a == nil || a.Path != "" || a.Sync != nil {
		t.Fatalf("%+v", a)
	}
	opts := a.Field("model").Options(a.Values())
	var mine, ours bool
	for _, o := range opts {
		mine = mine || o.Value == "gemini-3-pro"
		ours = ours || usesMagpie(o.Value)
	}
	if mine || !ours || len(*asked) != 0 || len(*opened) != 0 {
		t.Fatalf("options %v, asked %v, opened %v", opts, *asked, *opened)
	}
	if es, _ := os.ReadDir(root); len(es) > 0 {
		t.Fatal("a stopped distro was opened")
	}
	if err := a.Field("model").Set("magpie/relay/glm-4.6"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*asked, ",") != "Stopped" {
		t.Fatalf("not started first: %v", *asked)
	}
	if m, ok := agyDistroMark(t, filepath.Join(root, "home", "me")); !ok || m != "relay/glm-4.6" {
		t.Fatalf("mark %q %v", m, ok)
	}
	if body := readFile(filepath.Join(root, "home", "me", ".bashrc")); !strings.Contains(body, "function agy {") ||
		!strings.Contains(body, "local m='relay/glm-4.6'") {
		t.Fatalf(".bashrc:\n%s", body)
	}
}

// Whether a stopped distro's agy is on magpie is magpie's record alone:
// its settings.json, with the old wiring in it here, is never opened, as
// opening it through \\wsl.localhost starts the distro (#3896). A row
// made while the distro ran asks again, and reads it so once it stopped.
func TestWSLAgyStoppedOwnedByRecord(t *testing.T) {
	syncHome(t)
	root := t.TempDir()
	dhome := filepath.Join(root, "home", "me")
	settings := filepath.Join(dhome, ".gemini", "antigravity-cli", "settings.json")
	writeFile(t, settings, `{"model":"magpie/relay/glm-4.6","modelProvider":"gemini","customModelsConfig":{"customModels":{"magpie/relay/glm-4.6":{"apiProvider":"API_PROVIDER_GOOGLE_GEMINI","modelName":"relay/glm-4.6"}}}}`)
	b, _ := json.Marshal(map[string]*distro{
		"Stopped": {Name: "Stopped", Home: "/home/me", Root: root, Has: map[string]bool{"bin:agy": true}, Probe: wslProbeVersion},
	})
	os.MkdirAll(filepath.Dir(wslStatePath()), 0o755)
	os.WriteFile(wslStatePath(), b, 0o600)
	asked, _ := fakeWSL(t, "Stopped\r\n", "", nil, nil)
	var a *Agent
	for _, x := range wslAgentsOf(wslDistros()) {
		if x.ID == "agy@wsl:Stopped" {
			a = x
		}
	}
	if a == nil {
		t.Fatal("no agy row")
	}
	if a.Owned() || len(*asked) != 0 {
		t.Fatalf("owned %v, asked %v: the stopped distro's settings were read", a.Owned(), *asked)
	}
	// a row made while it ran, the distro stopped since
	d := distro{Name: "Stopped", Home: "/home/me", Root: root, Has: map[string]bool{"bin:agy": true}, Running: true,
		up: func() bool { return false }}
	if w := wslAgent(wslKindOf("agy"), d); w.Owned() {
		t.Fatal("the row of a distro stopped since read its settings")
	}
	// magpie's record says so: owned, still without a look
	stash(map[string]string{agyStateKey(settings): `{"model":"relay/glm-4.6","before":{}}`})
	if !a.Owned() || !wslAgent(wslKindOf("agy"), d).Owned() || len(*asked) != 0 {
		t.Fatalf("magpie's record isn't read: %v, asked %v", a.Owned(), *asked)
	}
}

// A stopped distro's agy on magpie is taken off it by Disconnect, as a
// quit's and uninstall's ask: the distro is started first, then the
// modelProvider magpie took out comes back, and magpie's record and the
// function go, the user's own lines as they were.
func TestWSLAgyStoppedDisconnect(t *testing.T) {
	syncHome(t)
	root := t.TempDir()
	dhome := filepath.Join(root, "home", "me")
	mine := "alias ll='ls -l'\n"
	bashrc := filepath.Join(dhome, ".bashrc")
	writeFile(t, bashrc, mine)
	settings := filepath.Join(dhome, ".gemini", "antigravity-cli", "settings.json")
	writeFile(t, settings, `{"model":"mine","modelProvider":"gemini"}`)
	b, _ := json.Marshal(map[string]*distro{
		"Stopped": {Name: "Stopped", Home: "/home/me", Root: root, Has: map[string]bool{"bin:agy": true}, Probe: wslProbeVersion},
	})
	os.MkdirAll(filepath.Dir(wslStatePath()), 0o755)
	os.WriteFile(wslStatePath(), b, 0o600)
	asked, _ := fakeWSL(t, "Stopped\r\n", "", nil, nil)
	row := func() *Agent {
		for _, x := range wslAgentsOf(wslDistros()) {
			if x.ID == "agy@wsl:Stopped" {
				return x
			}
		}
		t.Fatal("no agy row")
		return nil
	}
	if err := row().Field("model").Set("magpie/relay/glm-4.6"); err != nil {
		t.Fatal(err)
	}
	if p, set := edit.GetJSON(settings, "modelProvider"); set || !strings.Contains(readFile(bashrc), "function agy {") {
		t.Fatalf("not on magpie: modelProvider %q\n%s", p, readFile(bashrc))
	}
	*asked = nil
	a := row()
	if a.Owned == nil || !a.Owned() {
		t.Fatal("a stopped distro's agy on magpie isn't owned")
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*asked, ",") != "Stopped" {
		t.Fatalf("not started first: %v", *asked)
	}
	if p, _ := edit.GetJSON(settings, "modelProvider"); p != "gemini" {
		t.Fatalf("modelProvider after Disconnect: %q", p)
	}
	if _, ok := stashShared()[agyStateKey(settings)]; ok {
		t.Fatal("magpie's record left after Disconnect")
	}
	if got := readFile(bashrc); got != mine {
		t.Fatalf(".bashrc after Disconnect:\n%s", got)
	}
}

// A distro whose ~/.gemini is a link it couldn't resolve has an agy row
// that writes nothing there: connecting, picking, syncing and, on magpie,
// Disconnect each say why, agy's settings and magpie's stash as they were.
func TestWSLAgyUnreached(t *testing.T) {
	syncHome(t)
	d, dhome := agyDistro(t, "")
	d.Links = map[string]string{".gemini": ""}
	settings := filepath.Join(dhome, ".gemini", "antigravity-cli", "settings.json")
	writeFile(t, settings, `{"model":"mine"}`)
	a := wslAgent(wslKindOf("agy"), d)
	why := a.Unreached()
	if why == nil {
		t.Fatal("reached")
	}
	still := func(t *testing.T, stash string) {
		t.Helper()
		if got := readFile(settings); got != `{"model":"mine"}` {
			t.Fatalf("settings written:\n%s", got)
		}
		if got := readFile(stashPath()); got != stash {
			t.Fatalf("stash written:\n%s", got)
		}
	}
	stash(map[string]string{"x": "1"})
	before := readFile(stashPath())
	for name, run := range map[string]func() error{
		"connect": a.Connect,
		"pick":    func() error { return a.Field("model").Set("magpie/relay/glm-4.6") },
		"sync":    a.Sync,
	} {
		if err := run(); !errors.Is(err, why) {
			t.Errorf("%s: %v, want %v", name, err, why)
		}
		still(t, before)
	}
	// on magpie, as magpie's record says: Disconnect says why too
	stash(map[string]string{agyStateKey(settings): `{"model":"relay/glm-4.6","before":{"modelProvider":"gemini"},"providerRemoved":true}`})
	before = readFile(stashPath())
	if err := a.Disconnect(); !errors.Is(err, why) {
		t.Fatalf("disconnect: %v, want %v", err, why)
	}
	still(t, before)
}
