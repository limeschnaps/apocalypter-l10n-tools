package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apocalypter-l10n-tools/internal/dictionary"
	"apocalypter-l10n-tools/internal/editor/index"
	"apocalypter-l10n-tools/internal/patch"
	"apocalypter-l10n-tools/internal/po"
)

func TestRunServesAndShutsDown(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	errc := make(chan error, 1)
	go func() { errc <- run(ctx, []string{"-addr", "127.0.0.1:0", root}, io.Discard, ready) }()

	var addr string
	select {
	case addr = <-ready:
	case err := <-errc:
		t.Fatalf("run exited early: %v", err)
	}
	resp, err := http.Get("http://" + addr + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"files":0`) {
		t.Errorf("stats: %d %s", resp.StatusCode, body)
	}

	cancel()
	if err := <-errc; err != nil {
		t.Errorf("run: %v", err)
	}
}

func TestRunErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cases := map[string][]string{
		"unknown flag":   {"-nope"},
		"bad level":      {"-root", root, "-log-level", "loud"},
		"bad addr":       {"-root", root, "-addr", "256.0.0.1:bad"},
		"two roots":      {root, root},
		"game and root":  {"-game", root, root},
		"game and -root": {"-root", root, "-game", root},
		"game no bundle": {"-game", root},
	}
	for name, args := range cases {
		if err := run(context.Background(), args, &out, nil); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if err := run(context.Background(), []string{"-root", t.TempDir()}, &out, nil); !errors.Is(err, index.ErrNotUnityProject) {
		t.Errorf("non-Unity root: %v", err)
	}
}

func TestRunDump(t *testing.T) {
	root := t.TempDir()
	const uiGUID = "44444444444444444444444444444444"
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Assets/Plugins/UnityEngine.UI.dll.meta", "fileFormatVersion: 2\nguid: "+uiGUID+"\n")
	label := func(id, owner, text string) string {
		return "--- !u!1 &" + id + "0\nGameObject:\n  m_Name: " + owner + "\n--- !u!114 &" + id + "1\nMonoBehaviour:\n" +
			"  m_GameObject: {fileID: " + id + "0}\n  m_Script: {fileID: 708705254, guid: " + uiGUID + ", type: 3}\n  m_Text: " + text + "\n"
	}
	write("Assets/UI/Shop.prefab", "%YAML 1.1\n"+label("1", "Price", "Nuts")+label("2", "Title", "Shop"))
	write("Assets/UI/Bag.prefab", "%YAML 1.1\n"+label("3", "Item", "Nuts"))
	out := filepath.Join(t.TempDir(), "ru")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	poPath := filepath.Join(out, dictionary.POName)
	readPO := func() *po.File {
		t.Helper()
		data, err := os.ReadFile(poPath)
		if err != nil {
			t.Fatal(err)
		}
		f, err := po.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	if err := runDump(context.Background(), []string{"-o", out, root}, io.Discard); err != nil {
		t.Fatalf("dump: %v", err)
	}
	f := readPO()
	if len(f.Messages) != 2 || f.Messages[0].ID != "Nuts" || f.Messages[1].ID != "Shop" || !strings.Contains(f.Header.Str, "Language: ru") {
		t.Fatalf("po = %+v", f)
	}
	mapData, err := os.ReadFile(filepath.Join(out, dictionary.MapName))
	if err != nil {
		t.Fatal(err)
	}
	if entries, _, err := dictionary.Decode(mapData, po.Marshal(f)); err != nil || len(entries) != 2 || len(entries[0].FoundIn) != 2 {
		t.Errorf("decoded = %+v, %v", entries, err)
	}

	// A second run keeps translations; a translation of a vanished string
	// turns obsolete.
	f.Messages[0].Str = "Гайки"
	f.Messages = append(f.Messages, po.Message{ID: "Gone", Str: "Пропало"})
	if err := os.WriteFile(poPath, po.Marshal(f), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := runDump(context.Background(), []string{"-o", out, "-root", root}, &log); err != nil {
		t.Fatalf("dump again: %v", err)
	}
	f = readPO()
	if len(f.Messages) != 3 || f.Messages[0].Str != "Гайки" || f.Messages[1].Str != "" || !f.Messages[2].Obsolete {
		t.Errorf("po after rerun = %+v", f.Messages)
	}
	if !strings.Contains(log.String(), "translation_obsolete") || !strings.Contains(log.String(), `"translated":1`) {
		t.Errorf("log = %s", log.String())
	}

	// Strings the journal already translated stay out of the dictionary.
	journal := filepath.Join(t.TempDir(), "patches.json")
	if err := patch.Append(journal, patch.Patch{Owner: "Title", Old: "Old shop", New: "Shop"}); err != nil {
		t.Fatal(err)
	}
	if err := runDump(context.Background(), []string{"-o", out, "-patches", journal, root}, io.Discard); err != nil {
		t.Fatalf("dump with journal: %v", err)
	}
	if f := readPO(); len(f.Messages) != 2 || f.Messages[0].ID != "Nuts" || !f.Messages[1].Obsolete {
		t.Errorf("po with journal = %+v", f.Messages)
	}

	badDir := t.TempDir()
	bad := filepath.Join(badDir, dictionary.POName)
	if err := os.WriteFile(bad, []byte("msgid"), 0o644); err != nil {
		t.Fatal(err)
	}
	failures := map[string][]string{
		"no -o":       {root},
		"bad flag":    {"-nope"},
		"bad po":      {"-o", badDir, root},
		"not unity":   {"-o", out, t.TempDir()},
		"bad journal": {"-o", out, "-patches", bad, root},
		"bad dest":    {"-o", filepath.Join(t.TempDir(), "no"), root},
	}
	for name, args := range failures {
		if err := runDump(context.Background(), args, io.Discard); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
