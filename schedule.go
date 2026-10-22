package main

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const agentLabel = "com.smash.daily"

func agentPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")
}

func installAgent(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	plist := agentPlist(exe, filepath.Join(smashDir(home), "launchd.log"))
	path := agentPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	exec.Command("launchctl", "bootout", domain, path).Run() // not loaded yet is fine
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, out)
	}
	return nil
}

func agentPlist(exe, logFile string) string {
	esc := func(s string) string {
		var b bytes.Buffer
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>run</string></array>
  <key>StartCalendarInterval</key><dict><key>Hour</key><integer>10</integer><key>Minute</key><integer>0</integer></dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, agentLabel, esc(exe), esc(logFile), esc(logFile))
}

func unitDir(home string) string { return filepath.Join(home, ".config", "systemd", "user") }

// jobFiles lists every daily-job file smash may have installed (launchd on macOS, systemd on Linux).
func jobFiles(home string) []string {
	return []string{agentPath(home), filepath.Join(unitDir(home), "smash.service"), filepath.Join(unitDir(home), "smash.timer")}
}

// offerDailyJob asks to install the daily job this OS supports: launchd on macOS, a systemd user
// timer on Linux, otherwise it prints a crontab line.
func offerDailyJob(home string, in *bufio.Reader) error {
	for _, p := range jobFiles(home) {
		if _, err := os.Stat(p); err == nil {
			return nil // already installed
		}
	}
	switch {
	case runtime.GOOS == "darwin":
		if ask(in, "\nInstall a daily job (launchd, 10:00, catches up after sleep)? [y/N] ") {
			if err := installAgent(home); err != nil {
				return err
			}
			fmt.Println("Installed", agentPath(home))
		}
	case exec.Command("systemctl", "--user", "show-environment").Run() == nil:
		if ask(in, "\nInstall a daily job (systemd user timer, 10:00, catches up after downtime)? [y/N] ") {
			if err := installTimer(home); err != nil {
				return err
			}
			fmt.Println("Installed smash.timer in ~/.config/systemd/user (logs: journalctl --user -u smash)")
			if out, _ := exec.Command("loginctl", "show-user", os.Getenv("USER"), "--property=Linger", "--value").Output(); strings.TrimSpace(string(out)) != "yes" {
				fmt.Println("It runs while you're logged in. To also run while logged out: loginctl enable-linger $USER")
			}
		}
	default:
		exe, _ := os.Executable()
		fmt.Printf("\nTo run daily, add to crontab -e:\n  0 10 * * * %s run\n", exe)
	}
	return nil
}

// systemdUnits returns the service and timer. Persistent=true runs a missed 10:00 slot at the next
// boot or login, like launchd's catch-up after sleep.
func systemdUnits(exe string) (service, timer string) {
	quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(exe) + `"`
	service = "[Unit]\nDescription=smash: redact secrets from shell history and AI agent logs\n\n" +
		"[Service]\nType=oneshot\nExecStart=" + quoted + " run\n"
	timer = "[Unit]\nDescription=Run smash daily\n\n" +
		"[Timer]\nOnCalendar=*-*-* 10:00:00\nPersistent=true\n\n" +
		"[Install]\nWantedBy=timers.target\n"
	return service, timer
}

func installTimer(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	service, timer := systemdUnits(exe)
	if err := os.MkdirAll(unitDir(home), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(unitDir(home), "smash.service"), []byte(service), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(unitDir(home), "smash.timer"), []byte(timer), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", "smash.timer"}} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return nil
}

// stopDailyJob unloads whichever daily job is active; the caller deletes the files, then reloadUnits.
// Commands missing on this OS or a job that isn't loaded are fine.
func stopDailyJob(home string) {
	exec.Command("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid()), agentPath(home)).Run()
	exec.Command("systemctl", "--user", "disable", "--now", "smash.timer").Run()
}

func reloadUnits() { exec.Command("systemctl", "--user", "daemon-reload").Run() }
