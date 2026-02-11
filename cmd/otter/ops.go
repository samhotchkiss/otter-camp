package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	localServerHealthURL = "http://127.0.0.1:4200/health"
	localBridgeHealthURL = "http://127.0.0.1:8787/health"

	launchLabelServer = "com.ottercamp.server"
	launchLabelBridge = "com.ottercamp.bridge"
)

func handleLocalControl(command string, args []string) {
	if err := runLocalControl(command, args, os.Stdout, os.Stderr); err != nil {
		die(err.Error())
	}
}

func runLocalControl(command string, args []string, out io.Writer, errOut io.Writer) error {
	root, deep, err := parseLocalControlOptions(command, args)
	if err != nil {
		return err
	}
	resolvedRoot, err := resolveLocalRepoRoot(root)
	if err != nil {
		return err
	}

	switch command {
	case "start", "stop", "status":
		return runMakeTarget(resolvedRoot, command, out, errOut)
	case "restart":
		if err := runMakeTarget(resolvedRoot, "stop", out, errOut); err != nil {
			fmt.Fprintf(errOut, "warning: stop failed: %v\n", err)
		}
		return runMakeTarget(resolvedRoot, "start", out, errOut)
	case "repair":
		return runLocalRepair(resolvedRoot, deep, out, errOut)
	default:
		return fmt.Errorf("unsupported local control command %q", command)
	}
}

func parseLocalControlOptions(command string, args []string) (string, bool, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "otter-camp repo root")
	deep := flags.Bool("deep", false, "run deep repair (invokes scripts/setup.sh --yes)")
	if err := flags.Parse(args); err != nil {
		return "", false, err
	}
	if len(flags.Args()) != 0 {
		return "", false, fmt.Errorf("usage: otter %s [--root <path>]", command)
	}
	if command != "repair" && *deep {
		return "", false, fmt.Errorf("--deep is only valid for 'otter repair'")
	}
	return strings.TrimSpace(*root), *deep, nil
}

func runLocalRepair(repoRoot string, deep bool, out io.Writer, errOut io.Writer) error {
	fmt.Fprintf(out, "Running local repair in %s\n", repoRoot)

	if deep {
		fmt.Fprintln(out, "Deep repair: running scripts/setup.sh --yes")
		if err := runCommand(repoRoot, out, errOut, "bash", "scripts/setup.sh", "--yes"); err != nil {
			return fmt.Errorf("deep repair setup failed: %w", err)
		}
	} else {
		if _, err := os.Stat(filepath.Join(repoRoot, ".env")); err != nil {
			fmt.Fprintln(errOut, "warning: .env missing in repo root")
		}
		if _, err := os.Stat(filepath.Join(repoRoot, "bridge", ".env")); err != nil {
			fmt.Fprintln(errOut, "warning: bridge/.env missing; bridge may not connect to OpenClaw")
		}
	}

	if err := runMakeTarget(repoRoot, "stop", out, errOut); err != nil {
		fmt.Fprintf(errOut, "warning: stop failed during repair: %v\n", err)
	}
	if err := runMakeTarget(repoRoot, "prod-build", out, errOut); err != nil {
		return fmt.Errorf("build failed during repair: %w", err)
	}
	if err := runMakeTarget(repoRoot, "start", out, errOut); err != nil {
		return fmt.Errorf("start failed during repair: %w", err)
	}

	if !checkLocalHealth(localServerHealthURL, 3*time.Second) {
		tail := tailFile(filepath.Join("/tmp", "ottercamp-server.log"), 1600)
		if strings.TrimSpace(tail) != "" {
			fmt.Fprintln(errOut, "--- /tmp/ottercamp-server.log (tail) ---")
			fmt.Fprintln(errOut, tail)
		}
		return fmt.Errorf("server health check failed (%s)", localServerHealthURL)
	}

	bridgeEnvPath := filepath.Join(repoRoot, "bridge", ".env")
	if _, err := os.Stat(bridgeEnvPath); err == nil && !checkLocalHealth(localBridgeHealthURL, 3*time.Second) {
		tail := tailFile(filepath.Join("/tmp", "ottercamp-bridge.log"), 1600)
		if strings.TrimSpace(tail) != "" {
			fmt.Fprintln(errOut, "--- /tmp/ottercamp-bridge.log (tail) ---")
			fmt.Fprintln(errOut, tail)
		}
		return fmt.Errorf("bridge health check failed (%s)", localBridgeHealthURL)
	}

	fmt.Fprintln(out, "Repair complete.")
	return nil
}

func handleAutostart(args []string) {
	if err := runAutostart(args, os.Stdout, os.Stderr); err != nil {
		die(err.Error())
	}
}

func runAutostart(args []string, out io.Writer, errOut io.Writer) error {
	if runtime.GOOS != "darwin" {
		return errors.New("otter autostart currently supports macOS launchd only")
	}
	if len(args) == 0 {
		return errors.New("usage: otter autostart <enable|disable|status> [--root <path>]")
	}

	sub := strings.ToLower(strings.TrimSpace(args[0]))
	flags := flag.NewFlagSet("autostart "+sub, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "otter-camp repo root")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return fmt.Errorf("usage: otter autostart %s [--root <path>]", sub)
	}

	switch sub {
	case "enable":
		resolvedRoot, err := resolveLocalRepoRoot(strings.TrimSpace(*root))
		if err != nil {
			return err
		}
		return enableAutostart(resolvedRoot, out, errOut)
	case "disable":
		return disableAutostart(out, errOut)
	case "status":
		return autostartStatus(out, errOut, false)
	default:
		return errors.New("usage: otter autostart <enable|disable|status> [--root <path>]")
	}
}

func enableAutostart(repoRoot string, out io.Writer, errOut io.Writer) error {
	if err := runMakeTarget(repoRoot, "prod-build", out, errOut); err != nil {
		return fmt.Errorf("build failed before enabling autostart: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return err
	}

	serverPlistPath := filepath.Join(agentsDir, launchLabelServer+".plist")
	bridgePlistPath := filepath.Join(agentsDir, launchLabelBridge+".plist")

	if err := os.WriteFile(serverPlistPath, []byte(renderServerLaunchAgent(repoRoot)), 0o644); err != nil {
		return err
	}
	if err := reloadLaunchAgent(launchLabelServer, serverPlistPath, errOut); err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(repoRoot, "bridge", ".env")); err == nil {
		if err := os.WriteFile(bridgePlistPath, []byte(renderBridgeLaunchAgent(repoRoot)), 0o644); err != nil {
			return err
		}
		if err := reloadLaunchAgent(launchLabelBridge, bridgePlistPath, errOut); err != nil {
			return err
		}
		fmt.Fprintf(out, "Autostart enabled for server + bridge (%s, %s)\n", launchLabelServer, launchLabelBridge)
	} else {
		fmt.Fprintf(out, "Autostart enabled for server (%s)\n", launchLabelServer)
		fmt.Fprintln(errOut, "warning: bridge/.env missing, bridge autostart not enabled")
	}

	return autostartStatus(out, errOut, true)
}

func disableAutostart(out io.Writer, errOut io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	serverPlistPath := filepath.Join(agentsDir, launchLabelServer+".plist")
	bridgePlistPath := filepath.Join(agentsDir, launchLabelBridge+".plist")

	_ = unloadLaunchAgent(launchLabelServer, serverPlistPath, errOut)
	_ = unloadLaunchAgent(launchLabelBridge, bridgePlistPath, errOut)

	_ = os.Remove(serverPlistPath)
	_ = os.Remove(bridgePlistPath)
	fmt.Fprintln(out, "Autostart disabled.")
	return nil
}

func autostartStatus(out io.Writer, errOut io.Writer, waitForHealth bool) error {
	target := launchctlTarget()
	printOne := func(label string) {
		cmd := exec.Command("launchctl", "print", target+"/"+label)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		if err != nil {
			fmt.Fprintf(out, "%s: not loaded\n", label)
			return
		}
		state := parseLaunchctlState(buf.String())
		if state == "" {
			state = "loaded"
		}
		fmt.Fprintf(out, "%s: %s\n", label, state)
	}

	printOne(launchLabelServer)
	printOne(launchLabelBridge)

	serverHealthy := false
	bridgeHealthy := false
	if waitForHealth {
		serverHealthy = checkLocalHealthEventually(localServerHealthURL, 8, 500*time.Millisecond, 2*time.Second)
		bridgeHealthy = checkLocalHealthEventually(localBridgeHealthURL, 8, 500*time.Millisecond, 2*time.Second)
	} else {
		serverHealthy = checkLocalHealth(localServerHealthURL, 2*time.Second)
		bridgeHealthy = checkLocalHealth(localBridgeHealthURL, 2*time.Second)
	}

	if !serverHealthy {
		fmt.Fprintf(errOut, "warning: server health endpoint not responding (%s)\n", localServerHealthURL)
	}
	if !bridgeHealthy {
		fmt.Fprintf(errOut, "warning: bridge health endpoint not responding (%s)\n", localBridgeHealthURL)
	}
	return nil
}

func launchctlTarget() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func reloadLaunchAgent(label, plistPath string, errOut io.Writer) error {
	_ = unloadLaunchAgent(label, plistPath, errOut)
	if err := runCommand("", os.Stdout, errOut, "launchctl", "bootstrap", launchctlTarget(), plistPath); err != nil {
		return fmt.Errorf("failed to bootstrap %s: %w", label, err)
	}
	if err := runCommand("", os.Stdout, errOut, "launchctl", "kickstart", "-k", launchctlTarget()+"/"+label); err != nil {
		return fmt.Errorf("failed to kickstart %s: %w", label, err)
	}
	return nil
}

func unloadLaunchAgent(label, plistPath string, errOut io.Writer) error {
	if err := runCommand("", io.Discard, io.Discard, "launchctl", "bootout", launchctlTarget()+"/"+label); err == nil {
		return nil
	}
	if err := runCommand("", io.Discard, io.Discard, "launchctl", "bootout", launchctlTarget(), plistPath); err == nil {
		return nil
	}
	if err := runCommand("", io.Discard, io.Discard, "launchctl", "unload", plistPath); err == nil {
		return nil
	}
	fmt.Fprintf(errOut, "warning: could not unload %s (it may already be unloaded)\n", label)
	return nil
}

func renderServerLaunchAgent(repoRoot string) string {
	command := fmt.Sprintf("cd %s && STATIC_DIR=./web/dist ./bin/server", shellSingleQuote(repoRoot))
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-lc</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/ottercamp-server.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/ottercamp-server.error.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
</dict>
</plist>
`, launchLabelServer, xmlEscape(command))
}

func renderBridgeLaunchAgent(repoRoot string) string {
	command := fmt.Sprintf("cd %s && set -a && . bridge/.env && set +a && npx tsx bridge/openclaw-bridge.ts continuous", shellSingleQuote(repoRoot))
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-lc</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>ThrottleInterval</key>
  <integer>12</integer>
  <key>StandardOutPath</key>
  <string>/tmp/ottercamp-bridge.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/ottercamp-bridge.error.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
</dict>
</plist>
`, launchLabelBridge, xmlEscape(command))
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func shellSingleQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func parseLaunchctlState(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "state =") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "state ="))
		}
	}
	return ""
}

func resolveLocalRepoRoot(rootOverride string) (string, error) {
	candidates := make([]string, 0, 6)
	if trimmed := strings.TrimSpace(rootOverride); trimmed != "" {
		candidates = append(candidates, trimmed)
	}
	if fromEnv := strings.TrimSpace(os.Getenv("OTTERCAMP_ROOT")); fromEnv != "" {
		candidates = append(candidates, fromEnv)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if gitRoot, err := gitRepoRoot(); err == nil {
		candidates = append(candidates, gitRoot)
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates, exeDir, filepath.Dir(exeDir), filepath.Dir(filepath.Dir(exeDir)))
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		if isOtterCampRepoRoot(abs) {
			return abs, nil
		}
	}

	return "", errors.New("could not locate otter-camp repo root; run from repo root or pass --root <path>")
}

func isOtterCampRepoRoot(path string) bool {
	mustExist := []string{
		filepath.Join(path, "Makefile"),
		filepath.Join(path, "cmd", "server", "main.go"),
		filepath.Join(path, "web", "package.json"),
	}
	for _, required := range mustExist {
		if _, err := os.Stat(required); err != nil {
			return false
		}
	}
	return true
}

func runMakeTarget(repoRoot string, target string, out io.Writer, errOut io.Writer) error {
	return runCommand(repoRoot, out, errOut, "make", target)
}

func runCommand(dir string, out io.Writer, errOut io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = out
	cmd.Stderr = errOut
	return cmd.Run()
}

func checkLocalHealth(url string, timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func checkLocalHealthEventually(url string, attempts int, delay time.Duration, timeout time.Duration) bool {
	if attempts <= 0 {
		attempts = 1
	}
	if delay < 0 {
		delay = 0
	}
	for i := 0; i < attempts; i++ {
		if checkLocalHealth(url, timeout) {
			return true
		}
		if i < attempts-1 && delay > 0 {
			time.Sleep(delay)
		}
	}
	return false
}

func tailFile(path string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(raw) <= maxBytes {
		return string(raw)
	}
	return string(raw[len(raw)-maxBytes:])
}
