package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

var version = "dev" // set by scripts/release.sh via -ldflags

func main() {
	home, err := os.UserHomeDir()
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if err == nil {
		switch cmd {
		case "":
			err = interactive(home)
		case "run":
			err = daily(home)
		case "log":
			err = printLog(home)
		case "uninstall":
			err = uninstall(home, bufio.NewReader(os.Stdin))
		case "update":
			err = selfUpdate(home)
		case "version":
			fmt.Println("smash", version)
		default:
			fmt.Fprintln(os.Stderr, "usage: smash        set up, preview and redact\n       smash run    redact new content (used by the daily job)\n       smash log    show recent scans\n       smash update     install the latest release\n       smash version\n       smash uninstall  remove the daily job, ~/.smash and the installed binary\n\nConfig: ~/.smash/config (PROVIDER=openai|anthropic, OPENAI_API_KEY, ANTHROPIC_API_KEY)")
			os.Exit(2)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "smash:", err)
		os.Exit(1)
	}
}

func smashDir(home string) string { return filepath.Join(home, ".smash") }

func interactive(home string) error {
	in := bufio.NewReader(os.Stdin)
	db, err := openDB(filepath.Join(smashDir(home), "smash.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	ai, label, err := loadConfig(home, db)
	if errors.Is(err, errUndecryptable) {
		fmt.Println("smash can't decrypt the saved API key (its encryption key in ~/.smash/smash.db is missing); set it up again.")
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errUndecryptable) {
		fmt.Println("smash removes API keys and other secrets from your shell history and Claude/Codex logs.")
		fmt.Printf("\nWhich model should look for secrets?\n  1) OpenAI (%s) [default]\n  2) Anthropic (%s)\nChoose [1/2]: ", defaultOpenAIModel, defaultAnthropicModel)
		choice, _ := in.ReadString('\n')
		provider, keyVar, name := "openai", "OPENAI_API_KEY", "OpenAI"
		if strings.TrimSpace(choice) == "2" {
			provider, keyVar, name = "anthropic", "ANTHROPIC_API_KEY", "Anthropic"
		}
		fmt.Printf("%s API key (input hidden): ", name)
		key := readHidden(in)
		if key == "" {
			return errors.New("no key entered")
		}
		if err := writeConfig(home, db, provider, keyVar, key); err != nil {
			return err
		}
		fmt.Println("Saved to ~/.smash/config, encrypted (readable only by you).")
		ai, label, err = loadConfig(home, db)
	}
	if err != nil {
		return err
	}

	r := &runner{home: home, db: db, out: os.Stderr}
	fmt.Println("\nLooking for suspicious lines (nothing leaves this machine yet)...")
	rep, err := r.runWithProgress()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\nSOURCE\tFILES\tTO SCAN\tSUSPICIOUS LINES\tSKIPPED (ACTIVE)")
	for _, name := range sortedKeys(rep.bySource) {
		s := rep.bySource[name]
		fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%d\n", name, s.files, mb(s.bytes), s.candidates, s.skipped)
	}
	tw.Flush()

	if rep.candidates > 0 && !ask(in, fmt.Sprintf("\nSend %d suspicious lines to %s and redact secrets in place? [y/N] ", rep.candidates, label)) {
		fmt.Println("Nothing sent.")
		return nil
	}
	r.ai = ai
	if rep, err = r.runWithProgress(); err != nil {
		return err
	}
	fmt.Printf("Done: %d redacted, %d rejected in %d files. Details: smash log\n", rep.redacted, rep.rejected, rep.files)
	if rep.redacted > 0 {
		fmt.Println("Restart other open shells: they keep old commands in memory and can write them back on exit.")
	}

	return offerDailyJob(home, in)
}

func daily(home string) error {
	db, err := openDB(filepath.Join(smashDir(home), "smash.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	ai, _, err := loadConfig(home, db)
	if err != nil {
		return err
	}
	r := &runner{home: home, db: db, ai: ai, out: os.Stderr}
	rep, err := r.runWithProgress()
	fmt.Printf("%s scanned %d files (%s), %d suspicious, %d redacted, %d rejected, %d skipped\n",
		time.Now().Format(time.RFC3339), rep.files, mb(rep.bytes), rep.candidates, rep.redacted, rep.rejected, rep.skipped)
	return err
}

func printLog(home string) error {
	db, err := openDB(filepath.Join(smashDir(home), "smash.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT at, source, status, bytes, candidates, redacted, rejected, path FROM scans ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tSOURCE\tSTATUS\tSCANNED\tSUSPICIOUS\tREDACTED\tREJECTED\tFILE")
	for rows.Next() {
		var at, src, status, path string
		var b, c, red, rej int
		if err := rows.Scan(&at, &src, &status, &b, &c, &red, &rej, &path); err != nil {
			return err
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n", at, src, status, mb(b), c, red, rej, strings.Replace(path, home, "~", 1))
	}
	tw.Flush()
	return rows.Err()
}

func readHidden(in *bufio.Reader) string {
	stty := func(arg string) {
		c := exec.Command("stty", arg)
		c.Stdin = os.Stdin
		c.Run() // not a terminal: input just isn't hidden
	}
	stty("-echo")
	defer func() { stty("echo"); fmt.Println() }()
	s, _ := in.ReadString('\n')
	return strings.TrimSpace(s)
}

func ask(in *bufio.Reader, q string) bool {
	fmt.Print(q)
	s, _ := in.ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(s), "y")
}

func mb(n int) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }

func sortedKeys(m map[string]*summary) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// uninstall removes the daily job, ~/.smash (API key, database, logs) and the binary install.sh
// put in ~/.local/bin. A symlink there is removed, never its target. Redactions already made stay.
func uninstall(home string, in *bufio.Reader) error {
	bin := filepath.Join(home, ".local", "bin", "smash")
	var found []string
	for _, p := range append(jobFiles(home), smashDir(home), bin) {
		if _, err := os.Lstat(p); err == nil {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		fmt.Println("smash isn't installed for this user; nothing to remove.")
		return nil
	}
	fmt.Println("This removes:")
	for _, p := range found {
		fmt.Println("  " + strings.Replace(p, home, "~", 1))
	}
	if !ask(in, "Your API key and scan history are deleted; lines already redacted stay redacted. Continue? [y/N] ") {
		fmt.Println("Nothing removed.")
		return nil
	}
	stopDailyJob(home)
	for _, p := range found {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	reloadUnits()
	fmt.Println("smash uninstalled.")
	if exe, err := os.Executable(); err == nil && exe != bin {
		if _, err := os.Stat(exe); err == nil {
			fmt.Printf("This copy (%s) wasn't installed by install.sh; delete it yourself if you no longer need it.\n", exe)
		}
	}
	return nil
}
