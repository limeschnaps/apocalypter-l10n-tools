PLATFORMS := linux-x64 win-x64

# Go target and executable suffix for each platform.
GOOS_linux-x64 := linux
GOOS_win-x64   := windows
EXE_win-x64    := .exe

# The Go module lives in ./src. go -C runs every go command there, so
# paths passed to the tools are absolute.
SRC := ./src
GO  := go -C $(SRC)

# Static, path-independent binaries that run without the build machine's
# libraries.
GO_BUILD := CGO_ENABLED=0 GOARCH=amd64 $(GO) build -trimpath

all: .pre build-editor build-patcher

.pre:
	-rm -r ./build
	mkdir -p ./build

build-editor: $(PLATFORMS:%=editor-%)

build-patcher: $(PLATFORMS:%=patcher-%)

editor-%:
	GOOS=$(GOOS_$*) $(GO_BUILD) -o $(CURDIR)/build/$*/editor$(EXE_$*) ./cmd/editor

patcher-%:
	GOOS=$(GOOS_$*) $(GO_BUILD) -o $(CURDIR)/build/$*/patcher$(EXE_$*) ./cmd/patcher

# Release archives. The main one,
# build/apocalypter-l10n-tools-<platform>.{tar.gz,zip}, is what players
# get: the patcher, every localization package, the player
# instructions (INSTALL_RU.txt, INSTALL_EN.txt) and the launcher script
# that asks for a package and runs the patcher on the game. The editor
# only helps to author packages and ships separately in
# build/apocalypter-l10n-tools-editor-<platform>.{tar.gz,zip}. Each
# archive has a top-level folder. tar.gz keeps the executable bit on
# Linux; zip opens natively on Windows. Fixed timestamps, sorted entries
# and no owner data make the archives reproducible.
DIST_NAME             := apocalypter-l10n-tools
DIST_TIME             := 1980-01-01T00:00:00Z
DIST_FORMAT_linux-x64 := tar.gz
DIST_FORMAT_win-x64   := zip
LAUNCHER_linux-x64    := ./scripts/patcher.sh
LAUNCHER_win-x64      := ./scripts/patcher.bat

# $(call archive,NAME,PLATFORM,FILES) packs FILES into
# ./build/NAME.<format of PLATFORM> under the folder NAME.
define archive
rm -rf ./build/dist/$(1) ./build/$(1).$(DIST_FORMAT_$(2))
mkdir -p ./build/dist/$(1)
cp $(3) ./build/dist/$(1)/
find ./build/dist/$(1) -exec touch -d $(DIST_TIME) {} +
cd ./build/dist && case "$(DIST_FORMAT_$(2))" in \
  tar.gz) tar --sort=name --owner=0 --group=0 --numeric-owner --mtime=$(DIST_TIME) -cf - $(1) | gzip -9n > ../$(1).tar.gz ;; \
  zip) find $(1) | sort | TZ=UTC zip -qX -9 ../$(1).zip -@ ;; \
  *) echo "no archive format for $(2)" >&2; exit 1 ;; \
esac
rm -rf ./build/dist/$(1)
rmdir --ignore-fail-on-non-empty ./build/dist
endef

dist: $(PLATFORMS:%=dist-%) $(PLATFORMS:%=dist-editor-%)

dist-%: patcher-% pack
	$(call archive,$(DIST_NAME)-$*,$*,./build/$*/patcher$(EXE_$*) $(LAUNCHER_$*) INSTALL_RU.txt INSTALL_EN.txt $(L10N:%=./build/%.lang))

dist-editor-%: editor-%
	$(call archive,$(DIST_NAME)-editor-$*,$*,./build/$*/editor$(EXE_$*) README.md)

# Localizations to pack: every directory in ./l10n unless L10N is given,
# e.g. make pack L10N=ru. Packages do not depend on the platform, so the
# patcher runs for the build machine via go run.
L10N ?= $(notdir $(patsubst %/,%,$(wildcard ./l10n/*/)))

pack:
	@test -n "$(L10N)" || { echo "no localization directories in ./l10n" >&2; exit 1; }
	mkdir -p ./build
	set -e; for lang in $(L10N); do $(GO) run ./cmd/patcher pack -s $(CURDIR)/l10n/$$lang -o $(CURDIR)/build/$$lang.lang; done

# Dictionaries: editor dump rewrites translation.map and translation.po in
# ./l10n/<lang> for every language in L10N and keeps the translations
# already there. GAME is the build to read strings from: the *_Data
# directory or data.unity3d. PATCHES overrides the journal, which defaults
# to patches.json in the *_Data directory. A new language gets its
# directory, e.g. make dump GAME=/path/to/Game_Data L10N=de.
dump:
	@test -n "$(GAME)" || { echo "GAME is not set, e.g. make dump GAME=/path/to/Game_Data" >&2; exit 1; }
	@test -n "$(L10N)" || { echo "no localization directories in ./l10n, pass L10N" >&2; exit 1; }
	set -e; for lang in $(L10N); do mkdir -p ./l10n/$$lang; $(GO) run ./cmd/editor dump -game "$(abspath $(GAME))" $(if $(PATCHES),-patches "$(abspath $(PATCHES))") -o $(CURDIR)/l10n/$$lang; done

# Coverage of the production code; src/internal/unitytest only builds test
# fixtures and has no tests of its own. Fails below COVER_MIN percent.
COVER_MIN  := 80
COVER_PKGS  = $(shell $(GO) list ./... | grep -v /internal/unitytest)

cover:
	mkdir -p ./build
	$(GO) test -race -count=1 -coverprofile=$(CURDIR)/build/cover.out $(COVER_PKGS)
	@$(GO) tool cover -func=$(CURDIR)/build/cover.out | awk -v min=$(COVER_MIN) '/^total:/ { sub("%", "", $$3); print "total coverage: " $$3 "%"; if ($$3 + 0 < min) { print "below " min "%" > "/dev/stderr"; exit 1 } }'
