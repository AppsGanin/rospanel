package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/AppsGanin/rospanel/internal/plugin/devkit"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
	"github.com/AppsGanin/rospanel/internal/version"
)

const pluginUsage = `rospanel plugin — tools for plugin authors

  rospanel plugin new <dir> [--id <id>]        start a plugin from the template
  rospanel plugin validate <dir|zip>           check it the way the panel will, and start it
  rospanel plugin pack <dir> [-o <file.zip>] [--prev <old.zip>]
                                               build the zip to install (--prev: check migrations)
  rospanel plugin test <dir>                   run test.js
  rospanel plugin dev <dir> [--panel <url> --key <api key>]
                                               try it by hand, reloading on save
                                               (--panel: panel.api goes to that panel's /v1)

Docs: docs/plugins in the RosPanel repository.`

// runPlugin is `rospanel plugin …`. It runs before the panel's own logging is set
// up: an author's laptop has no /var/lib/rospanel, and none of this touches it.
func runPlugin(args []string) {
	if len(args) == 0 {
		fmt.Println(pluginUsage)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	sub, rest := args[0], args[1:]
	var err error
	switch sub {
	case "new":
		err = pluginNew(rest)
	case "validate":
		err = pluginValidate(ctx, rest)
	case "pack":
		err = pluginPack(rest)
	case "test":
		err = pluginTest(ctx, rest)
	case "dev":
		err = pluginDev(ctx, rest)
	case "help", "-h", "--help":
		fmt.Println(pluginUsage)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", sub, pluginUsage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// positional splits "<dir> --flags" so the directory may come first, as the usage
// shows (the flag package stops at the first non-flag).
func positional(fs *flag.FlagSet, args []string) (string, error) {
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return "", err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	if len(pos) != 1 {
		return "", fmt.Errorf("expected one path, got %d", len(pos))
	}
	return pos[0], nil
}

func pluginNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	id := fs.String("id", "", "plugin id (default: the directory name)")
	dir, err := positional(fs, args)
	if err != nil {
		return err
	}
	if *id == "" {
		*id = strings.ToLower(filepath.Base(filepath.Clean(dir)))
	}
	files, err := devkit.New(dir, *id, version.Version)
	if err != nil {
		return err
	}
	fmt.Printf("created %s in %s:\n  %s\n\nnext: cd %s && rospanel plugin test .\n", *id, dir, strings.Join(files, "\n  "), dir)
	return nil
}

func pluginValidate(ctx context.Context, args []string) error {
	src, err := positional(flag.NewFlagSet("validate", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	raw, err := devkit.ReadPackage(src)
	if err != nil {
		return err
	}
	pkg, err := manifest.Read(raw, version.Version)
	if err != nil {
		return err
	}
	settings := map[string]string{}
	if st, err := os.Stat(src); err == nil && st.IsDir() {
		if cfg, err := devkit.ReadDevConfig(src); err == nil {
			settings = cfg.Settings
		}
	}
	// Required settings the author left empty get a stand-in, so the check reaches
	// the code; onEnable sees "validate" as their value.
	for _, f := range pkg.Manifest.Settings {
		if !f.Optional && f.Kind != "bool" && settings[f.Key] == "" {
			if settings == nil {
				settings = map[string]string{}
			}
			settings[f.Key] = "validate"
			if f.Kind == "select" && len(f.Options) > 0 {
				settings[f.Key] = f.Options[0].Value
			}
			if f.Kind == "number" {
				settings[f.Key] = "1"
			}
		}
	}
	h, err := devkit.Start(ctx, raw, devkit.Options{Settings: settings, PanelVersion: version.Version})
	if err != nil {
		return err
	}
	h.Close()
	m := pkg.Manifest
	fmt.Printf("ok: %s %s — %d KB, exports %s\n", m.ID, m.Version, len(raw)>>10, strings.Join(m.Exports(), ", "))
	return nil
}

func pluginPack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	out := fs.String("o", "", "output file (default: <id>-<version>.zip)")
	prev := fs.String("prev", "", "the previous release's zip, to check migrations were only added")
	dir, err := positional(fs, args)
	if err != nil {
		return err
	}
	raw, skipped, err := devkit.Pack(dir)
	if err != nil {
		return err
	}
	pkg, err := manifest.Read(raw, version.Version)
	if err != nil {
		return err
	}
	if *prev != "" {
		old, err := os.ReadFile(*prev)
		if err != nil {
			return err
		}
		oldPkg, err := manifest.Read(old, "")
		if err != nil {
			return fmt.Errorf("--prev: %w", err)
		}
		if err := devkit.CheckMigrations(oldPkg, pkg); err != nil {
			return err
		}
	}
	if *out == "" {
		*out = fmt.Sprintf("%s-%s.zip", pkg.Manifest.ID, pkg.Manifest.Version)
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s — %d KB, sha256 %s\n", *out, len(raw)>>10, pkg.SHA256)
	if len(skipped) > 0 {
		fmt.Printf("not packed: %s\n", strings.Join(skipped, ", "))
	}
	return nil
}

func pluginTest(ctx context.Context, args []string) error {
	dir, err := positional(flag.NewFlagSet("test", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	results, err := devkit.RunTests(ctx, dir, version.Version, os.Stdout)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
		}
	}
	fmt.Printf("\n%d passed, %d failed\n", len(results)-failed, failed)
	if failed > 0 {
		os.Exit(1)
	}
	return nil
}

func pluginDev(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	panelURL := fs.String("panel", "", "a panel's API base, https://host/<api path> (the part before /v1)")
	key := fs.String("key", "", "an API key of that panel")
	dir, err := positional(fs, args)
	if err != nil {
		return err
	}
	var remote *devkit.Remote
	if *panelURL != "" || *key != "" {
		if *panelURL == "" || *key == "" {
			return fmt.Errorf("--panel and --key go together")
		}
		remote = &devkit.Remote{Base: *panelURL, Key: *key}
	}
	return devkit.Dev(ctx, dir, version.Version, remote, os.Stdin, os.Stdout)
}
