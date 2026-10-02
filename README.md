# apocalypter-l10n-tools

Localization tools for Unity games. A player gets an archive for their language with `patcher`, the `*.lang` package and a launcher script that runs `patcher` on the build's `data.unity3d` (see "Installing a localization", "Applying edits to the game" and "Localization package"). Packages are built from the `l10n/<language>/` folders.

`editor` is for package authors. It is a local web tool that searches and edits MonoBehaviour string fields in Unity assets (`.prefab`, `.unity`, `.asset`) or directly in a game build. Every edit goes to the `patches.json` journal. Repeated strings are easier to translate through the dictionary: a `translation.po` file for a PO editor and a `translation.map` file with the places of the strings in the game (see "Dictionary"). `patcher pack` builds a package from the journal and the dictionary.

## Disclaimer

This software is provided "AS IS", without warranty of any kind, express or implied, including but not limited to the warranties of merchantability, fitness for a particular purpose and noninfringement. In no event shall the authors be liable for any claim, damages or other liability arising from the use of this software, including damage to game files or saved games. Use it at your own risk.

This is an unofficial fan project. It is not affiliated with, endorsed by or supported by the developers or publishers of Apocalypter.

## Installing a localization

Players get these steps in more detail, in `INSTALL_RU.txt` (Russian) and `INSTALL_EN.txt` (English) inside the archive.

1. Download the archive for your language: `apocalypter-l10n-tools-<language>-linux-x64.tar.gz` (Linux) or `apocalypter-l10n-tools-<language>-win-x64.zip` (Windows), for example `apocalypter-l10n-tools-ru-win-x64.zip`.
2. Unpack it into the game folder, the one with `Apocalypter.exe`. The files can lie next to `Apocalypter.exe` or in the archive's own subfolder there.
3. Run `patcher.sh` (Linux) or `patcher.bat` (Windows). The script runs `patcher -package <file> -in-place Apocalypter_Data` with the `*.lang` package next to it.
4. If several `*.lang` files lie next to the script, it lists them: type the package number and press Enter; Enter alone picks the first one. A single package is installed without asking.

The first run keeps the original bundle as `Apocalypter_Data/data.unity3d.orig`, so the script can be run again, and another language is installed by unpacking its archive and running its script. After a game update or a file integrity check in Steam, delete `data.unity3d.orig` and run the script again (see "Replacing fonts" for the `backup does not match the game bundle` error).

## Making a new localization

The Russian and Japanese localizations are built with the tools from this repository, and any other language can be added the same way. This section walks through the whole process, from an empty dictionary to a package other players can install. It needs no programming and no Go toolchain: a few commands in a terminal and a PO editor are enough.

### How it works

Apocalypter keeps all its text in one file, `Apocalypter_Data/data.unity3d`. Nobody unpacks or rebuilds it by hand:

1. `editor dump` reads the game and collects every string the player sees into the `translation.po` dictionary. Each unique string appears there once, even if the game uses it in a hundred places.
2. The translator translates the dictionary in any PO editor, for example [Poedit](https://poedit.net/).
3. `patcher pack` packs the translation and the fonts into one `.lang` file.
4. Players run the same `patcher` with this `.lang` file, and it writes the translation into their copy of the game.

### What you need

From the [releases page](https://github.com/limeschnaps/apocalypter-l10n-tools/releases) download two archives for your system:

- the editor: `apocalypter-l10n-tools-editor-win-x64.zip` (Windows) or `apocalypter-l10n-tools-editor-linux-x64.tar.gz` (Linux);
- any language archive, for example `apocalypter-l10n-tools-ru-win-x64.zip`. It provides `patcher` and the launcher script.

Also install a PO editor. Poedit is free and works on Windows and Linux.

### Step 1. Prepare a work folder

Create an empty folder outside the game folder, for example `C:\apocalypter-l10n`. Unpack both archives into it, so that `editor.exe`, `patcher.exe` and `patcher.bat` lie together. Delete `ru.lang` from the language archive: it is not needed.

Pick a language code. It becomes the name of the localization folder and of the package: `editor dump` writes it to the `Language` header of `translation.po`, and `pack -s` names the package after it. Use the standard code: `de` for German, `fr` for French, `pt-BR` for Brazilian Portuguese. The examples below use `de` and the Windows binaries; on Linux drop `.exe`, use `/` in paths and run `./patcher.sh` instead of `patcher.bat`.

Commands run in a terminal opened in the work folder:

- Windows: open the folder in Explorer, type `cmd` in the address bar and press Enter.
- Linux: open a terminal and go to the folder with `cd`.

The commands also need the path to the game's `Apocalypter_Data` folder. In the Steam library right-click Apocalypter and choose Manage → Browse local files. The usual paths are:

- Windows: `C:\Program Files (x86)\Steam\steamapps\common\Apocalypter\Apocalypter_Data`
- Linux: `~/.local/share/Steam/steamapps/common/Apocalypter/Apocalypter_Data`

### Step 2. Create the dictionary

`editor dump` reads the game and writes `translation.po` and `translation.map` into the folder given by `-o` (see "Dictionary"). The folder name becomes the language code, so pass `de`, not a name like `my-translation`.

```sh
editor.exe dump -game "C:\Program Files (x86)\Steam\steamapps\common\Apocalypter\Apocalypter_Data" -o de
./editor dump -game ~/.local/share/Steam/steamapps/common/Apocalypter/Apocalypter_Data -o de
```

The command takes a few seconds and prints a JSON log. Then the `de` folder holds two files:

| File | Purpose |
| --- | --- |
| `translation.po` | the strings; this is the file to translate |
| `translation.map` | where each string lives in the game; never edit it by hand |

An installed translation does not matter. If the editor finds the `data.unity3d.orig` backup, it reads the original English text from it.

### Step 3. Translate

Open `de/translation.po` in Poedit. On the first open Poedit may ask for the language: choose yours. Each entry shows the English original and an empty field for the translation. Above each entry Poedit shows notes like `level1 | percent | m_Text`: the game file, the object name and the field. The object name often hints at where the string appears.

Rules that keep the game working:

- Never change the original text (`msgid`). `translation.map` refers to messages by its hash, and `pack` fails if an original changes.
- Leave a string empty if it needs no translation, such as numbers, units or symbols. Empty strings stay in English in the game.
- Poedit marks uncertain translations as "Needs work" (`fuzzy`). The game skips them as if they were empty. Remove the mark when the translation is ready.
- Keep leading and trailing spaces, line breaks and characters such as `%`, `:` and `/` where the original has them. The game often joins strings, for example a number and a unit.
- Keep the length close to the original. Many labels sit in tight frames, and a long translation gets cut off or disappears (see "Replacing fonts" about `Vertical Overflow = Truncate`).

The dictionary does not have to be translated at once. Translated strings go into the game, the rest stay in English. Save the file regularly and test the result in the game (see step 6).

### Step 4. Fonts

The game fonts are made for English and cover few other letters. If the language uses other letters, for example Cyrillic, Greek, Chinese or Japanese, or Latin letters with diacritics the fonts lack, the game takes missing letters from system fonts. The text then looks mixed or does not show at all. Replacing the game fonts with fonts that cover the alphabet fixes this (see "Replacing fonts").

First list the fonts of the game:

```sh
patcher.exe -list-fonts "C:\Program Files (x86)\Steam\steamapps\common\Apocalypter\Apocalypter_Data"
```

The `NAME` column holds the names `fonts.json` needs: `Helveticrap`, `forcedSquare`, `OpenSans-Regular`, `OpenSans-Semibold` and `OpenSans-Bold`. The `RUSSIAN` column checks the Russian alphabet only. For other languages ignore it and check the letters in the game.

Then pick fonts. [Google Fonts](https://fonts.google.com/) has many fonts under the free OFL license and a filter by language. Choose a font close in style to the original, download the `.ttf` or `.otf` file and put it into `de/fonts/` together with its license file (`OFL.txt`).

Create `de/fonts.json`. Each entry links a Font object of the game (`name`) to a file (`file`, relative to `fonts.json`). The Japanese localization, `l10n/ja/fonts.json`, replaces all five:

```json
{
  "version": 1,
  "fonts": [
    {"name": "Helveticrap", "file": "./fonts/Yomogi-Regular.ttf"},
    {"name": "forcedSquare", "file": "./fonts/MPLUS1p-Medium.ttf"},
    {"name": "OpenSans-Regular", "file": "./fonts/MPLUS1p-Regular.ttf"},
    {"name": "OpenSans-Semibold", "file": "./fonts/MPLUS1p-Medium.ttf"},
    {"name": "OpenSans-Bold", "file": "./fonts/MPLUS1p-Bold.ttf"}
  ]
}
```

List only the fonts to replace. If the original fonts already cover the language, skip this step and do not create `fonts.json`.

By default the new font keeps the line height of the old one (`metrics: original`), so the game layout does not move. Glyphs of a taller font may slightly stick out of the line. This is usually fine, but check menus and settings in the game.

### Step 5. Build the package

```sh
patcher.exe pack -s de
```

`patcher` checks the dictionary and the fonts and writes `de.lang` to the current directory (see "Localization package"). On an error it stops and writes nothing. The common errors:

| Error | Cause and fix |
| --- | --- |
| `msgid ... is missing from translation.map` | an original string in `translation.po` was changed; undo the change or run step 2 again |
| `translation of ... equals old value ... of another entry` | the translation matches another English string of the same owner and script, and `patcher` would overwrite one translation with the other; reword it slightly |
| `font "...": open ...: no such file or directory` (on Windows, `The system cannot find the file specified`) | a font file is not found; check the paths in `fonts.json` |

The `font_missing_characters` warning checks Russian letters and says nothing for a non-Cyrillic language.

### Step 6. Test in the game

Close the game and copy `patcher.exe`, `patcher.bat` and `de.lang` into the game folder, next to `Apocalypter.exe`. Run `patcher.bat`, as a player would (see "Installing a localization"). The script installs the package and keeps the original bundle as `data.unity3d.orig`.

After the translation changes, build the package again (step 5), copy the new `de.lang` and run `patcher.bat` again. The game does not need restoring first: `patcher` always applies the package to the saved original.

A dry run checks a package without touching the game. It reports which strings it found and changes nothing:

```sh
patcher.exe -package de.lang -dry-run "C:\Program Files (x86)\Steam\steamapps\common\Apocalypter\Apocalypter_Data"
```

### Step 7. Share the localization

Players need three files: `patcher.exe`, `patcher.bat` and `de.lang` (`patcher`, `patcher.sh` and `de.lang` on Linux). They can go into an archive of their own with installation instructions. The launcher script picks up any `*.lang` file next to it, so nothing else needs changing.

To get release archives for the language, add the `de` folder with `translation.po`, `translation.map`, `fonts.json` and `fonts/` to `l10n/` in this repository, through an issue or a pull request. `make dist` then builds the archives for every folder there (see "Building"), as for Russian and Japanese.

Always ship the font licenses together with the fonts.

### After a game update

Updates add new strings and change old ones. Run step 2 again with the same `-o` folder. The editor keeps all translations, adds the new strings with an empty `msgstr` and turns translations of removed strings into obsolete `#~` messages at the end of the file. Poedit shows the new strings as untranslated.

Steam must have installed the new version of the game before that. If the game folder still has `data.unity3d.orig` from an older version, delete it first, otherwise the tools stop with `backup does not match the game bundle`.

### Strings outside the dictionary

The dictionary holds only "on screen" strings, the ones the game is certain to show. Some text lives in the game's scripts, and the tools cannot tell whether the player sees it (the "maybe" kind, see "String kinds"). Such strings are translated in the editor's web UI with the journal in the localization folder:

```sh
editor.exe -game "C:\Program Files (x86)\Steam\steamapps\common\Apocalypter\Apocalypter_Data" -patches de\patches.json
```

Open `http://127.0.0.1:8080`, find the string, type the translation and press Save. The editor does not touch the game: it writes each edit to `de/patches.json` (see "Working with a build directly"), and `pack -s` adds this file to the package. The same works for a string that needs different translations in different places.

Do not translate "service" strings. These are names of objects, events and tags, the game logic depends on them, and a translation can break items or quests. The fuel type on vehicle tanks (Gasoline / Diesel) is such a case: the game shows an object tag there, so it stays in English.

After journal edits, run `editor dump` again so that the strings translated in the journal leave the dictionary.

## Layout

```
src/                    the Go module (go.mod)
src/cmd/patcher/        patcher: applies edits and packages, builds *.lang
src/cmd/editor/         editor: web UI for editing strings, dictionary generation
src/internal/           patcher core: bundle, font, package and dictionary formats, .NET assembly parsing and script layouts, string kinds, applying edits
src/internal/editor/    code only editor needs: index, HTTP server, YAML parsing
scripts/                patcher.sh and patcher.bat: launchers that ship with patcher to players
docs/INSTALL_RU.txt     installation instructions for players in Russian, shipped in their archive
docs/INSTALL_EN.txt     the same instructions in English
l10n/<language>/        package sources: patches.json, translation.po with translation.map, and fonts.json
```

## Running editor

```sh
go -C src build -o ../editor ./cmd/editor
./editor /path/to/UnityProject
```

The project root can be passed as a positional argument or with the `-root` flag. A game build can be opened directly, without an AssetRipper export: `./editor -game /path/to/Game/Game_Data` (see "Working with a build directly").

Then open `http://127.0.0.1:8080`.

| Flag | Default | Purpose |
| --- | --- | --- |
| `-root` | `.` | root of the Unity project, the folder with `Assets/` |
| `-game` | — | the build's `*_Data` folder or `data.unity3d`; replaces `-root` |
| `-addr` | `127.0.0.1:8080` | HTTP server address |
| `-log-level` | `info` | `debug`, `info`, `warn` or `error` |
| `-patches` | `<root>/patches.json` | edit journal for `patcher`; with `-game` defaults to `patches.json` in the `*_Data` folder |

Logs go to stderr as JSON.

## Project requirements

Unity must serialize assets as text: **Project Settings → Editor → Asset Serialization → Force Text**. The tool skips binary assets.

Projects extracted with AssetRipper must be exported as a Unity project (YAML), not as "primary content": the latter does not keep MonoBehaviours. If there are no suitable assets, the tool logs an `index_empty` warning.

## What gets indexed

- Assets from `Assets/` and `Packages/`. Unity ignores folders whose names start with a dot or end with `~`, so the tool skips them too.
- Scalar values of all MonoBehaviour fields, including nested structures and lists, for example `m_items[2].title`.
- Empty strings, numbers and `m_EditorClassIdentifier` are not indexed.
- The script name comes from the GUID in `.cs.meta` files under `Assets/`, `Packages/` and `Library/PackageCache/`. Scripts from DLLs show as `Name.dll#fileID`.

Search filters by text (substring or regex, case-sensitive or not), field path, GameObject name, script name and file path. Field filters match a case-insensitive substring. In the API these are the `q`, `field`, `gameObject`, `script` and `file` parameters. In `-game` mode a ScriptableObject is matched by its `m_Name` instead of a GameObject name.

Results are paged by 100, 200, 500 or 1000 rows. A new search or a checkbox change opens the first page; saving an edit keeps the current one. In the API the page is set by the `offset` and `limit` (up to 2000) search parameters; `total` in the response counts all matches.

Clicking a column header sorts the results: ascending, descending, then back to index order (file by file). Comparison ignores case, and numbers inside strings compare by value: `save game 9` comes before `save game 10`, `items[2]` before `items[10]`. Kind sorts in the order "on screen", "maybe", "service"; "GameObject / script" sorts by object, then by script; file sorts by file and position in it. Rows with equal values stay in index order. In the API these are the `sort` (`value`, `kind`, `path`, `owner`, `file`) and `desc=1` parameters. The first sort of all 829 thousand Apocalypter strings with the "service" checkbox on takes about a second; later pages open instantly because the sorted order is kept until the index changes.

## String kinds

Most strings in assets are not player text but names of objects, variables, events and tags, and save keys. In Apocalypter they make up 99.7%: of 829 thousand strings, about 2.8 thousand are text. So the editor splits strings into three kinds and shows the first two by default:

| Kind | What it covers |
| --- | --- |
| on screen | `UI.Text.m_Text`, TextMeshPro `m_text`, `Dropdown` options, literals of PlayMaker actions that display text (`UiTextSetText.text`, `setTextmeshProText.textString`, `GUILabel.text` and others), the initial value of the FSM string variable such a parameter is bound to, and the literals `StringAppend`, `StringAppend2`, `BuildString` or `SetStringValue` build such a variable from, for example `L` in the `/ 60L` label on a fuel tank |
| maybe | strings the game logic may display: fields of the game's own scripts (`Assembly-CSharp`), `InputField` text, initial values of the other FSM string variables, parameters of actions that build text (`SetFsmString.setValue`, `StringAppend`, `BuildString` and others) |
| service | `m_Name`, strings of Unity and third-party libraries, internal parameters of FSM actions (FSM and variable names, tags, events, input buttons, number formats), FSM parameters bound to a variable (the game does not use their literal), Easy Save 3, PlayMaker ArrayMaker proxy names |

Service strings appear in search when the "service" checkbox is on. The "hide maybe" checkbox leaves only strings that are certainly displayed. The status line shows how many strings of each kind are hidden. In the API these are the `service=1` and `hideMaybe=1` search parameters and the `hidden` and `hiddenMaybe` response fields. The editor shows a warning on a service string: translating a name or key can break the game logic.

PlayMaker action parameters live in shared per-state arrays. The editor links each string to its action and parameter through `actionStartIndex`, `paramDataType`, `paramDataPos` and `paramName`, taking parameter arrays (`arrayParamSizes`) and nested classes (`customTypeSizes`) into account. In Apocalypter this matches in all 25 thousand FSM states. If a state's tables contradict each other, all its string parameters get the "maybe" kind. Classification is the same for a YAML export and for `-game`.

## How saving works

1. The index keeps the byte range and the source text of every value.
2. Before writing, the server checks these bytes against the file on disk. If the file has changed, the server returns `409` and reindexes the file, and the UI refreshes the results.
3. The new value is written on one line. Non-ASCII characters are encoded as `\uXXXX`, as Unity itself does.
4. Before writing, the changed file is parsed again and checked against the new value. The write is atomic: a temporary file and `rename`.

## Working with a build directly

With the `-game` flag the editor reads `data.unity3d` itself, so no AssetRipper export is needed. The bundle is unpacked into memory and nothing is written to disk: for Apocalypter, loading and parsing all components takes about 0.6 s and about 700 MB of memory.

```sh
./editor -game /path/to/Game/Game_Data -patches ./l10n/ru/patches.json
```

- The editor reads the original `data.unity3d.orig` if it exists and applies the journal to it. So search shows the text the game will have after `patcher`. A journal edit that finds no target is logged as `journal_patch_failed`.
- Saving changes the string only in memory and appends the edit to the journal. `data.unity3d` stays unchanged; `patcher` carries the edits into the game, as with a YAML export. If the journal cannot be written, the editor rereads the build so that memory matches the journal.
- As in `patcher`, an edit reaches all components with the same owner and script that hold the old value in the same field with the same kind, for example prefab copies in scenes.

### Field names

The build has no typetree, so the editor rebuilds the field layout from the script assemblies in `Managed/` next to `data.unity3d`, as AssetRipper does. For the class of each MonoScript it reads the .NET metadata and applies the Unity 2020.3 serialization rules:

- a field is serialized if it is public or marked `[SerializeField]` and is not `static`, `const`, `readonly` or `[NonSerialized]`;
- base class fields come first, then the class's own, in declaration order;
- `[Serializable]` classes and structs, generic ones included, are expanded inline up to depth 10; `T[]` and `List<T>` are written as arrays; references to `UnityEngine.Object` as PPtr; enums as their underlying integer type;
- types from `mscorlib` and `System.*`, interfaces, abstract classes, delegates and nested collections are not serialized;
- `Vector3`, `Color`, `Rect`, `AnimationCurve`, `Gradient`, `GUIStyle` and other built-in Unity types have a fixed layout defined by native code.

Strings get the same paths as in a YAML export, for example `fsm.states[1].actionData.fsmStringParams[4].value`. So journals from both modes match. In Apocalypter the layout fits all 13,141 components, and the paths of the 28 edits in `l10n/ru/patches.json` match the AssetRipper paths.

The size validates the layout: decoding must consume the whole object. If the layout cannot be built (for example, the script has `[SerializeReference]` fields or the assembly is missing) or the size does not match, the editor finds strings in that component heuristically and logs `script_layout_failed` or `script_decode_failed`. If there is no `Managed/` folder, as in an IL2CPP build, the heuristic is used for all components (`script_assemblies_unavailable`).

The heuristic checks every offset that is a multiple of 4 and accepts a string if the length is followed by valid UTF-8 of printable characters with at least one letter, and the padding is zero. Strings shorter than 2 bytes do not get into this index, and occasionally neighbouring numbers are taken for a string.

Result columns in this mode:

| Column | Value |
| --- | --- |
| File | the bundle's serialized file and the object's path ID, for example `sharedassets1.assets #111180` |
| Field | the field path, the component's `m_Name`, or `str[N]` for strings the heuristic found |
| Script | the MonoScript class with its namespace |

Fields with a known layout accept any value, including an empty string. For strings the heuristic found, the new value must pass the same filter, otherwise the string would drop out of the index. The server rejects such a value with `400`.

## Applying edits to the game

A project extracted with AssetRipper does not build back into the game. So on every save the editor appends the edit to the `patches.json` journal, and the `patcher` utility applies the journal to the original build's `data.unity3d`.

```sh
go -C src build -o ../patcher ./cmd/patcher
./patcher -patches /path/to/ExportedProject/patches.json -dry-run  /path/to/Game/Game_Data
./patcher -patches /path/to/ExportedProject/patches.json -in-place /path/to/Game/Game_Data
```

| Flag | Purpose |
| --- | --- |
| `-package FILE.lang` | apply a localization package (see below); cannot be combined with `-patches` and `-font` |
| `-patches` | edit journal |
| `-dry-run` | find the targets and print a report without writing |
| `-out FILE` | write the patched bundle to a separate file |
| `-in-place` | replace `data.unity3d`; the original is kept as `data.unity3d.orig` |
| `-strict` | treat a patch that matches more than one object as an error |
| `-font NAME=FILE.ttf` | replace the TTF in the Font object named `NAME`; the flag can be repeated |
| `-font-metrics` | `original` (default) or `font`: where `-font` takes vertical metrics from, see "Replacing fonts" |
| `-list-fonts` | list the build's fonts and whether they cover the Russian alphabet |

There is no link between a YAML export and the build's objects, so `patcher` finds the target by content. A MonoBehaviour matches if it has the same owner and script and holds the old value in the field at the patch's `path`. The owner is the GameObject name, or `m_Name` for a ScriptableObject. The script is given by DLL name and fileID or by class name. Patches apply in journal order.

`patcher` reads the field layout from `Managed/` next to `data.unity3d`, as the editor does (see "Field names"). It also classifies the strings of the component and requires the kind the patch records in `kind`. One GameObject often carries several PlayMaker FSMs: in Apocalypter the car light has `ItemName`, which shows "Light", and `LightOn`/`LightOff`, which look up the child object named "Light" at the same path. The kind check keeps the translation in `ItemName` and leaves the object name alone. Patches without `kind`, such as those from a `translation.map` written by an older `editor dump`, skip the kind check; running `editor dump` again adds it.

Without `Managed/` (`script_assemblies_unavailable` in the log), for components whose layout does not fit, and for patches with heuristic `str[N]` paths, the target is found the old way: the component's strings contain the old value, and the occurrence number picks one of several identical strings. A patch that differs from an earlier one only by `path` then skips the component the earlier one has changed. This mode can also change a string with the same text in another component of the owner.

By default a patch changes all matching objects. This way a prefab edit also reaches its copies baked into scenes. The list of changed objects (`file:pathID`) goes to the log.

If `data.unity3d.orig` exists, `-in-place` always reads the original from it. So the journal can be extended and applied again. If any patch finds no target, nothing is written.

### Localization package

For distribution, all localization files are packed into one `.lang` package, a zip archive:

```
ru.lang
├── patches.json   string edits: the editor journal, then translations from the dictionary
├── fonts.json     font replacements
└── fonts/         font files referenced by fonts.json
```

At least one of `patches.json` and `fonts.json` must be present. `fonts.json` format: `name` is the Font object name in the game (from `-list-fonts`), `file` is the TTF/OTF path relative to `fonts.json` itself, and the optional `metrics` is `original` (default) or `font`, see "Replacing fonts".

```json
{
  "version": 1,
  "fonts": [
    {"name": "Helveticrap", "file": "fonts/LiberationSans-Regular.ttf"}
  ]
}
```

Building and applying:

```sh
./patcher pack -s ./l10n/ru
./patcher -package ru.lang -dry-run  /path/to/Game/Game_Data
./patcher -package ru.lang -in-place /path/to/Game/Game_Data
```

`pack -s DIR` takes `DIR/patches.json`, the `DIR/translation.map` and `DIR/translation.po` pair, and `DIR/fonts.json` (any one of the three is enough) and creates a `<folder name>.lang` archive in the current directory: for `./l10n/ru` it is `./ru.lang`. The `-o` flag overrides the archive path. The files can also be given one by one, without `-s`: `pack -o ru.lang -patches patches.json -map translation.map -po translation.po -fonts fonts.json`.

`pack` validates the journal and `fonts.json`, parses every font and warns about missing Russian letters. Fonts go to `fonts/` under their own file names, and `fonts.json` in the archive is rewritten to these paths. A file referenced by several entries is stored once. Two different files with the same name are an error. The archive is deterministic: identical input files give a byte-identical zip.

`patcher` reads the archive without unpacking anything to disk. It rejects entries with absolute paths, `..` and `\`, duplicates, and entries larger than 64 MiB.

### Dictionary

The same string often appears in dozens of components. In Apocalypter "Nuts" sits in 130 labels, "Buy: F" in 127. Translating each one in the editor is slow, so `editor dump` collects all unique "on screen" strings into a dictionary where each string is translated once. The dictionary consists of two files:

| File | Contents | Who changes it |
| --- | --- | --- |
| `translation.po` | one gettext message per string: `msgid` is the original, `msgstr` the translation | the translator, in any PO editor (Poedit, Lokalize) or by hand |
| `translation.map` | where each string occurs, in journal terms; it holds no text | only `editor dump` |

```sh
./editor dump -game /path/to/Game/Game_Data -o ./l10n/ru
```

`-o` sets the folder both files are written to. `editor dump` takes the same `-root`, `-game`, `-patches` and `-log-level` flags as the server and works with both a YAML project and a build.

A `translation.map` entry is linked to its message by `id`: the first 16 hex digits of the SHA-256 of `msgid`. So `msgid` must not be changed. If `pack` cannot find a message's `id` in `translation.map`, it fails, and both files have to be regenerated.

```json
[
  {
    "id": "afb54131c7b775f3",
    "found_in": [
      {"file": "level1", "path": "m_Text", "owner": "game made by (1)", "script": {"assembly": "UnityEngine.UI.dll", "fileId": 708705254}, "occurrence": 0, "kind": "screen"}
    ]
  }
]
```

Above each message in `translation.po`, `editor dump` writes up to five places of the string as `file | owner | field`. PO editors show them as notes for the translator:

```po
#. level0 | point1 | m_Text
#. level0 | point2 | m_Text
#. level0 | point3 | m_Text
#. level1 | chambered_indicator | m_Text
#. level1 | dot1 | m_Text
#. … +8
msgid "."
msgstr ""
```

How `patcher` uses the dictionary:

- Messages with a non-empty `msgstr` and without the `fuzzy` flag go into the game, as in gettext. `pack` logs the number of skipped `fuzzy` messages (`dictionary_fuzzy_skipped`). `msgctxt` and plural forms are not supported.
- `pack` turns translated messages into patches and appends them to the package's `patches.json` after the journal. `patcher` finds the target by owner, script, `path`, `kind` and old value, and ignores `file`. So places that differ only in file give one patch: it replaces the string in all files anyway. Places with different paths, usually different components of one GameObject, give a patch each.
- Within one message, patches go from higher occurrence numbers to lower ones, otherwise replacing the first string would shift the numbers of the rest.
- `pack` rejects a translation that equals the original of another message with the same owner and script: the second message would replace the already translated text.

Running `editor dump` again after new edits or a game update rewrites `translation.map` and keeps the translations, flags, translator comments and header from `translation.po`. The translation of a string that is no longer in the game becomes an obsolete `#~` message, and a `translation_obsolete` warning is logged. If the string comes back, the translation is restored.

Strings are read with the journal (`-patches`) applied, because `patcher` applies the dictionary after the journal. Strings that equal a translation from the journal (the `new` value of an edit) are already translated and do not get into the dictionary. Without a journal, the Apocalypter dictionary builds in 0.8 s: 823 strings in 1993 places.

### Replacing fonts

`UnityEngine.UI.Text` draws text with a dynamic font from the TTF embedded in a Font object. If the font lacks the needed letters, the game takes them from system fonts, and the text looks mismatched. If the font has one or two decorative Cyrillic letters, they stand out from the text. Replacing the TTF with a font with full Cyrillic fixes both cases.

```sh
./patcher -list-fonts /path/to/Game/Game_Data
./patcher -patches patches.json -font "Helveticrap=/path/to/Cyrillic.ttf" -in-place /path/to/Game/Game_Data
```

`-list-fonts` shows the fonts of the current `data.unity3d` (not the backup): the object name (the one passed to `-font` and `fonts.json`), the family, the build file, the line height in em (`LINE_EM`) and the missing letters of the Russian alphabet. On replacement the object name, material, fallback fonts and settings stay the same, and the kerning table is recomputed from the new TTF by the same rules as Unity's importer.

Unity stores vertical metrics (`m_Ascent`, `m_Descent`, `m_LineSpacing`) in the Font object and lays out text by them. A `UI.Text` with `Vertical Overflow = Truncate` does not draw a line that does not fit the height of its rectangle at all. So a font with a taller line makes labels in tight frames disappear. In Apocalypter, Helveticrap has a 1.000 em line and Rubik Dirt 1.185 em, and 49 settings labels stopped fitting. Hence two modes:

| `metrics` | Metrics | When it fits |
| --- | --- | --- |
| `original` (default) | kept from the replaced font, the game layout does not change | almost always; glyphs of a taller font may slightly overhang the line |
| `font` | from the new TTF, as Unity's importer computes them | the new font is not taller than the old one, or the layout is designed for it |

`pack` and `patcher` log the line height of the old and the new font (`original_line_em`, `font_line_em`). In `font` mode a taller line triggers a `font_line_height_grows` warning.

Kerning follows FreeType's `FT_Get_Kerning`, and metrics in `font` mode are computed as Unity's importer does. The recomputation is verified on all Apocalypter fonts: in `font` mode the Font objects are rebuilt byte for byte from the source TTFs. If the new font lacks Russian letters, a `font_missing_characters` warning is logged.

TextMeshPro fonts store a pre-rendered glyph atlas (SDF). Replacing the TTF does not rebuild it: TextMeshPro takes characters missing from the atlas from fallback fonts.

How writing works:

- The bundle's original compressed blocks are copied unchanged. Changed serialized files are appended as uncompressed blocks, and the offsets of their nodes are updated in the bundle directory. The bundle grows by the size of the changed files.
- Inside a serialized file, objects after a changed one shift by a multiple of 8 bytes, so alignment is preserved.

Supported builds: UnityFS versions 6–8 with LZ4/LZ4HC or no compression, SerializedFile versions 17–22 (Unity 5.5–2022) without typetree. The MonoBehaviour, GameObject and MonoScript header layouts match Unity 2019–2022. LZMA bundles and builds with loose `.assets` files and no `data.unity3d` are not supported.

When Steam verifies file integrity or updates the game, it writes a new, unpatched `data.unity3d`, and `data.unity3d.orig` becomes stale. `patcher` and the editor (`-game`) detect such a backup: a patched bundle starts with the original's blocks, and a new Unity file does not. Instead of silently building the game from the old version, they fail with `backup does not match the game bundle`. In that case delete `data.unity3d.orig` and run `patcher -in-place` again.

## Limitations

- Classification relies on known classes and actions. Text the game displays with its own script from a third-party library or with a non-standard PlayMaker action may end up as "service"; it is visible with the "service" checkbox on.
- Field overrides in nested prefabs (`PrefabInstance → m_Modifications`) are not indexed.
- `-game` mode supports the same builds as `patcher`. The field layout rules match Unity 2020.3; for other versions the size check validates them.
- `patcher` does not find a target if the MonoBehaviour in the export has no GameObject name (stripped objects) or the old value does not occur in the build as a serialized string.
- If a scene or prefab is open in Unity with unsaved changes, Unity may overwrite the edit. Save or close them before editing.
- The server accepts requests only from loopback hosts (`localhost`, `127.0.0.1`, `::1`). POST requests require `Content-Type: application/json` and a same-origin `Origin`. This protects the tool against CSRF and DNS rebinding.

## Building

```sh
make                  # build/linux-x64/{editor,patcher}, build/win-x64/{editor,patcher}.exe
make pack             # build/<language>.lang for every folder in l10n/
make pack L10N=ru
make dump GAME=/path/to/Game/Game_Data          # translation.map and translation.po for every folder in l10n/
make dump GAME=/path/to/Game/Game_Data L10N=ru PATCHES=./patches.json
make dist             # patcher archives for every language and platform, editor archives for every platform
make cover            # tests with -race and coverage of production code, fails below 80%
```

`make dist` builds two kinds of archives:

| Archive | Contents | For |
| --- | --- | --- |
| `build/apocalypter-l10n-tools-<language>-<platform>.{tar.gz,zip}` | `patcher`, `patcher.sh` or `patcher.bat`, the `<language>.lang` package, `INSTALL_RU.txt`, `INSTALL_EN.txt` | players |
| `build/apocalypter-l10n-tools-editor-<platform>.{tar.gz,zip}` | `editor`, README | package authors |

Binaries are built statically (`CGO_ENABLED=0`, `-trimpath`) and without the symbol table and debug info (`-ldflags="-s -w"`) for linux/amd64 and windows/amd64. A single platform is built by the `editor-<platform>` or `patcher-<platform>` target, for example `make patcher-win-x64`. Archives for one platform are built by `make dist-win-x64` and `make dist-editor-win-x64`; `L10N` sets the languages that get a `patcher` archive. A single archive is built by `make dist-<language>-<platform>`, for example `make dist-ru-win-x64`. Archives are reproducible: with the same sources and the same Go version they are byte-identical. `make` without arguments first cleans the whole `build/`, including built `.lang` files.

## Development

The Go module lives in `src/`, so run the Go tools from there:

```sh
cd src
gofmt -l .
go vet ./...
go test -race -cover ./...
```
