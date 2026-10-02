// Command patcher applies a patch journal recorded by editor
// to the data.unity3d bundle of a Unity player build.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"apocalypter-l10n-tools/internal/dictionary"
	"apocalypter-l10n-tools/internal/gamepatch"
	"apocalypter-l10n-tools/internal/locpack"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/scriptlayout"
	"apocalypter-l10n-tools/internal/unityfs"
)

const (
	bundleName   = gamepatch.BundleName
	backupSuffix = gamepatch.BackupSuffix
	packageExt   = ".lang"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, logOut io.Writer) error {
	if len(args) > 0 && args[0] == "pack" {
		return runPack(args[1:], logOut)
	}
	flags := flag.NewFlagSet("patcher", flag.ContinueOnError)
	flags.SetOutput(logOut)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: patcher -package ru.lang (-out FILE | -in-place | -dry-run) <Game_Data dir | data.unity3d>")
		fmt.Fprintln(flags.Output(), "       patcher [-patches patches.json] [-font NAME=FILE.ttf ...] (-out FILE | -in-place | -dry-run) <game>")
		fmt.Fprintln(flags.Output(), "       patcher -list-fonts <game>")
		fmt.Fprintln(flags.Output(), "       patcher pack -s l10n/ru [-o ru.lang]")
		fmt.Fprintln(flags.Output(), "       patcher pack -o ru.lang [-patches patches.json] [-map translation.map -po translation.po] [-fonts fonts.json]")
		flags.PrintDefaults()
	}
	packagePath := flags.String("package", "", "localization package built by 'patcher pack'")
	patchesPath := flags.String("patches", "", "patch journal written by editor")
	out := flags.String("out", "", "write the patched bundle to this file")
	inPlace := flags.Bool("in-place", false, "replace the game bundle, keeping the original as data.unity3d"+backupSuffix)
	strict := flags.Bool("strict", false, "fail when a patch matches more than one object")
	dryRun := flags.Bool("dry-run", false, "match patches and report without writing")
	listFonts := flags.Bool("list-fonts", false, "list Font objects of the current bundle and their Russian coverage, then exit")
	var fonts []gamepatch.FontReplacement
	flags.Func("font", "replace the TTF data of the Font named NAME with FILE (repeatable)", func(v string) error {
		name, file, ok := strings.Cut(v, "=")
		if !ok || name == "" || file == "" {
			return errors.New("expected NAME=FILE")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		fonts = append(fonts, gamepatch.FontReplacement{Name: name, Data: data})
		return nil
	})
	fontMetrics := flags.String("font-metrics", string(gamepatch.MetricsOriginal), `vertical metrics for -font replacements: "original" keeps the replaced font's line layout, "font" takes them from the new file`)
	if err := flags.Parse(args); err != nil {
		return err
	}
	metrics, err := gamepatch.ParseMetrics(*fontMetrics)
	if err != nil {
		return err
	}
	for i := range fonts {
		fonts[i].Metrics = metrics
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return errors.New("expected one game path")
	}
	logger := slog.New(slog.NewJSONHandler(logOut, nil))

	target, err := gamepatch.BundlePath(flags.Arg(0))
	if err != nil {
		return err
	}
	// Patching always starts from the pristine bundle; listing shows what
	// the game uses now.
	source := target
	backup := target + backupSuffix
	if !*listFonts {
		if source, err = gamepatch.PristinePath(target); err != nil {
			return err
		}
	}

	if !*listFonts {
		if *packagePath != "" && (*patchesPath != "" || len(fonts) > 0) {
			return errors.New("-package cannot be combined with -patches or -font")
		}
		if *packagePath == "" && *patchesPath == "" && len(fonts) == 0 {
			flags.Usage()
			return errors.New("nothing to do: pass -package, -patches or -font")
		}
		if !*dryRun && (*out == "") == !*inPlace {
			return errors.New("specify exactly one of -out, -in-place or -dry-run")
		}
	}
	var patches []patch.Patch
	var sounds []gamepatch.SoundReplacement
	switch {
	case *packagePath != "":
		pkg, err := locpack.Read(*packagePath)
		if err != nil {
			return err
		}
		patches, fonts, sounds = pkg.Patches, pkg.Fonts, pkg.Sounds
	case *patchesPath != "":
		if patches, err = patch.Load(*patchesPath); err != nil {
			return err
		}
		if len(patches) == 0 {
			return fmt.Errorf("%s: no patches", *patchesPath)
		}
	}

	f, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open bundle: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat bundle: %w", err)
	}
	b, err := unityfs.Open(f, info.Size())
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	if *listFonts {
		return printFonts(stdout, b)
	}
	logger.Info("bundle_opened", "path", source, "nodes", len(b.Nodes), "patches", len(patches), "fonts", len(fonts), "sounds", len(sounds))

	var layouts *scriptlayout.Resolver
	if len(patches) > 0 {
		layouts = loadLayouts(logger, filepath.Join(filepath.Dir(target), gamepatch.ManagedDir))
	}
	rep, nodes, applyErr := gamepatch.Apply(b, patches, gamepatch.Options{Strict: *strict, Layouts: layouts, Fonts: fonts, Sounds: sounds})
	logReport(logger, rep)
	if applyErr != nil {
		return fmt.Errorf("nothing was written: %w", applyErr)
	}
	if *dryRun {
		logger.Info("dry_run_complete", "changed_files", len(nodes))
		return nil
	}

	dest := *out
	if *inPlace {
		dest = target
	}
	tmp, err := writeTempBundle(dest, info.Mode().Perm(), b, nodes)
	if err != nil {
		return err
	}
	// Windows refuses to rename a file that is still open, so the source
	// must be closed before the bundle is moved into place.
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close bundle: %w", err)
	}
	if err := commitBundle(tmp, dest, *inPlace && source == target, backup); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	logger.Info("bundle_written", "path", dest, "changed_files", len(nodes), "source", source)
	return nil
}

// loadLayouts returns the layout resolver over the game's script
// assemblies, which lets patches match by field path. Without them patches
// match by occurrence, which may also change other components of the same
// GameObject, so the failure is logged rather than fatal.
func loadLayouts(logger *slog.Logger, dir string) *scriptlayout.Resolver {
	resolver, skipped, err := scriptlayout.LoadDir(dir)
	if err != nil {
		logger.Warn("script_assemblies_unavailable", "dir", dir, "error", err.Error(),
			"hint", "patches match by occurrence and may change other components of the same GameObject")
		return nil
	}
	for _, e := range skipped {
		logger.Debug("script_assembly_skipped", "error", e.Error())
	}
	return resolver
}

func logReport(logger *slog.Logger, rep gamepatch.Report) {
	for i, r := range rep.Patches {
		attrs := []any{"index", i + 1, "file", r.Patch.File, "path", r.Patch.Path, "owner", r.Patch.Owner, "targets", len(r.Targets)}
		if r.Err != nil {
			logger.Error("patch_failed", append(attrs, "error", r.Err.Error())...)
			continue
		}
		logger.Info("patch_matched", append(attrs, "objects", objectList(r.Targets))...)
	}
	for _, r := range rep.Fonts {
		logger.Info("font_replaced", "name", r.Name, "family", r.Family, "metrics", string(r.Metrics),
			"original_line_em", round3(r.OriginalLineHeight), "font_line_em", round3(r.FontLineHeight), "objects", objectList(r.Targets))
		if r.Metrics == gamepatch.MetricsFont && r.FontLineHeight > r.OriginalLineHeight*1.001 {
			logger.Warn("font_line_height_grows", "name", r.Name, "family", r.Family,
				"percent", math.Round((r.FontLineHeight/r.OriginalLineHeight-1)*100),
				"hint", "UI.Text with vertical truncation hides lines taller than its rect; use metrics \"original\" to keep the layout")
		}
		if len(r.Missing) > 0 {
			logger.Warn("font_missing_characters", "name", r.Name, "family", r.Family, "missing", string(r.Missing))
		}
	}
	for _, r := range rep.Sounds {
		logger.Info("sound_replaced", "name", r.Name, "channels", r.Channels, "rate", r.Rate, "samples", r.Samples, "objects", objectList(r.Targets))
	}
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func objectList(targets []gamepatch.Target) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, fmt.Sprintf("%s:%d", t.File, t.PathID))
	}
	return out
}

func printFonts(w io.Writer, b *unityfs.Bundle) error {
	fonts, err := gamepatch.ListFonts(b)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tFAMILY\tFILE\tPATH_ID\tTTF_BYTES\tLINE_EM\tRUSSIAN")
	total := utf8.RuneCountInString(gamepatch.RussianAlphabet)
	for _, f := range fonts {
		russian := "ok"
		switch {
		case f.Err != nil:
			russian = "unreadable: " + f.Err.Error()
		case len(f.Missing) > 0:
			russian = fmt.Sprintf("missing %d/%d: %s", len(f.Missing), total, string(f.Missing))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%.3f\t%s\n", f.Name, f.Family, f.File, f.PathID, f.DataSize, f.LineHeight, russian)
	}
	return tw.Flush()
}

func runPack(args []string, logOut io.Writer) error {
	flags := flag.NewFlagSet("patcher pack", flag.ContinueOnError)
	flags.SetOutput(logOut)
	source := flags.String("s", "", "localization directory with any of "+locpack.PatchesName+", "+dictionary.MapName+" with "+dictionary.POName+", and "+locpack.FontsName+"; the package defaults to ./<dir name>"+packageExt)
	out := flags.String("o", "", "output package file (required without -s)")
	var src locpack.Sources
	flags.StringVar(&src.Patches, "patches", "", "patch journal written by editor")
	flags.StringVar(&src.Map, "map", "", "translation.map written by 'editor dump'; requires -po")
	flags.StringVar(&src.PO, "po", "", "translation.po matching -map; translated messages are applied after the journal")
	flags.StringVar(&src.Fonts, "fonts", "", "fonts.json; font paths in it are relative to its directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return errors.New("unexpected positional arguments")
	}
	if *source != "" {
		if src != (locpack.Sources{}) {
			return errors.New("-s cannot be combined with -patches, -map, -po or -fonts")
		}
		var err error
		if src, err = sourceFiles(*source); err != nil {
			return err
		}
		if *out == "" {
			abs, err := filepath.Abs(*source)
			if err != nil {
				return fmt.Errorf("resolve %s: %w", *source, err)
			}
			*out = filepath.Base(abs) + packageExt
		}
	}
	if *out == "" {
		flags.Usage()
		return errors.New("expected -o or -s")
	}
	logger := slog.New(slog.NewJSONHandler(logOut, nil))

	var buf bytes.Buffer
	res, err := locpack.Pack(&buf, src)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(*out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	for _, f := range res.Fonts {
		logger.Info("font_packed", "name", f.Name, "entry", f.Entry, "family", f.Family, "metrics", f.Metrics, "font_line_em", round3(f.LineHeight))
		if len(f.Missing) > 0 {
			logger.Warn("font_missing_characters", "name", f.Name, "family", f.Family, "missing", string(f.Missing))
		}
	}
	for _, s := range res.Sounds {
		logger.Info("sound_packed", "name", s.Name, "entry", s.Entry, "channels", s.Channels, "rate", s.Rate, "samples", s.Samples)
	}
	if res.Fuzzy > 0 {
		logger.Warn("dictionary_fuzzy_skipped", "messages", res.Fuzzy)
	}
	logger.Info("package_written", "path", *out, "patches", res.Patches, "dictionary_patches", res.DictionaryPatches, "fonts", len(res.Fonts), "sounds", len(res.Sounds), "bytes", buf.Len())
	return nil
}

// sourceFiles returns the journal, the dictionary, fonts.json and
// sounds.json inside dir; a file that does not exist is returned as an empty path.
func sourceFiles(dir string) (locpack.Sources, error) {
	var src locpack.Sources
	info, err := os.Stat(dir)
	if err != nil {
		return src, fmt.Errorf("localization directory: %w", err)
	}
	if !info.IsDir() {
		return src, fmt.Errorf("localization directory: %s is not a directory", dir)
	}
	found := func(name string) (string, error) {
		p := filepath.Join(dir, name)
		switch _, err := os.Stat(p); {
		case err == nil:
			return p, nil
		case errors.Is(err, fs.ErrNotExist):
			return "", nil
		default:
			return "", fmt.Errorf("stat %s: %w", p, err)
		}
	}
	if src.Patches, err = found(locpack.PatchesName); err != nil {
		return src, err
	}
	if src.Map, err = found(dictionary.MapName); err != nil {
		return src, err
	}
	if src.PO, err = found(dictionary.POName); err != nil {
		return src, err
	}
	if src.Fonts, err = found(locpack.FontsName); err != nil {
		return src, err
	}
	if src.Sounds, err = found(locpack.SoundsName); err != nil {
		return src, err
	}
	if src == (locpack.Sources{}) {
		return src, fmt.Errorf("%s contains none of %s, %s with %s, and %s", dir, locpack.PatchesName, dictionary.MapName, dictionary.POName, locpack.FontsName)
	}
	return src, nil
}

func writeFileAtomic(dest string, data []byte, perm fs.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dest, err)
	}
	if err = os.Chmod(tmp.Name(), perm); err != nil {
		return fmt.Errorf("chmod %s: %w", dest, err)
	}
	if err = os.Rename(tmp.Name(), dest); err != nil {
		return fmt.Errorf("replace %s: %w", dest, err)
	}
	return nil
}

// writeTempBundle writes the patched bundle to a temp file next to dest
// and returns its path.
func writeTempBundle(dest string, perm fs.FileMode, b *unityfs.Bundle, nodes map[string][]byte) (path string, err error) {
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp bundle: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = gamepatch.Write(tmp, b, nodes); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp bundle: %w", err)
	}
	if err = os.Chmod(tmp.Name(), perm); err != nil {
		return "", fmt.Errorf("chmod temp bundle: %w", err)
	}
	return tmp.Name(), nil
}

// commitBundle moves the temp bundle to dest. With makeBackup, dest is
// first renamed to backup, which must not exist yet.
func commitBundle(tmp, dest string, makeBackup bool, backup string) error {
	if makeBackup {
		if _, err := os.Stat(backup); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("backup %s already exists", backup)
		}
		if err := os.Rename(dest, backup); err != nil {
			return fmt.Errorf("create backup: %w", err)
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("replace bundle: %w", err)
	}
	return nil
}
